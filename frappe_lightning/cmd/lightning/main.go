package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"frappe_lightning/canal"
	"frappe_lightning/config"
	"frappe_lightning/gateway"
	"frappe_lightning/jobs"
	"frappe_lightning/nlp"

	_ "github.com/go-sql-driver/mysql"
	"github.com/meilisearch/meilisearch-go"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	configPath string
	log        *zap.Logger
)

var rootCmd = &cobra.Command{
	Use:   "lightning",
	Short: "Lightning is a high-performance search proxy for Frappe",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		var err error
		log, err = zap.NewDevelopment()
		if err != nil {
			fmt.Printf("failed to init log: %v\n", err)
			os.Exit(1)
		}
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check the status of all configured site pipelines",
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal("failed to load config", zap.Error(err))
		}

		fmt.Println("⚡ Lightning Pipeline Status")
		fmt.Println(strings.Repeat("-", 40))
		for _, site := range cfg.Sites {
			fmt.Printf("Site: %-20s [ACTIVE]\n", site.Name)
			fmt.Printf("  MariaDB: %s:%d\n", site.MariaDB.Host, site.MariaDB.Port)
			fmt.Printf("  Meili:   %s\n", site.Meilisearch.Host)
			fmt.Printf("  Redis:   %s\n", site.Redis.Addr())
			fmt.Println()
		}
	},
}

var diffCmd = &cobra.Command{
	Use:   "diff [site] [doctype]",
	Short: "Compare MariaDB vs Meilisearch record counts",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		siteName, dt := args[0], args[1]
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal("failed to load config", zap.Error(err))
		}

		site, ok := cfg.GetSite(siteName)
		if !ok {
			log.Fatal("site not found", zap.String("site", siteName))
		}

		schemas := config.DefaultSchemas()
		schema, ok := config.SchemaByTable(schemas, "tab"+strings.Title(dt))
		if !ok {
			// Try without prefix
			schema, ok = config.SchemaByTable(schemas, dt)
			if !ok {
				log.Fatal("doctype not tracked", zap.String("dt", dt))
			}
		}

		// 1. Get MariaDB Count
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", site.MariaDB.User, site.MariaDB.Password, site.MariaDB.Host, site.MariaDB.Port, site.Name)
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			log.Fatal("db connect failed", zap.Error(err))
		}
		defer db.Close()

		var mysqlCount int
		err = db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", schema.Table)).Scan(&mysqlCount)
		if err != nil {
			log.Fatal("mysql query failed", zap.Error(err))
		}

		// 2. Get Meilisearch Count
		meili := meilisearch.New(site.Meilisearch.Host, meilisearch.WithAPIKey(site.Meilisearch.MasterKey))
		stats, err := meili.Index(schema.IndexName(siteName)).GetStats()
		if err != nil {
			log.Fatal("meili query failed", zap.Error(err))
		}
		meiliCount := int(stats.NumberOfDocuments)

		fmt.Printf("📊 Sync Diff for %s [%s]\n", siteName, dt)
		fmt.Println(strings.Repeat("-", 40))
		fmt.Printf("  MariaDB Rows:     %d\n", mysqlCount)
		fmt.Printf("  Meilisearch Docs: %d\n", meiliCount)
		diff := mysqlCount - meiliCount
		if diff == 0 {
			fmt.Println("  ✅ Perfect Match!")
		} else {
			fmt.Printf("  ⚠️  Diff: %d (In MariaDB but not in Meili)\n", diff)
		}
	},
}

var parseCmd = &cobra.Command{
	Use:   "parse [query]",
	Short: "Interactively test NLP parsing logic",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		q := args[0]
		parsed := nlp.BuildQuery(q)
		meili := nlp.ToMeili(parsed)

		fmt.Printf("🔮 NLP Parse: \"%s\"\n", q)
		fmt.Println(strings.Repeat("-", 40))
		
		out, _ := json.MarshalIndent(parsed, "", "  ")
		fmt.Printf("Structured Query:\n%s\n\n", string(out))

		mOut, _ := json.MarshalIndent(meili, "", "  ")
		fmt.Printf("Meilisearch DSL Params:\n%s\n", string(mOut))
	},
}

