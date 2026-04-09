package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"frappe_lightning/config"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/redis/go-redis/v9"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

// Server is the API Gateway Fiber application.
type Server struct {
	app     *fiber.App
	proxy   *Proxy
	limiter *RateLimiter
	cache   *ResponseCache
	cfg     *config.GatewayConfig
	redis   map[string]*redis.Client // keyed by site name
	log     *zap.Logger
}

// NewServer constructs and wires the gateway.
// redisClients must contain one entry per site configured in cfg.Sites.
func NewServer(cfg *config.GatewayConfig, redisClients map[string]*redis.Client, log *zap.Logger) *Server {
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
	})

	// Use the first site's Redis client for shared rate-limiting and caching.
	// In a multi-tenant setup each site brings its own Redis — we pick one per
	// request once the site is resolved (handled inside the handler).
	var sharedRedis *redis.Client
	for _, rdb := range redisClients {
		sharedRedis = rdb
		break
	}

	proxy := NewProxy(cfg.Sites, cfg.CircuitBreaker, log)
	limiter := NewRateLimiter(sharedRedis, cfg.RateLimits, 50)

	var cacheInst *ResponseCache
	if cfg.Cache.Enabled {
		cacheInst = NewResponseCache(sharedRedis, cfg.Cache.Rules)
	}

	s := &Server{
		app:     app,
		proxy:   proxy,
		limiter: limiter,
		cache:   cacheInst,
		cfg:     cfg,
		redis:   redisClients,
		log:     log,
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	s.app.Use(recover.New())

	// Admin endpoints (no upstream proxy).
	s.app.Get("/lightning/gateway/health", s.handleHealth)
	s.app.Get("/lightning/gateway/stats", s.handleStats)
	s.app.Delete("/lightning/gateway/cache", s.handleCacheFlush)

	// All other requests are proxied.
	s.app.All("/*", s.handleProxy)
}

// handleProxy is the core request handler. It:
//  1. Resolves the site from the Host header.
//  2. Checks the cache (GET only).
//  3. Validates the session (unless path is on the skip list).
//  4. Enforces per-user rate limits.
//  5. Forwards to the upstream via the circuit-broken proxy.
//  6. Stores cacheable responses.
func (s *Server) handleProxy(c *fiber.Ctx) error {
	site := s.resolveSite(c)
	path := string(c.Request().URI().Path())
	method := c.Method()
	query := string(c.Request().URI().QueryString())

	ctx := context.Background()

	// ── Cache check (GET only) ──────────────────────────────────────────────
	if s.cache != nil && method == "GET" {
		if cached, err := s.cache.Get(ctx, site, method, path, query); err == nil && cached != nil {
			s.cache.RecordHit(ctx, site)
			c.Set("X-Lightning-Cache", "HIT")
			for k, v := range cached.Headers {
				c.Set(k, v)
			}
			return c.Status(cached.StatusCode).Send(cached.Body)
		}
		s.cache.RecordMiss(ctx, site)
	}

	// ── Auth check ─────────────────────────────────────────────────────────
	if !s.isSkippedPath(path) {
		rdb := s.redisFor(site)
		user, err := s.validateSession(ctx, c, site, rdb)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": err.Error()})
		}

		// ── Per-user rate limit ─────────────────────────────────────────────
		if !s.limiter.Allow(ctx, site, user, path) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		}

		// Inject user identity for the upstream so Frappe can trust it.
		c.Request().Header.Set("X-Frappe-User", user)
	}

	// Invalidate cache entries on mutating requests.
	if s.cache != nil && (method == "POST" || method == "PUT" || method == "DELETE" || method == "PATCH") {
		go s.cache.Invalidate(ctx, site, path)
	}

	// ── Reverse proxy ──────────────────────────────────────────────────────
	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(upReq)
	defer fasthttp.ReleaseResponse(upResp)

	// Copy the incoming request into the upstream request.
	c.Request().CopyTo(upReq)
	upReq.Header.Set("X-Real-IP", c.IP())
	upReq.Header.Set("X-Forwarded-For", c.IP())
	upReq.Header.Set("X-Forwarded-Proto", c.Protocol())

	start := time.Now()
	if err := s.proxy.Forward(site, upReq, upResp); err != nil {
		s.log.Error("proxy forward failed", zap.String("site", site), zap.String("path", path), zap.Error(err))
		if strings.Contains(err.Error(), "circuit open") {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error": "upstream unavailable — please try again shortly",
			})
		}
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "bad gateway"})
	}
	latencyMs := time.Since(start).Milliseconds()
	c.Set("X-Lightning-Latency", fmt.Sprintf("%dms", latencyMs))

	// Copy upstream response headers back to the client.
	upResp.Header.VisitAll(func(k, v []byte) {
		key := string(k)
		// Skip hop-by-hop headers.
		switch strings.ToLower(key) {
		case "connection", "keep-alive", "transfer-encoding", "upgrade":
			return
		}
		c.Set(key, string(v))
	})

	statusCode := upResp.StatusCode()
	body := upResp.Body()

	// ── Cache store ────────────────────────────────────────────────────────
	if s.cache != nil && method == "GET" {
		headers := make(map[string]string)
		upResp.Header.VisitAll(func(k, v []byte) {
			headers[string(k)] = string(v)
		})
		go s.cache.Set(ctx, site, method, path, query, statusCode, headers, append([]byte{}, body...))
	}

	return c.Status(statusCode).Send(body)
}

