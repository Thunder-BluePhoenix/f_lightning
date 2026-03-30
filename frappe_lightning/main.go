package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"frappe_lightning/api"
	"frappe_lightning/api/middleware"
	"frappe_lightning/ai"
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

	rankingCfg, err := config.LoadRanking("config/ranking.yaml")
	if err != nil {
		log.Warn("failed to load ranking config - using defaults", zap.Error(err))
	}

	schemas := config.DefaultSchemas()

	// --- AI Embedder (shared across sites) ---
	var embedder *ai.Embedder
	if cfg.AIMode == "local" {
		embedder = ai.NewEmbedder(cfg.EmbeddingServerURL)
		log.Info("AI Hybrid mode enabled", zap.String("server", cfg.EmbeddingServerURL))
	}

	tenants := make(map[string]*middleware.Tenant)
	var primaryAPIPort int = 8765 // default fallback

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
			if err := search.InitIndex(meiliClient, &schemas[j], rankingCfg, site.Name, cfg.AIMode, cfg.VectorDimensions); err != nil {
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

		// Redis client (for Frappe session validation and click signals)
		rdb := redis.NewClient(&redis.Options{
			Addr: site.Redis.Addr(),
		})

		// Sync engine (consumes events, writes to Meilisearch, requires redis for ClickTracker)
		engine := search.NewEngine(site, schemas, meiliClient, rdb, cfg.AIMode, embedder, log)
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

		// Log execution
		log.Info("site pipeline started",
			zap.String("site", site.Name),
			zap.String("mariadb", fmt.Sprintf("%s:%d", site.MariaDB.Host, site.MariaDB.Port)),
			zap.String("meilisearch", site.Meilisearch.Host),
			zap.String("redis", site.Redis.Addr()),
		)

		tenants[site.Name] = &middleware.Tenant{
			Config:   site,
			Meili:    meiliClient,
			Redis:    rdb,
			AIMode:   cfg.AIMode,
			Embedder: embedder,
			APIToken: cfg.APIToken,
		}

		if site.API.Port > 0 {
			primaryAPIPort = site.API.Port
		}
	}

	// 5. Spin up global Multi-Tenant HTTP Server
	server := api.NewServer(tenants, log)
	go func() {
		if err := server.Start(primaryAPIPort); err != nil {
			log.Error("API server stopped", zap.Error(err))
		}
	}()

	log.Info("⚡ Lightning is running — multi-tenant mode active")

	// --- Graceful shutdown on SIGINT / SIGTERM ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("⚡ Lightning shutting down gracefully...")

	// 1. Stop API Server
	if err := server.Stop(); err != nil {
		log.Error("failed to stop API server", zap.Error(err))
	}

	log.Info("⚡ Lightning shut down")
}
