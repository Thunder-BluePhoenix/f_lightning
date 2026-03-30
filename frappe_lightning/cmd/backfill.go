package cmd

import (
	"database/sql"
	"fmt"
	"time"

	"frappe_lightning/config"

	_ "github.com/go-sql-driver/mysql"
	"github.com/meilisearch/meilisearch-go"
	"go.uber.org/zap"
)

// Backfill performs a paginated full-sync of a table into Meilisearch.
func Backfill(site *config.SiteConfig, schema config.IndexSchema, meili meilisearch.ServiceManager, log *zap.Logger) error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true",
		site.MariaDB.User, site.MariaDB.Password,
		site.MariaDB.Host, site.MariaDB.Port,
		site.Name, // Database == site name in Frappe
	)

	// Since we are replacing mysqldump, we connect directly via database/sql
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("failed to open db: %w", err)
	}
	defer db.Close()

	// 1. Get total row count for progress indicator
	var total int
	err = db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", schema.Table)).Scan(&total)
	if err != nil {
		return fmt.Errorf("failed to count rows in %s: %w", schema.Table, err)
	}

	log.Info("starting backfill",
		zap.String("doctype", schema.Name),
		zap.Int("total_rows", total),
	)

	if total == 0 {
		return nil
	}

	indexName := schema.IndexName(site.Name)
	index := meili.Index(indexName)
	batchSize := 5000
	offset := 0

	// Select only the fields defined in the schema
	queryFields := "`" + schema.Fields[0] + "`"
	for i := 1; i < len(schema.Fields); i++ {
		queryFields += ", `" + schema.Fields[i] + "`"
	}

	for offset < total {
		query := fmt.Sprintf("SELECT %s FROM `%s` LIMIT %d OFFSET %d", queryFields, schema.Table, batchSize, offset)
		rows, err := db.Query(query)
		if err != nil {
			return fmt.Errorf("query failed at offset %d: %w", offset, err)
		}

		cols, _ := rows.Columns()
		batch := make([]interface{}, 0, batchSize)

		for rows.Next() {
			// Dynamic row scanning
			columns := make([]interface{}, len(cols))
			columnPointers := make([]interface{}, len(cols))
			for i := range columns {
				columnPointers[i] = &columns[i]
			}

			if err := rows.Scan(columnPointers...); err != nil {
				rows.Close()
				return err
			}

			doc := make(map[string]interface{})
			doc["doctype"] = schema.Name
			for i, colName := range cols {
				val := columns[i]
				if b, ok := val.([]byte); ok {
					doc[colName] = string(b)
				} else {
					doc[colName] = val
				}
			}
			batch = append(batch, doc)
		}
		rows.Close()

		if len(batch) > 0 {
			pk := "name"
			_, err = index.AddDocuments(batch, &meilisearch.DocumentOptions{PrimaryKey: &pk})
			if err != nil {
				log.Error("batch AddDocuments failed", zap.Int("offset", offset), zap.Error(err))
				// In a full CLI we could retry here, but logging is fine for Phase 2
			} else {
				log.Info("backfill progress", 
					zap.String("doctype", schema.Name), 
					zap.Int("processed", offset+len(batch)), 
					zap.Int("total", total),
				)
			}
		}

		offset += batchSize
		time.Sleep(100 * time.Millisecond) // Be gentle on Frappe's MariaDB
	}

	log.Info("backfill complete", zap.String("doctype", schema.Name))
	return nil
}