// ── Admin handlers ────────────────────────────────────────────────────────────

func (s *Server) handleHealth(c *fiber.Ctx) error {
	type siteStatus struct {
		Site    string `json:"site"`
		Circuit string `json:"circuit"`
		Workers int    `json:"workers"`
	}
	statuses := make([]siteStatus, 0, len(s.cfg.Sites))
	for _, gs := range s.cfg.Sites {
		statuses = append(statuses, siteStatus{
			Site:    gs.Name,
			Circuit: s.proxy.BreakerState(gs.Name),
			Workers: len(gs.UpstreamWorkers),
		})
	}
	return c.JSON(fiber.Map{"status": "ok", "sites": statuses})
}

func (s *Server) handleStats(c *fiber.Ctx) error {
	if s.cache == nil {
		return c.JSON(fiber.Map{"cache": "disabled"})
	}
	ctx := context.Background()
	stats := make(map[string]fiber.Map)
	for _, gs := range s.cfg.Sites {
		hits, misses := s.cache.Stats(ctx, gs.Name)
		var hitRate float64
		if total := hits + misses; total > 0 {
			hitRate = float64(hits) / float64(total) * 100
		}
		stats[gs.Name] = fiber.Map{
			"hits":     hits,
			"misses":   misses,
			"hit_rate": fmt.Sprintf("%.1f%%", hitRate),
		}
	}
	return c.JSON(fiber.Map{"cache": stats})
}

func (s *Server) handleCacheFlush(c *fiber.Ctx) error {
	if s.cache == nil {
		return c.JSON(fiber.Map{"flushed": 0})
	}
	site := c.Query("site")
	ctx := context.Background()
	var total int64
	if site != "" {
		n, _ := s.cache.FlushSite(ctx, site)
		total = n
	} else {
		for _, gs := range s.cfg.Sites {
			n, _ := s.cache.FlushSite(ctx, gs.Name)
			total += n
		}
	}
	return c.JSON(fiber.Map{"flushed": total})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// resolveSite returns the site name from the Host header or falls back to the
// X-Frappe-Site-Name header (set by the existing Lightning search middleware).
func (s *Server) resolveSite(c *fiber.Ctx) string {
	if name := c.Get("X-Frappe-Site-Name"); name != "" {
		return name
	}
	host := strings.Split(c.Hostname(), ":")[0]
	// Verify it's a known gateway site; otherwise return the host as-is.
	for _, gs := range s.cfg.Sites {
		if gs.Name == host {
			return host
		}
	}
	// Single-site convenience: return the only configured site.
	if len(s.cfg.Sites) == 1 {
		return s.cfg.Sites[0].Name
	}
	return host
}

func (s *Server) redisFor(site string) *redis.Client {
	if rdb, ok := s.redis[site]; ok {
		return rdb
	}
	// Fallback to any available client.
	for _, rdb := range s.redis {
		return rdb
	}
	return nil
}

func (s *Server) isSkippedPath(path string) bool {
	for _, skip := range s.cfg.Auth.SkipPaths {
		if strings.HasPrefix(path, skip) {
			return true
		}
	}
	return false
}

// validateSession reads the Frappe sid cookie and validates it against Redis.
// Returns the username or an error if the session is invalid.
func (s *Server) validateSession(ctx context.Context, c *fiber.Ctx, site string, rdb *redis.Client) (string, error) {
	// Allow Bearer token (API key) to skip cookie validation.
	if auth := c.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return "api_user", nil
	}

	if rdb == nil {
		return "", fmt.Errorf("no Redis client available for site %q", site)
	}

	sid := c.Cookies("sid")
	if sid == "" || sid == "Guest" {
		return "", fmt.Errorf("missing or guest session")
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	// Try the Frappe v15/v16 session key format.
	sessionKey := site + "|sessiondata|" + sid
	data, err := rdb.Get(ctxTimeout, sessionKey).Result()
	if err == redis.Nil {
		data, err = rdb.Get(ctxTimeout, sid).Result()
	}
	if err != nil || data == "" {
		return "", fmt.Errorf("invalid or expired session")
	}

	user := extractUser(data)
	if user == "" || user == "Guest" {
		return "", fmt.Errorf("forbidden")
	}
	return user, nil
}

// extractUser is a minimal parser for Frappe's session data string.
// Frappe v15/v16 stores sessions as JSON; older versions may use pickle.
func extractUser(data string) string {
	// Look for "user": "..." pattern.
	const key = `"user":"`
	idx := strings.Index(data, key)
	if idx < 0 {
		// Fallback: check for Administrator string in the data.
		if strings.Contains(data, "Administrator") {
			return "Administrator"
		}
		return ""
	}
	start := idx + len(key)
	end := strings.IndexByte(data[start:], '"')
	if end < 0 {
		return ""
	}
	return data[start : start+end]
}

// Start listens on the configured port.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.ListenPort)
	s.log.Info("⚡ Gateway listening", zap.String("addr", addr))
	return s.app.Listen(addr)
}

// Stop shuts down the gateway gracefully.
func (s *Server) Stop() error {
	return s.app.Shutdown()
}