var watchCmd = &cobra.Command{
	Use:   "watch [site]",
	Short: "Live stream binlog events for a specific site (debug)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		siteName := args[0]
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal("failed to load config", zap.Error(err))
		}

		site, ok := cfg.GetSite(siteName)
		if !ok {
			log.Fatal("site not found in config", zap.String("site", siteName))
		}

		fmt.Printf("👀 Watching events for %s... (Press Ctrl+C to stop)\n", siteName)
		eventsCh := make(chan *canal.RowEvent, 100)
		schemas := config.DefaultSchemas()

		go func() {
			for event := range eventsCh {
				fmt.Printf("[%s] %-10s %-20s | %v\n", 
					event.Action, 
					event.Table, 
					"event", 
					event.Rows[0],
				)
			}
		}()

		if err := canal.Start(site, schemas, eventsCh, log); err != nil {
			log.Fatal("canal failure", zap.Error(err))
		}
	},
}

// ── Gateway command group ──────────────────────────────────────────────────────

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Manage the Lightning API Gateway",
}

var gatewayStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the API Gateway server",
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal("failed to load config", zap.Error(err))
		}
		if !cfg.Gateway.Enabled {
			log.Fatal("gateway is not enabled in config (set gateway.enabled: true)")
		}

		// Build one Redis client per configured gateway site.
		redisClients := make(map[string]*redis.Client)
		for _, gs := range cfg.Gateway.Sites {
			// Match gateway site to its search-site config for Redis credentials.
			if sc, ok := cfg.GetSite(gs.Name); ok {
				redisClients[gs.Name] = redis.NewClient(&redis.Options{
					Addr: sc.Redis.Addr(),
				})
			}
		}

		srv := gateway.NewServer(&cfg.Gateway, redisClients, log)
		log.Info("starting gateway", zap.Int("port", cfg.Gateway.ListenPort))
		if err := srv.Start(); err != nil {
			log.Fatal("gateway stopped", zap.Error(err))
		}
	},
}

var gatewayStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show gateway health and circuit breaker states",
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal("failed to load config", zap.Error(err))
		}

		fmt.Println("⚡ Gateway Status")
		fmt.Println(strings.Repeat("-", 40))
		if !cfg.Gateway.Enabled {
			fmt.Println("  Gateway: DISABLED (set gateway.enabled: true)")
			return
		}
		fmt.Printf("  Listen port: %d\n", cfg.Gateway.ListenPort)
		fmt.Printf("  Sites configured: %d\n\n", len(cfg.Gateway.Sites))
		for _, gs := range cfg.Gateway.Sites {
			fmt.Printf("  Site: %-25s  Workers: %d\n", gs.Name, len(gs.UpstreamWorkers))
			for _, w := range gs.UpstreamWorkers {
				fmt.Printf("    → %s\n", w)
			}
		}
	},
}

var gatewayCacheFlushCmd = &cobra.Command{
	Use:   "cache-flush [site]",
	Short: "Flush the response cache for one site or all sites",
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal("failed to load config", zap.Error(err))
		}
		if !cfg.Gateway.Cache.Enabled {
			fmt.Println("Cache is disabled in config.")
			return
		}

		ctx := context.Background()

		// Use the first matching Redis client.
		var rdb *redis.Client
		targetSite := ""
		if len(args) > 0 {
			targetSite = args[0]
		}

		for _, gs := range cfg.Gateway.Sites {
			if targetSite != "" && gs.Name != targetSite {
				continue
			}
			if sc, ok := cfg.GetSite(gs.Name); ok {
				rdb = redis.NewClient(&redis.Options{Addr: sc.Redis.Addr()})
				cache := gateway.NewResponseCache(rdb, cfg.Gateway.Cache.Rules)
				n, err := cache.FlushSite(ctx, gs.Name)
				if err != nil {
					fmt.Printf("  %-25s  ERROR: %v\n", gs.Name, err)
				} else {
					fmt.Printf("  %-25s  flushed %d keys ✓\n", gs.Name, n)
				}
			}
		}
	},
}

var gatewayCacheStatsCmd = &cobra.Command{
	Use:   "cache-stats",
	Short: "Show cache hit/miss statistics per site",
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal("failed to load config", zap.Error(err))
		}
		if !cfg.Gateway.Cache.Enabled {
			fmt.Println("Cache is disabled in config.")
			return
		}

		ctx := context.Background()
		fmt.Println("⚡ Gateway Cache Stats")
		fmt.Println(strings.Repeat("-", 50))
		fmt.Printf("  %-25s  %8s  %8s  %s\n", "Site", "Hits", "Misses", "Hit Rate")

		for _, gs := range cfg.Gateway.Sites {
			if sc, ok := cfg.GetSite(gs.Name); ok {
				rdb := redis.NewClient(&redis.Options{Addr: sc.Redis.Addr()})
				cache := gateway.NewResponseCache(rdb, cfg.Gateway.Cache.Rules)
				hits, misses := cache.Stats(ctx, gs.Name)
				var rate float64
				if total := hits + misses; total > 0 {
					rate = float64(hits) / float64(total) * 100
				}
				fmt.Printf("  %-25s  %8d  %8d  %.1f%%\n", gs.Name, hits, misses, rate)
			}
		}
	},
}

