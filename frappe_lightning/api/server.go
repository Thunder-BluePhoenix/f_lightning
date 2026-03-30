package api

import (
	"fmt"
	"time"

	"frappe_lightning/api/handlers"
	"frappe_lightning/api/middleware"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"github.com/valyala/fasthttp/fasthttpadaptor"
)

// Server encapsulates the Fiber HTTP server and dependencies via Multi-Tenancy.
type Server struct {
	app     *fiber.App
	tenants map[string]*middleware.Tenant
	log     *zap.Logger
}

// NewServer initializes the Fiber app with multi-tenant middleware and routes.
func NewServer(tenants map[string]*middleware.Tenant, log *zap.Logger) *Server {
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			log.Error("API error", zap.Error(err), zap.String("path", c.Path()))
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
	})

	s := &Server{app: app, tenants: tenants, log: log}
	s.setupMiddleware()
	s.setupRoutes()
	return s
}

func (s *Server) setupMiddleware() {
	// Panic recovery
	s.app.Use(recover.New())

	// CORS whitelist based on config (globally loose for multi-tenant, strict via Nginx in production)
	s.app.Use(cors.New(cors.Config{
		AllowOrigins:     "*",
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization, X-Frappe-Site-Name",
		AllowCredentials: true,
	}))

	// Rate Limiting
	s.app.Use(limiter.New(limiter.Config{
		Max:        150,
		Expiration: 10 * time.Second,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "too many requests",
			})
		},
	}))

	// NOTE: SiteResolver is now applied to specific API groups to allow
	// global endpoints like /metrics and /health to function without a site context.
}

func (s *Server) setupRoutes() {
	// Global Metrics (No Site Context required)
	s.app.Get("/metrics", func(c *fiber.Ctx) error {
		fasthttpadaptor.NewFastHTTPHandler(promhttp.Handler())(c.Context())
		return nil
	})

	v1 := s.app.Group("/api/v1")

	// Public (No Auth)
	v1.Get("/health", handlers.HealthCheck)

	// Tenant-Aware Routes (Require SiteResolver)
	tenantAware := v1.Group("", middleware.SiteResolver(s.tenants, s.log))

	// Protected Routes (Require Auth)
	protected := tenantAware.Group("", middleware.AuthRequired)
	protected.Get("/search", handlers.Search)
	protected.Get("/suggest", handlers.Suggest)
	protected.Get("/autocomplete", handlers.Autocomplete)
	protected.Post("/analytics/click", handlers.LogClick)
}

// Start listens on the generic fallback port (e.g. 8765) or Frappe configuration default.
func (s *Server) Start(port int) error {
	addr := fmt.Sprintf(":%d", port)
	s.log.Info("starting global Multi-Tenant API server", zap.String("addr", addr), zap.Int("tenants_initialized", len(s.tenants)))
	return s.app.Listen(addr)
}

// Stop initiates graceful shutdown.
func (s *Server) Stop() error {
	return s.app.Shutdown()
}
