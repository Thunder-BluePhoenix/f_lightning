package middleware

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// AuthRequired validates the Frappe 'sid' cookie against the Redis session store.
// Alternatively, accepts a Bearer API token for external consumers.
func AuthRequired(c *fiber.Ctx) error {
	log := c.Locals("log").(*zap.Logger)
	rdb := c.Locals("redis").(*redis.Client)
	site := c.Locals("site").(string)

	var user string
	var sessionData string
	var err error

	// 1. Try API Token First (for external/mobile apps)
	authHeader := c.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		// In a real implementation this would check a token table/cache.
		// For now we simulate an admin token bypass.
		if token == "dev-token-xyz" {
			c.Locals("user", "api_user")
			c.Locals("roles", []string{"System Manager", "Search API User"})
			return c.Next()
		}
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid API token"})
	}

	// 2. Fall back to Frappe standard session cookie ('sid')
	sid := c.Cookies("sid")
	if sid == "" || sid == "Guest" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing session cookie"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Frappe stores sessions in Redis as string values under keys matching the sid.
	// We extract the user and roles from Frappe's pickled or JSON-dumped session dictionary.
	// By default Frappe's redis cache namespace keys like `siteName|sessionData|{sid}` or just `{sid}`.
	// NOTE: Depending on Frappe version, we may need to decode base64 or unpickle. We assume JSON here for simplicity.
	
	// Try typical Frappe v15/v16 session key format:
	sessionKey := site + "|seesiondata|" + sid
	sessionData, err = rdb.Get(ctx, sessionKey).Result()
	if err == redis.Nil {
		// Try fallback generic key
		sessionData, err = rdb.Get(ctx, sid).Result()
	}

	if err != nil && err != redis.Nil {
		log.Error("redis session fetch error", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "session validation timeout"})
	}

	if sessionData == "" || err == redis.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid or expired session"})
	}

	// In a complete port of frappe.sessions.Session, we'd parse the user and roles here.
	// For now, we mock the extraction of the user from the raw session string.
	// E.g., Frappe session dict contains {"user": "admin@example.com"}
	user = extractUserFromFrappeSession(sessionData)
	if user == "" || user == "Guest" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "forbidden"})
	}

	// In a real Frappe integration we would fetch roles for `user` from a local Redis cache or DB map.
	// We inject mock roles here to enable RBAC in the next layer.
	roles := []string{"System Manager", "Sales User"} // MOCK

	c.Locals("user", user)
	c.Locals("roles", roles)

	// Refresh session expiry (frappe sliding expiration style)
	go rdb.Expire(context.Background(), sessionKey, 24*time.Hour)

	return c.Next()
}

// extractUserFromFrappeSession is a dummy parser.
// Real Frappe uses Python `pickle` or `json`.
// If it's a python pickle we'd need a simple regex or a small python sidecar/redis-module hook.
func extractUserFromFrappeSession(data string) string {
	if strings.Contains(data, "Administrator") {
		return "Administrator"
	}
	return "test@example.com"
}
