package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"frappe_lightning/api"
	"frappe_lightning/canal"
	"frappe_lightning/config"
	"frappe_lightning/search"

	"github.com/meilisearch/meilisearch-go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	flag.Parse()

	// --- Logger ---
	log, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync()

	log.Info("⚡ Frappe Lightning starting", zap.String("config", *configPath))

	// --- Config ---
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal("failed to load config", zap.Error(err))
	}

	schemas := config.DefaultSchemas()

	// --- Start one listener + engine per site ---
	for i := range cfg.Sites {
		site := &cfg.Sites[i]

		// Meilisearch client
		meiliClient := meilisearch.New(
			site.Meilisearch.Host,
			meilisearch.WithAPIKey(site.Meilisearch.MasterKey),
		)

		// Initialize all tracked indexes in Meilisearch
		for j := range schemas {
			if err := search.InitIndex(meiliClient, &schemas[j], site.Name); err != nil {
				log.Warn("failed to init index",
					zap.String("site", site.Name),
					zap.String("index", schemas[j].IndexSuffix),
					zap.Error(err),
				)
			} else {
				log.Info("index ready",
					zap.String("site", site.Name),
					zap.String("index", schemas[j].IndexName(site.Name)),
				)
			}
		}

		// Shared channel: binlog listener → sync engine
		eventsCh := make(chan *canal.RowEvent, 1024)

		// Sync engine (consumes events, writes to Meilisearch)
		engine := search.NewEngine(site, schemas, meiliClient, log)
		go engine.Start(eventsCh)

		// Binlog listener (produces events from MariaDB)
		go func(s *config.SiteConfig) {
			if err := canal.Start(s, schemas, eventsCh, log); err != nil {
				log.Error("canal listener stopped",
					zap.String("site", s.Name),
					zap.Error(err),
				)
			}
		}(site)

		// Redis client (for Frappe session validation)
		rdb := redis.NewClient(&redis.Options{
			Addr: site.Redis.Addr(),
		})

		// API HTTP Server
		server := api.NewServer(site, meiliClient, rdb, log)
		go func(s *api.Server) {
			if err := s.Start(); err != nil {
				log.Error("API server stopped", zap.Error(err))
			}
		}(server)

		log.Info("site started",
			zap.String("site", site.Name),
			zap.String("mariadb", fmt.Sprintf("%s:%d", site.MariaDB.Host, site.MariaDB.Port)),
			zap.String("meilisearch", site.Meilisearch.Host),
			zap.String("redis", site.Redis.Addr()),
			zap.Int("api_port", site.API.Port),
		)
	}

	log.Info("⚡ Lightning is running — waiting for binlog events and HTTP requests")

	// --- Graceful shutdown on SIGINT / SIGTERM ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("⚡ Lightning shutting down gracefully")
}
