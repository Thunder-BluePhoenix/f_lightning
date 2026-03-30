package search

import (
	"fmt"
	"strings"
	"time"

	"frappe_lightning/canal"
	"frappe_lightning/config"

	"github.com/meilisearch/meilisearch-go"
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

// InitIndex ensures a Meilisearch index exists with the correct attribute settings.
func InitIndex(client meilisearch.ServiceManager, schema *config.IndexSchema, site string) error {
	indexName := schema.IndexName(site)
	index := client.Index(indexName)

	_, err := index.UpdateSettings(&meilisearch.Settings{
		SearchableAttributes: schema.Searchable,
		FilterableAttributes: schema.Filterable,
		SortableAttributes:   schema.Sortable,
		RankingRules: []string{
			"words", "typo", "proximity", "attribute", "sort", "exactness",
		},
	})
	if err != nil {
		return fmt.Errorf("InitIndex %s: %w", indexName, err)
	}
	return nil
}

// Engine consumes RowEvents and keeps Meilisearch in sync.
type Engine struct {
	site    string
	schemas []config.IndexSchema
	meili   meilisearch.ServiceManager
	batcher *Batcher
	hooks   *HookRegistry
	log     *zap.Logger
}

// NewEngine creates a configured sync engine for a single site.
func NewEngine(site *config.SiteConfig, schemas []config.IndexSchema, meili meilisearch.ServiceManager, log *zap.Logger) *Engine {
	e := &Engine{
		site:    site.Name,
		schemas: schemas,
		meili:   meili,
		hooks:   newHookRegistry(),
		log:     log,
	}
	e.batcher = NewBatcher(e.flushBatch, log)
	return e
}

// Start reads events from ch and processes them. Blocks until ch is closed.
func (e *Engine) Start(ch <-chan *canal.RowEvent) {
	for event := range ch {
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
		if !e.hooks.runBeforeIndex(schema.Name, doc) {
			return nil
		}
		doc = e.hooks.runTransformDoc(schema.Name, doc)
		e.batcher.Add(indexName, doc)

	case "update":
		// Binlog UPDATE delivers [before_row, after_row]
		if len(event.Rows) < 2 {
			return nil
		}
		doc := mapRowToDoc(event.Rows[1], event.Columns, allowed)
		doc["doctype"] = schema.Name
		if !e.hooks.runBeforeIndex(schema.Name, doc) {
			return nil
		}
		doc = e.hooks.runTransformDoc(schema.Name, doc)
		e.batcher.Add(indexName, doc)

	case "delete":
		doc := mapRowToDoc(event.Rows[0], event.Columns, allowed)
		if id, ok := doc["name"].(string); ok && id != "" {
			_, err := e.meili.Index(indexName).DeleteDocument(id, nil)
			if err != nil {
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
