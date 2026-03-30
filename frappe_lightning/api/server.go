package api

import (
	"fmt"
	"time"

	"frappe_lightning/api/handlers"
	"frappe_lightning/api/middleware"
	"frappe_lightning/config"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/meilisearch/meilisearch-go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Server encapsulates the Fiber HTTP server and dependencies.
type Server struct {
	app   *fiber.App
	cfg   *config.SiteConfig
	meili meilisearch.ServiceManager
	rdb   *redis.Client
	log   *zap.Logger
}

// NewServer initializes the Fiber app with middleware and routes.
func NewServer(cfg *config.SiteConfig, meili meilisearch.ServiceManager, rdb *redis.Client, log *zap.Logger) *Server {
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

	s := &Server{app: app, cfg: cfg, meili: meili, rdb: rdb, log: log}
	s.setupMiddleware()
	s.setupRoutes()
	return s
}

func (s *Server) setupMiddleware() {
	// Panic recovery
	s.app.Use(recover.New())

	// CORS whitelist based on config
	origins := ""
	if s.cfg.API.AllowedOrigins != "" {
		origins = s.cfg.API.AllowedOrigins
	} else {
		origins = "*"
	}
	s.app.Use(cors.New(cors.Config{
		AllowOrigins:     origins,
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization",
		AllowCredentials: true,
	}))

	// Rate Limiting (100 requests per 10 seconds per IP implies high burst capacity but caps abuse)
	s.app.Use(limiter.New(limiter.Config{
		Max:        100,
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

	// Inject Site, Meilisearch, Redis and Logger into context for handlers
	s.app.Use(func(c *fiber.Ctx) error {
		c.Locals("site", s.cfg.Name)
		c.Locals("meili", s.meili)
		c.Locals("redis", s.rdb)
		c.Locals("log", s.log)
		return c.Next()
	})
}

func (s *Server) setupRoutes() {
	v1 := s.app.Group("/api/v1")

	// Public (No Auth)
	v1.Get("/health", handlers.HealthCheck)

	// Protected Routes
	protected := v1.Group("", middleware.AuthRequired)
	protected.Get("/search", handlers.Search)
	protected.Get("/suggest", handlers.Suggest)
	protected.Get("/autocomplete", handlers.Autocomplete)
	protected.Post("/analytics/click", handlers.LogClick)
}

// Start listens on the configured port. Blocks until error or shutdown.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.API.Port)
	s.log.Info("starting API server", zap.String("site", s.cfg.Name), zap.String("addr", addr))
	return s.app.Listen(addr)
}

// Stop initiates graceful shutdown.
func (s *Server) Stop() error {
	return s.app.Shutdown()
}
