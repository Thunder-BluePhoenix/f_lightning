package main

import (
	"fmt"
	"os"
	"strings"

	"frappe_lightning/canal"
	"frappe_lightning/config"

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
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
