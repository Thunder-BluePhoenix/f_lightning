package search

import (
	"fmt"
	"strings"
	"time"

	"frappe_lightning/ai"
	"frappe_lightning/canal"
	"frappe_lightning/config"
	"frappe_lightning/search/metrics"
	"frappe_lightning/search/ranking"

	"github.com/meilisearch/meilisearch-go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// mapRowToDoc converts a raw binlog row slice into a Meilisearch-ready map,
// including only fields listed in the schema's allowed set.
func mapRowToDoc(row []interface{}, columns []string, allowed map[string]bool) map[string]interface{} {
	doc := make(map[string]interface{}, len(columns))
	for i, col := range columns {
		if i >= len(row) {
			break
		}
		if !allowed[col] {
			continue
		}
		val := row[i]
		switch v := val.(type) {
		case []byte:
			doc[col] = string(v)
		case time.Time:
			doc[col] = v.Unix() // Store as UNIX timestamp for range filters
		default:
			doc[col] = v
		}
	}
	return doc
}

// InitIndex ensures a Meilisearch index exists with the correct attribute settings and ranking rules.
func InitIndex(client meilisearch.ServiceManager, schema *config.IndexSchema, ranking *config.RankingConfig, site string, aiMode string, dims int) error {
	indexName := schema.IndexName(site)
	index := client.Index(indexName)

	searchable := schema.Searchable
	synonyms := make(map[string][]string)
	rankingRules := []string{"words", "typo", "proximity", "attribute", "sort", "exactness"}

	if ranking != nil {
		if len(ranking.Global.RankingRules) > 0 {
			rankingRules = ranking.Global.RankingRules
		}

		if dtRank, ok := ranking.DocTypes[schema.Name]; ok {
			if len(dtRank.SearchableAttributes) > 0 {
				searchable = dtRank.SearchableAttributes
			}
			if len(dtRank.Synonyms) > 0 {
				synonyms = dtRank.Synonyms
			}
		}
	}

	settings := &meilisearch.Settings{
		SearchableAttributes: searchable,
		FilterableAttributes: schema.Filterable,
		SortableAttributes:   schema.Sortable,
		RankingRules:         rankingRules,
		Synonyms:             synonyms,
	}

	// Enable Vector Search if AI mode is local
	if aiMode == "local" {
		settings.Embedders = map[string]meilisearch.Embedder{
			"default": {
				Source: "userProvided",
				Dimensions: dims,
			},
		}
	}

	_, err := index.UpdateSettings(settings)
	if err != nil {
		return fmt.Errorf("InitIndex %s: %w", indexName, err)
	}
	return nil
}

// Engine consumes RowEvents and keeps Meilisearch in sync.
type Engine struct {
	site         string
	schemas      []config.IndexSchema
	meili        meilisearch.ServiceManager
	batcher      *Batcher
	hooks        *HookRegistry
	clickTracker *ranking.ClickTracker
	embedder     *ai.Embedder
	aiMode       string
	log          *zap.Logger
}

// NewEngine creates a configured sync engine for a single site.
func NewEngine(site *config.SiteConfig, schemas []config.IndexSchema, meili meilisearch.ServiceManager, rdb *redis.Client, aiMode string, embedder *ai.Embedder, log *zap.Logger) *Engine {
	e := &Engine{
		site:         site.Name,
		schemas:      schemas,
		meili:        meili,
		hooks:        newHookRegistry(),
		clickTracker: ranking.NewClickTracker(rdb, log),
		embedder:     embedder,
		aiMode:       aiMode,
		log:          log,
	}
	dlq := NewDLQManager(log)
	e.batcher = NewBatcher(e.flushBatch, log, dlq)
	return e
}

// Start reads events from ch and processes them. Blocks until ch is closed.
func (e *Engine) Start(ch <-chan *canal.RowEvent) {
	for event := range ch {
		// Update replication lag metric (MariaDB event timestamp vs current local time)
		if event.Timestamp > 0 {
			lagSeconds := float64(time.Now().Unix()) - float64(event.Timestamp)
			metrics.BinlogLag.WithLabelValues(e.site).Set(lagSeconds)
		}

		if err := e.process(event); err != nil {
			e.log.Error("failed to process event",
				zap.String("table", event.Table),
				zap.String("action", event.Action),
				zap.Error(err),
			)
		}
	}
}

func (e *Engine) process(event *canal.RowEvent) error {
	schema, ok := config.SchemaByTable(e.schemas, event.Table)
	if !ok {
		return nil
	}

	allowed := schema.AllowedFields()
	indexName := schema.IndexName(e.site)

	switch event.Action {
	case "insert":
		doc := mapRowToDoc(event.Rows[0], event.Columns, allowed)
		doc["doctype"] = schema.Name
		if id, ok := doc["name"].(string); ok {
			doc["default_click_score"] = e.clickTracker.GetScore(e.site, schema.Name, id)
		}
		if !e.hooks.runBeforeIndex(schema.Name, doc) {
			return nil
		}
		doc = e.hooks.runTransformDoc(schema.Name, doc)

		// Generate embedding if AI mode is local
		if e.aiMode == "local" && e.embedder != nil {
			summary := fmt.Sprintf("%v", doc) // Simple trick: embed everything for now
			// In production, we'd pick key fields like customer_name, item_name, etc.
			if emb, err := e.embedder.Embed(summary); err == nil {
				doc["_vectors"] = map[string][]float32{"default": emb}
			} else {
				metrics.IndexingErrors.WithLabelValues(e.site, schema.Name).Inc()
			}
		}

		e.batcher.Add(indexName, doc)
		metrics.IndexingTotal.WithLabelValues(e.site, schema.Name, "insert").Inc()

	case "update":
		// Binlog UPDATE delivers [before_row, after_row]
		if len(event.Rows) < 2 {
			return nil
		}
		doc := mapRowToDoc(event.Rows[1], event.Columns, allowed)
		doc["doctype"] = schema.Name
		if id, ok := doc["name"].(string); ok {
			doc["default_click_score"] = e.clickTracker.GetScore(e.site, schema.Name, id)
		}
		if !e.hooks.runBeforeIndex(schema.Name, doc) {
			return nil
		}
		doc = e.hooks.runTransformDoc(schema.Name, doc)

		// Generate embedding if AI mode is local
		if e.aiMode == "local" && e.embedder != nil {
			summary := fmt.Sprintf("%v", doc)
			if emb, err := e.embedder.Embed(summary); err == nil {
				doc["_vectors"] = map[string][]float32{"default": emb}
			} else {
				metrics.IndexingErrors.WithLabelValues(e.site, schema.Name).Inc()
			}
		}

		e.batcher.Add(indexName, doc)
		metrics.IndexingTotal.WithLabelValues(e.site, schema.Name, "update").Inc()

	case "delete":
		doc := mapRowToDoc(event.Rows[0], event.Columns, allowed)
		if id, ok := doc["name"].(string); ok && id != "" {
			_, err := e.meili.Index(indexName).DeleteDocument(id, nil)
			if err != nil {
				metrics.IndexingErrors.WithLabelValues(e.site, schema.Name).Inc()
				e.log.Warn("failed to delete document",
					zap.String("index", indexName),
					zap.String("name", id),
					zap.Error(err),
				)
			}
		}
	}
	return nil
}

func (e *Engine) flushBatch(indexName string, docs []interface{}) error {
	pk := "name"
	_, err := e.meili.Index(indexName).AddDocuments(docs, &meilisearch.DocumentOptions{PrimaryKey: &pk})
	return err
}

// slugifyIndex converts table name to a clean index suffix for display.
func slugifyIndex(table string) string {
	s := strings.ToLower(table)
	s = strings.TrimPrefix(s, "tab")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}
