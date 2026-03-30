package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"frappe_lightning/canal"
	"frappe_lightning/config"
	"frappe_lightning/nlp"

	_ "github.com/go-sql-driver/mysql"
	"github.com/meilisearch/meilisearch-go"
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

func init() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "config.yaml", "Path to config file")
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(diffCmd)
	rootCmd.AddCommand(parseCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
