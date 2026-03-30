package handlers

import (
	"fmt"
	"strings"
	"time"

	"frappe_lightning/ai"
	"frappe_lightning/api/rbac"
	"frappe_lightning/nlp"
	"frappe_lightning/search/metrics"

	"github.com/gofiber/fiber/v2"
	"github.com/meilisearch/meilisearch-go"
	"go.uber.org/zap"
)

// Search handles the primary natural language search from the Frappe UI.
func Search(c *fiber.Ctx) error {
	q := c.Query("q")
	if q == "" {
		return c.JSON(fiber.Map{"hits": []interface{}{}, "took_ms": 0})
	}

	// 1. Dependency Injection from context
	site := c.Locals("site").(string)
	meili := c.Locals("meili").(meilisearch.ServiceManager)
	user := c.Locals("user").(string)
	roles := c.Locals("roles").([]string)
	aiMode := c.Locals("ai_mode").(string)
	embedder := c.Locals("embedder").(*ai.Embedder)
	log := c.Locals("log").(*zap.Logger)

	start := time.Now()

	// 2. Local NLP Parse
	parsedQuery := nlp.BuildQuery(q)
	
	// 3. Translate to Meilisearch Filter DSL
	meiliParams := nlp.ToMeili(parsedQuery)

	// 4. Inject Frappe User Permissions (Security Layer)
	rbacFilter := rbac.BuildPermissionFilter(user, roles, parsedQuery.DocType)

	// Merge RBAC filters with NLP filters
	var finalFilters []string
	if rawFilters, ok := meiliParams["filter"].([]string); ok && len(rawFilters) > 0 {
		finalFilters = append(finalFilters, strings.Join(rawFilters, " AND "))
	}
	if rbacFilter != "" {
		finalFilters = append(finalFilters, rbacFilter)
	}

	filterStrs := make([]interface{}, len(finalFilters))
	for i, f := range finalFilters {
		filterStrs[i] = f
	}

	// 5. Determine Target Index
	var indexName string
	if parsedQuery.DocType != "" {
		indexName = fmt.Sprintf("%s_%s", slugify(site), slugify(parsedQuery.DocType))
	} else {
		// Multi-index fallback. In v1 we'll default to searching across a globally aliased/federated index
		// or just the most common DocType if not specified. Meilisearch allows federated mult-search
		indexName = fmt.Sprintf("%s_sales_invoice", slugify(site)) // Demo fallback
	}

	// 6. Execute Search
	req := &meilisearch.SearchRequest{
		Limit:  int64(parsedQuery.Limit),
	}
	if len(filterStrs) > 0 {
		req.Filter = filterStrs
	}

	// 7. Add Vector (Hybrid Search) if AI mode is local
	if aiMode == "local" && embedder != nil {
		emb, err := embedder.Embed(parsedQuery.Text)
		if err == nil {
			req.Vector = emb
			// In hybrid search, we can also adjust ranking score
			req.Hybrid = &meilisearch.SearchRequestHybrid{
				SemanticRatio: 0.5, // Balance keyword vs semantic
				Embedder:      "default",
			}
		} else {
			log.Warn("embedding failed, falling back to keyword search", zap.Error(err))
		}
	}

	res, err := meili.Index(indexName).Search(parsedQuery.Text, req)
	if err != nil {
		log.Error("meilisearch query failed", zap.Error(err), zap.String("index", indexName))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "search engine error"})
	}

	tookMs := time.Since(start).Milliseconds()
	metrics.SearchDuration.WithLabelValues(site).Observe(float64(tookMs) / 1000.0)

	// Fire async tracking log
	go logSearchAsync(site, user, q, parsedQuery.DocType, int(res.TotalHits), int(tookMs))

	return c.JSON(fiber.Map{
		"hits":        res.Hits,
		"parsed":      parsedQuery,
		"took_ms":     tookMs,
		"total":       res.TotalHits,
		"ai_enabled":  aiMode == "local",
		"is_semantic": req.Vector != nil,
	})
}

// Minimal async logger (for Phase 4 Dashboard)
func logSearchAsync(site, user, rawQuery, doctype string, hits, tookMs int) {
	// In production, insert a record into Frappe's `tabLightning Search Log` or ClickHouse
	fmt.Printf("[ANALYTICS] Site: %s | User: %s | Q: %s | Hits: %d | Latency: %dms\n", site, user, rawQuery, hits, tookMs)
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, ".", "_")
	s = strings.ReplaceAll(s, "-", "_")
	return s
}
