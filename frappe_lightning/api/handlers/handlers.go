package handlers

import (
	"fmt"

	"frappe_lightning/search/ranking"

	"github.com/gofiber/fiber/v2"
	"github.com/meilisearch/meilisearch-go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
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

	site := c.Locals("site").(string)
	rdb := c.Locals("redis").(*redis.Client)
	meili := c.Locals("meili").(meilisearch.ServiceManager)
	log := c.Locals("log").(*zap.Logger)

	// 1. Log click to Redis tracker
	tracker := ranking.NewClickTracker(rdb, log)
	tracker.LogClick(site, body.DocType, body.Name)

	// 2. Fetch new score
	newScore := tracker.GetScore(site, body.DocType, body.Name)

	// 3. Fire lightning-fast partial document update to Meilisearch directly
	indexName := fmt.Sprintf("%s_%s", slugify(site), slugify(body.DocType))
	go func() {
		doc := map[string]interface{}{
			"name":                body.Name,
			"default_click_score": newScore,
		}
		pk := "name"
		_, err := meili.Index(indexName).UpdateDocuments([]map[string]interface{}{doc}, &meilisearch.DocumentOptions{PrimaryKey: &pk})
		if err != nil {
			log.Warn("failed to push click score to meili", zap.Error(err), zap.String("doc", body.Name))
		}
	}()

	return c.SendStatus(202)
}
