package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/meilisearch/meilisearch-go"
	"github.com/redis/go-redis/v9"
)

// Suggest returns autocomplete hints like top queried terms.
func Suggest(c *fiber.Ctx) error {
	// Currently a stub. In Phase 4, we query the `frappe_lightning_logs` index
	// for popular, recent search terms.
	q := c.Query("q")
	return c.JSON(fiber.Map{
		"suggestions": []string{
			q + " invoices",
			q + " customers",
		},
	})
}

// Autocomplete retrieves matching document IDs quickly as a user types a field value.
func Autocomplete(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"options": []string{},
	})
}

// HealthCheck verifies Meilisearch and Redis connections.
func HealthCheck(c *fiber.Ctx) error {
	rdb := c.Locals("redis").(*redis.Client)
	meili := c.Locals("meili").(meilisearch.ServiceManager)

	// Redis ping
	rHealth := "ok"
	if err := rdb.Ping(c.Context()).Err(); err != nil {
		rHealth = "unreachable"
	}

	// Meilisearch health
	mHealth := "ok"
	_, err := meili.Health()
	if err != nil {
		mHealth = "unreachable"
	}

	status := 200
	if rHealth != "ok" || mHealth != "ok" {
		status = 503
	}

	return c.Status(status).JSON(fiber.Map{
		"status": "online",
		"dependencies": fiber.Map{
			"redis":       rHealth,
			"meilisearch": mHealth,
		},
	})
}

// LogClick records that a user opened a specific document from search results.
// Used for usage-based ranking and CTR analytics.
func LogClick(c *fiber.Ctx) error {
	var body struct {
		DocType string `json:"doctype"`
		Name    string `json:"name"`
		Query   string `json:"query"`
	}

	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid format"})
	}

	// In Phase 6, we write this click signal to Redis to boost the popularity
	// score of this DocType+Name combination.
	// For now, return success.
	return c.SendStatus(202)
}