// ── Jobs command group ────────────────────────────────────────────────────────

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "Manage the Lightning background job runner",
}

var jobsStartCmd = &cobra.Command{
	Use:   "start [site]",
	Short: "Start the background job runner for a site",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		siteName := args[0]
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal("failed to load config", zap.Error(err))
		}
		if !cfg.JobRunner.Enabled {
			log.Fatal("job_runner is not enabled in config (set job_runner.enabled: true)")
		}
		site, ok := cfg.GetSite(siteName)
		if !ok {
			log.Fatal("site not found", zap.String("site", siteName))
		}
		rdb := redis.NewClient(&redis.Options{Addr: site.Redis.Addr()})
		runner := jobs.NewRunner(siteName, rdb, cfg.JobRunner, log)
		log.Info("starting job runner", zap.String("site", siteName))
		runner.Start(context.Background())
	},
}

var jobsStatusCmd = &cobra.Command{
	Use:   "status [site]",
	Short: "Show queue depths and job counts",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		siteName := args[0]
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal("failed to load config", zap.Error(err))
		}
		site, ok := cfg.GetSite(siteName)
		if !ok {
			log.Fatal("site not found", zap.String("site", siteName))
		}
		rdb := redis.NewClient(&redis.Options{Addr: site.Redis.Addr()})
		consumer := jobs.NewConsumer(rdb, siteName, log)
		ctx := context.Background()

		fmt.Printf("⚡ Job Queue Status — %s\n", siteName)
		fmt.Println(strings.Repeat("-", 45))
		fmt.Printf("  %-12s  %8s\n", "Queue", "Pending")
		fmt.Println(strings.Repeat("-", 45))

		queues := []string{"high", "default", "low", "long", "failed"}
		for _, q := range queues {
			depth := consumer.QueueDepth(ctx, q)
			fmt.Printf("  %-12s  %8d\n", q, depth)
		}
	},
}

var jobsRetryAllCmd = &cobra.Command{
	Use:   "retry-all [site]",
	Short: "Re-enqueue all jobs in the failed queue",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		siteName := args[0]
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal("failed to load config", zap.Error(err))
		}
		site, ok := cfg.GetSite(siteName)
		if !ok {
			log.Fatal("site not found", zap.String("site", siteName))
		}
		rdb := redis.NewClient(&redis.Options{Addr: site.Redis.Addr()})
		ctx := context.Background()
		consumer := jobs.NewConsumer(rdb, siteName, log)

		var moved int64
		for {
			result, err := rdb.RPopLPush(ctx, "rq:queue:failed", "rq:queue:default").Result()
			if err != nil || result == "" {
				break
			}
			// Reset status so the runner picks it up cleanly.
			consumer.SetStatus(ctx, result, "queued")
			moved++
		}
		fmt.Printf("  re-enqueued %d failed jobs → default queue ✓\n", moved)
	},
}

var jobsFlushCmd = &cobra.Command{
	Use:   "flush [site] [queue]",
	Short: "Clear all pending jobs from a queue",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		siteName, queue := args[0], args[1]
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal("failed to load config", zap.Error(err))
		}
		site, ok := cfg.GetSite(siteName)
		if !ok {
			log.Fatal("site not found", zap.String("site", siteName))
		}
		rdb := redis.NewClient(&redis.Options{Addr: site.Redis.Addr()})
		ctx := context.Background()
		n, err := rdb.Del(ctx, "rq:queue:"+queue).Result()
		if err != nil {
			log.Fatal("flush failed", zap.Error(err))
		}
		fmt.Printf("  flushed queue %q — removed %d entries ✓\n", queue, n)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "config.yaml", "Path to config file")
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(diffCmd)
	rootCmd.AddCommand(parseCmd)

	// Gateway subcommands
	gatewayCmd.AddCommand(gatewayStartCmd)
	gatewayCmd.AddCommand(gatewayStatusCmd)
	gatewayCmd.AddCommand(gatewayCacheFlushCmd)
	gatewayCmd.AddCommand(gatewayCacheStatsCmd)
	rootCmd.AddCommand(gatewayCmd)

	// Jobs subcommands
	jobsCmd.AddCommand(jobsStartCmd)
	jobsCmd.AddCommand(jobsStatusCmd)
	jobsCmd.AddCommand(jobsRetryAllCmd)
	jobsCmd.AddCommand(jobsFlushCmd)
	rootCmd.AddCommand(jobsCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
