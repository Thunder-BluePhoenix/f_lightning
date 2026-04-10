package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"frappe_lightning/cmd/frapctl/bench"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ── Global flags ──────────────────────────────────────────────────────────────

var (
	flagBench string
	flagSite  string
)

// userDefaults holds values loaded from ~/.frapctl.yaml.
type userDefaults struct {
	Bench       string `yaml:"bench"`
	DefaultSite string `yaml:"default_site"`
	Color       bool   `yaml:"color"`
}

var defaults userDefaults

// loadUserDefaults reads ~/.frapctl.yaml if it exists. Silently ignored if absent.
func loadUserDefaults() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(home, ".frapctl.yaml"))
	if err != nil {
		return // file not present — that's fine
	}
	yaml.Unmarshal(data, &defaults) //nolint:errcheck
}

// resolveBench returns --bench flag, then ~/.frapctl.yaml bench, then auto-discovery.
func resolveBench() (string, error) {
	if flagBench != "" {
		return flagBench, nil
	}
	if defaults.Bench != "" {
		return defaults.Bench, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return bench.FindRoot(cwd)
}

// resolveSite returns --site flag then ~/.frapctl.yaml default_site.
func resolveSite() string {
	if flagSite != "" {
		return flagSite
	}
	return defaults.DefaultSite
}

// ── Root ──────────────────────────────────────────────────────────────────────

var rootCmd = &cobra.Command{
	Use:   "frapctl",
	Short: "⚡ frapctl — fast Frappe management CLI",
	Long: `frapctl is a compiled Go CLI for managing Frappe bench, sites, apps,
cache, and services without Python startup overhead.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		loadUserDefaults()
	},
}

// ── completion ────────────────────────────────────────────────────────────────

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish]",
	Short: "Generate shell completion script",
	Long: `Generate a shell completion script and source it in your shell profile.

  # bash
  source <(frapctl completion bash)

  # zsh
  source <(frapctl completion zsh)

  # fish
  frapctl completion fish | source`,
	ValidArgs: []string{"bash", "zsh", "fish"},
	Args:      cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletion(os.Stdout)
		case "zsh":
			return rootCmd.GenZshCompletion(os.Stdout)
		case "fish":
			return rootCmd.GenFishCompletion(os.Stdout, true)
		default:
			return fmt.Errorf("unsupported shell %q — use bash, zsh, or fish", args[0])
		}
	},
}

// ── site ──────────────────────────────────────────────────────────────────────

var siteCmd = &cobra.Command{Use: "site", Short: "Manage Frappe sites"}

var siteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all sites in the bench",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		sites, err := bench.ListSites(benchRoot)
		if err != nil {
			return err
		}
		for _, s := range sites {
			apps, _ := bench.InstalledApps(benchRoot, s)
			fmt.Printf("  %-30s  %s\n", s, strings.Join(apps, ", "))
		}
		return nil
	},
}

var siteCreateCmd = &cobra.Command{
	Use:   "create [site-name]",
	Short: "Create a new site",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		dbPass, _ := cmd.Flags().GetString("db-password")
		adminPass, _ := cmd.Flags().GetString("admin-password")
		return bench.RunBench(benchRoot,
			"new-site", args[0],
			"--db-root-password", dbPass,
			"--admin-password", adminPass,
		)
	},
}

var siteDropCmd = &cobra.Command{
	Use:   "drop [site-name]",
	Short: "Drop a site and its database",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		force, _ := cmd.Flags().GetBool("force")
		bArgs := []string{"drop-site", args[0]}
		if force {
			bArgs = append(bArgs, "--force")
		}
		return bench.RunBench(benchRoot, bArgs...)
	},
}

var siteBackupCmd = &cobra.Command{
	Use:   "backup [site-name]",
	Short: "Backup a site database",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		return bench.RunBench(benchRoot, "--site", args[0], "backup")
	},
}

var siteRestoreCmd = &cobra.Command{
	Use:   "restore [site-name]",
	Short: "Restore a site from a backup file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		backupFile, _ := cmd.Flags().GetString("backup")
		return bench.RunBench(benchRoot, "--site", args[0], "restore", backupFile)
	},
}

// ── app ───────────────────────────────────────────────────────────────────────

var appCmd = &cobra.Command{Use: "app", Short: "Manage Frappe apps"}

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed apps for a site",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		site := flagSite
		if site == "" {
			return fmt.Errorf("--site is required")
		}
		apps, err := bench.InstalledApps(benchRoot, site)
		if err != nil {
			return err
		}
		for _, a := range apps {
			ver := bench.AppVersion(benchRoot, a)
			fmt.Printf("  %-20s  %s\n", a, ver)
		}
		return nil
	},
}

var appGetCmd = &cobra.Command{
	Use:   "get [app-name]",
	Short: "Clone an app into the bench",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		branch, _ := cmd.Flags().GetString("branch")
		bArgs := []string{"get-app", args[0]}
		if branch != "" {
			bArgs = append(bArgs, "--branch", branch)
		}
		return bench.RunBench(benchRoot, bArgs...)
	},
}

var appInstallCmd = &cobra.Command{
	Use:   "install [app-name]",
	Short: "Install an app on a site",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		site := flagSite
		if site == "" {
			return fmt.Errorf("--site is required")
		}
		return bench.RunBench(benchRoot, "--site", site, "install-app", args[0])
	},
}

var appUninstallCmd = &cobra.Command{
	Use:   "uninstall [app-name]",
	Short: "Uninstall an app from a site",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		site := flagSite
		if site == "" {
			return fmt.Errorf("--site is required")
		}
		return bench.RunBench(benchRoot, "--site", site, "uninstall-app", args[0])
	},
}

var appUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update apps in the bench",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		return bench.RunBench(benchRoot, "update", "--no-migrate")
	},
}

// ── migrate ───────────────────────────────────────────────────────────────────

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run database migrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		all, _ := cmd.Flags().GetBool("all")
		if all {
			sites, err := bench.ListSites(benchRoot)
			if err != nil {
				return err
			}
			for _, s := range sites {
				fmt.Printf("  → migrating %s\n", s)
				if err := bench.RunBench(benchRoot, "--site", s, "migrate"); err != nil {
					return fmt.Errorf("migration failed for %s: %w", s, err)
				}
			}
			return nil
		}
		site := flagSite
		if site == "" {
			return fmt.Errorf("--site or --all is required")
		}
		return bench.RunBench(benchRoot, "--site", site, "migrate")
	},
}

// ── cache ─────────────────────────────────────────────────────────────────────

var cacheCmd = &cobra.Command{Use: "cache", Short: "Redis cache operations"}

var cacheClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear Redis cache for a site or all sites",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		all, _ := cmd.Flags().GetBool("all")

		commonCfg, err := bench.LoadCommonConfig(benchRoot)
		if err != nil {
			return err
		}
		rdb := redisFromURL(commonCfg.RedisCache)
		ctx := context.Background()

		if all {
			n, err := rdb.FlushDB(ctx).Result()
			if err != nil {
				return err
			}
			fmt.Printf("  cache flushed: %s ✓\n", n)
			return nil
		}

		site := flagSite
		if site == "" {
			return fmt.Errorf("--site or --all is required")
		}
		// Frappe cache keys are prefixed with the site name.
		keys, err := rdb.Keys(ctx, site+":*").Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			rdb.Del(ctx, keys...) //nolint:errcheck
		}
		fmt.Printf("  cleared %d cache keys for %s ✓\n", len(keys), site)
		return nil
	},
}

var cacheStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show Redis cache statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		commonCfg, err := bench.LoadCommonConfig(benchRoot)
		if err != nil {
			return err
		}
		rdb := redisFromURL(commonCfg.RedisCache)
		ctx := context.Background()

		info, err := rdb.Info(ctx, "stats", "memory", "keyspace").Result()
		if err != nil {
			return err
		}

		dbSize, _ := rdb.DBSize(ctx).Result()
		memUsed := extractInfoField(info, "used_memory_human")
		hits := extractInfoField(info, "keyspace_hits")
		misses := extractInfoField(info, "keyspace_misses")

		fmt.Printf("  Redis addr   : %s\n", commonCfg.RedisCache)
		fmt.Printf("  Keys         : %d\n", dbSize)
		fmt.Printf("  Memory used  : %s\n", memUsed)
		fmt.Printf("  Cache hits   : %s\n", hits)
		fmt.Printf("  Cache misses : %s\n", misses)
		return nil
	},
}

// ── service ───────────────────────────────────────────────────────────────────

var serviceCmd = &cobra.Command{Use: "service", Short: "Manage bench processes"}

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of all bench services",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		return runServiceCtl(benchRoot, "status")
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start [service]",
	Short: "Start one or all services",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		all, _ := cmd.Flags().GetBool("all")
		if all || len(args) == 0 {
			return bench.RunBench(benchRoot, "start")
		}
		return runServiceCtl(benchRoot, "start", args[0])
	},
}

var serviceStopCmd = &cobra.Command{
	Use:   "stop [service]",
	Short: "Stop one or all services",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		all, _ := cmd.Flags().GetBool("all")
		if all || len(args) == 0 {
			return bench.RunBench(benchRoot, "stop")
		}
		return runServiceCtl(benchRoot, "stop", args[0])
	},
}

var serviceRestartCmd = &cobra.Command{
	Use:   "restart [service]",
	Short: "Restart one or all services",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		all, _ := cmd.Flags().GetBool("all")
		if all || len(args) == 0 {
			return bench.RunBench(benchRoot, "restart")
		}
		return runServiceCtl(benchRoot, "restart", args[0])
	},
}

// runServiceCtl tries supervisorctl first, then systemctl.
func runServiceCtl(benchRoot string, action string, service ...string) error {
	// Try supervisorctl
	supervisorConf := filepath.Join(benchRoot, "config", "supervisor.conf")
	if _, err := os.Stat(supervisorConf); err == nil {
		args := []string{"-c", supervisorConf, action}
		args = append(args, service...)
		c := exec.Command("supervisorctl", args...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}
	// Fall back to bench start/stop/restart
	return bench.RunBench(benchRoot, action)
}

// ── config ────────────────────────────────────────────────────────────────────

var configCmd = &cobra.Command{Use: "config", Short: "Read and write site configuration"}

var configGetCmd = &cobra.Command{
	Use:   "get [key]",
	Short: "Get a value from site_config.json",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		site := flagSite
		if site == "" {
			return fmt.Errorf("--site is required")
		}
		val, err := bench.GetSiteConfigKey(benchRoot, site, args[0])
		if err != nil {
			return err
		}
		fmt.Println(val)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set [key] [value]",
	Short: "Set a value in site_config.json",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		site := flagSite
		if site == "" {
			return fmt.Errorf("--site is required")
		}
		if err := bench.SetSiteConfigKey(benchRoot, site, args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("  %s = %s ✓\n", args[0], args[1])
		return nil
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print site_config.json",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		site := flagSite
		if site == "" {
			return fmt.Errorf("--site is required")
		}
		path := filepath.Join(benchRoot, "sites", site, "site_config.json")
		return prettyPrintJSON(path)
	},
}

var configShowCommonCmd = &cobra.Command{
	Use:   "show-common",
	Short: "Print common_site_config.json",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		path := filepath.Join(benchRoot, "sites", "common_site_config.json")
		return prettyPrintJSON(path)
	},
}

// ── shell / console ───────────────────────────────────────────────────────────

var shellCmd = &cobra.Command{
	Use:   "shell [python-expression]",
	Short: "Run a Python expression via bench execute",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		site := flagSite
		if site == "" {
			return fmt.Errorf("--site is required")
		}
		return bench.RunBench(benchRoot, "--site", site, "execute", args[0])
	},
}

var consoleCmd = &cobra.Command{
	Use:   "console",
	Short: "Open an interactive Frappe Python console",
	RunE: func(cmd *cobra.Command, args []string) error {
		benchRoot, err := resolveBench()
		if err != nil {
			return err
		}
		site := flagSite
		if site == "" {
			return fmt.Errorf("--site is required")
		}
		return bench.RunBench(benchRoot, "--site", site, "console")
	},
}

// ── helpers ───────────────────────────────────────────────────────────────────

func redisFromURL(url string) *redis.Client {
	// url may be "redis://127.0.0.1:13005" or bare "127.0.0.1:13005"
	addr := strings.TrimPrefix(url, "redis://")
	return redis.NewClient(&redis.Options{Addr: addr})
}

func extractInfoField(info, key string) string {
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		}
	}
	return "n/a"
}

func prettyPrintJSON(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out, _ := json.MarshalIndent(raw, "", "  ")
	fmt.Println(string(out))
	return nil
}

// ── init ──────────────────────────────────────────────────────────────────────

func init() {
	rootCmd.PersistentFlags().StringVar(&flagBench, "bench", "", "Path to frappe-bench root (auto-discovered if omitted)")
	rootCmd.PersistentFlags().StringVar(&flagSite, "site", "", "Frappe site name")

	// site
	siteCreateCmd.Flags().String("db-password", "", "MariaDB root password")
	siteCreateCmd.Flags().String("admin-password", "admin", "Frappe admin password")
	siteDropCmd.Flags().Bool("force", false, "Drop without confirmation")
	siteRestoreCmd.Flags().String("backup", "", "Path to backup file")
	siteCmd.AddCommand(siteListCmd, siteCreateCmd, siteDropCmd, siteBackupCmd, siteRestoreCmd)

	// app
	appGetCmd.Flags().String("branch", "", "Git branch to clone")
	appUpdateCmd.Flags().Bool("all", true, "Update all apps")
	appCmd.AddCommand(appListCmd, appGetCmd, appInstallCmd, appUninstallCmd, appUpdateCmd)

	// migrate
	migrateCmd.Flags().Bool("all", false, "Run migrate on every site")

	// cache
	cacheClearCmd.Flags().Bool("all", false, "Clear cache for all sites (flushdb)")
	cacheCmd.AddCommand(cacheClearCmd, cacheStatsCmd)

	// service
	serviceStartCmd.Flags().Bool("all", false, "Start all services")
	serviceStopCmd.Flags().Bool("all", false, "Stop all services")
	serviceRestartCmd.Flags().Bool("all", false, "Restart all services")
	serviceCmd.AddCommand(serviceStatusCmd, serviceStartCmd, serviceStopCmd, serviceRestartCmd)

	// config
	configCmd.AddCommand(configGetCmd, configSetCmd, configShowCmd, configShowCommonCmd)

	// root
	rootCmd.AddCommand(siteCmd, appCmd, migrateCmd, cacheCmd, serviceCmd, configCmd, shellCmd, consoleCmd, completionCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
