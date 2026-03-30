package middleware

import (
	"strings"

	"frappe_lightning/ai"
	"frappe_lightning/config"

	"github.com/gofiber/fiber/v2"
	"github.com/meilisearch/meilisearch-go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Tenant represents a single connected Frappe site and its resource pools.
type Tenant struct {
	Config   *config.SiteConfig
	Meili    meilisearch.ServiceManager
	Redis    *redis.Client
	AIMode   string
	Embedder *ai.Embedder
}

// SiteResolver automatically detects the Frappe Site name from the request context
// and injects the corresponding Tenant dependencies into Fiber locals.
func SiteResolver(tenants map[string]*Tenant, log *zap.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Detect Site: Frappe passes `X-Frappe-Site-Name` through Nginx.
		// Alternatively, parse the Host header.
		siteName := c.Get("X-Frappe-Site-Name")
		if siteName == "" {
			// Fallback to Hostname if proxy passes it
			host := strings.Split(c.Hostname(), ":")[0]
			siteName = host
		}

		tenant, exists := tenants[siteName]
		if !exists {
			// Specific Fallback for default local dev mapping
			if t, ok := tenants["erp.local"]; ok && (siteName == "localhost" || siteName == "127.0.0.1") {
				tenant = t
			} else {
                // If there's only 1 tenant installed, fallback to it safely
                if len(tenants) == 1 {
                    for _, v := range tenants {
                        tenant = v
                        break
                    }
                } else {
				    log.Warn("rejected request for unknown site", zap.String("site", siteName))
				    return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unknown tenant site"})
                }
			}
		}

		// Inject tenant-specific services
		c.Locals("site", tenant.Config.Name)
		c.Locals("meili", tenant.Meili)
		c.Locals("redis", tenant.Redis)
		c.Locals("ai_mode", tenant.AIMode)
		c.Locals("embedder", tenant.Embedder)
		c.Locals("log", log.With(zap.String("site", tenant.Config.Name)))

		return c.Next()
	}
}
