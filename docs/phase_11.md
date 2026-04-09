# Phase 11: Frappe CLI in Go

**Goal:** Build a fast, cross-platform CLI for managing Frappe sites — like `bench` but compiled to a single binary with no Python startup overhead. Common operations like migrate, clear-cache, restart, and backup run in milliseconds rather than seconds. Ships as a companion to the existing `lightning` CLI.

---

## Why This Matters

`bench` is a Python CLI. Every invocation pays a ~800ms Python interpreter startup cost before any actual work begins. On a typical deploy script running 10 bench commands, that's 8 seconds of pure waiting before work starts.

A Go binary starts in <5ms. The same 10 commands take milliseconds of overhead. For CI/CD pipelines, cron jobs, and developer inner-loop tasks, this is a meaningful difference.

Beyond speed, a single compiled binary works on any OS without a Python environment — useful for DevOps teams managing Frappe from non-Python toolchains.

---

## Architecture

```
frapctl (Go binary)
   │
   ├── site/          Site management (create, drop, backup, restore)
   ├── app/           App management (get-app, install, uninstall, list)
   ├── migrate/       Schema migration runner
   ├── cache/         Redis cache operations
   ├── service/       Process management (start, stop, restart, status)
   ├── config/        Read/write site_config.json and common_site_config.json
   └── shell/         Drop into bench execute for arbitrary Python calls
```

All subcommands accept `--bench` (path to frappe-bench root) and `--site` flags. Defaults are read from the current directory if it looks like a bench.

---

## Commands

### Site Management

```bash
frapctl site list --bench ~/frappe-bench
# erp.local          [active]   frappe, erpnext, f_lightning
# staging.local      [active]   frappe, erpnext

frapctl site create erp.local \
  --bench ~/frappe-bench \
  --db-name erp_local \
  --db-password secret \
  --admin-password admin

frapctl site drop erp.local --bench ~/frappe-bench --force

frapctl site backup erp.local \
  --bench ~/frappe-bench \
  --backup-path /mnt/backups

frapctl site restore erp.local \
  --bench ~/frappe-bench \
  --backup /mnt/backups/erp.local-2026-04-09.sql.gz
```

### App Management

```bash
frapctl app list --bench ~/frappe-bench --site erp.local
# frappe             v15.28.0
# erpnext            v15.18.0
# f_lightning        develop

frapctl app install f_lightning --site erp.local --bench ~/frappe-bench
frapctl app uninstall f_lightning --site erp.local --bench ~/frappe-bench
frapctl app update --all --bench ~/frappe-bench
frapctl app get erpnext --bench ~/frappe-bench --branch version-15
```

### Migration

```bash
frapctl migrate --site erp.local --bench ~/frappe-bench
# Runs bench migrate, captures structured output, exits non-zero on failure

frapctl migrate --site erp.local --bench ~/frappe-bench --skip-failing
frapctl migrate --all --bench ~/frappe-bench   # all sites sequentially
```

### Cache

```bash
frapctl cache clear --site erp.local --bench ~/frappe-bench
frapctl cache clear --all --bench ~/frappe-bench

frapctl cache stats --site erp.local
# Redis cache:  127.0.0.1:13005
# Keys:         14,283
# Memory used:  42 MB
# Hit rate:     87.4%
```

### Service Control

```bash
frapctl service status --bench ~/frappe-bench
# web        [running]  pid=12345  uptime=3d 14h
# worker     [running]  pid=12346  uptime=3d 14h
# scheduler  [running]  pid=12347  uptime=3d 14h
# redis      [running]  pid=12348  uptime=3d 14h

frapctl service restart web --bench ~/frappe-bench
frapctl service restart --all --bench ~/frappe-bench
frapctl service stop worker --bench ~/frappe-bench
frapctl service start --all --bench ~/frappe-bench
```

Service management reads from `Procfile` and talks to `supervisorctl` or `systemctl` depending on the bench setup.

### Config Management

```bash
# Read a config value
frapctl config get db_password --site erp.local --bench ~/frappe-bench
# Lql0Sc0JdB7OGsZl

# Set a value
frapctl config set maintenance_mode 1 --site erp.local --bench ~/frappe-bench

# Show full site config
frapctl config show --site erp.local --bench ~/frappe-bench

# Show common config
frapctl config show-common --bench ~/frappe-bench
```

### Shell / Execute

```bash
# Run a Python snippet via bench execute
frapctl shell --site erp.local --bench ~/frappe-bench \
  "frappe.get_doc('DocType', 'Customer').fields"

# Interactive Python REPL in the Frappe context
frapctl console --site erp.local --bench ~/frappe-bench
```

---

## Implementation

### Bench Auto-Discovery

When `--bench` is not provided, `frapctl` walks up the directory tree looking for a `sites/common_site_config.json` to identify the bench root — similar to how `git` finds `.git/`.

```go
func findBenchRoot(startDir string) (string, error) {
    dir := startDir
    for {
        if _, err := os.Stat(filepath.Join(dir, "sites", "common_site_config.json")); err == nil {
            return dir, nil
        }
        parent := filepath.Dir(dir)
        if parent == dir {
            return "", fmt.Errorf("not inside a frappe-bench directory")
        }
        dir = parent
    }
}
```

### Config Reader / Writer

```go
type SiteConfig struct {
    DBName     string `json:"db_name"`
    DBPassword string `json:"db_password"`
    DBUser     string `json:"db_user"`
    DBHost     string `json:"db_host,omitempty"`
    DBPort     int    `json:"db_port,omitempty"`
}

type CommonConfig struct {
    RedisCache    string `json:"redis_cache"`
    RedisQueue    string `json:"redis_queue"`
    WebserverPort int    `json:"webserver_port"`
    DefaultSite   string `json:"default_site"`
}

func LoadSiteConfig(benchPath, site string) (*SiteConfig, error) {
    path := filepath.Join(benchPath, "sites", site, "site_config.json")
    data, err := os.ReadFile(path)
    // ...
}
```

### Subprocess Delegation

Operations that are inherently Python (migrate, get-app, install-app) are delegated to the real `bench` binary. `frapctl` provides structured output parsing, coloured output, and non-zero exit codes on failure:

```go
func runBench(benchPath string, args ...string) error {
    cmd := exec.Command(filepath.Join(benchPath, "env", "bin", "bench"), args...)
    cmd.Dir = benchPath
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    return cmd.Run()
}
```

For purely Go-implementable operations (cache clear, config get/set, service status), no subprocess is used.

---

## Config

```yaml
# Optional ~/.frapctl.yaml for user-level defaults
bench: /home/frappe/frappe-bench
default_site: erp.local
color: true
```

---

## Task Checklist

- [ ] Set up `cmd/frapctl/` directory with cobra root command
- [ ] Implement bench auto-discovery (`findBenchRoot`)
- [ ] Implement `config/reader.go` — load `site_config.json` and `common_site_config.json`
- [ ] Implement `config/writer.go` — atomic JSON write with backup
- [ ] Implement `frapctl site list/create/drop/backup/restore`
- [ ] Implement `frapctl app list/install/uninstall/update/get`
- [ ] Implement `frapctl migrate` — delegates to bench with structured output
- [ ] Implement `frapctl cache clear/stats` — direct Redis operations
- [ ] Implement `frapctl service status/start/stop/restart` — supervisorctl/systemctl adapter
- [ ] Implement `frapctl config get/set/show/show-common`
- [ ] Implement `frapctl shell` and `frapctl console`
- [ ] Implement `~/.frapctl.yaml` user defaults
- [ ] Write unit tests for bench auto-discovery and config read/write
- [ ] Build cross-platform binaries for Linux (amd64, arm64) and macOS
- [ ] Add shell completion (bash, zsh, fish) via cobra

---

## Validation Checklist

- [ ] `frapctl site list` discovers all sites without `--bench` flag when run from inside the bench
- [ ] `frapctl cache clear --all` clears Redis cache for every site within 100ms
- [ ] `frapctl service status` correctly reads process state from supervisorctl
- [ ] `frapctl config set` writes atomically — no partial JSON on crash
- [ ] `frapctl migrate` exits non-zero and prints clear error when migration fails
- [ ] Binary runs on macOS and Linux without any Python or Go runtime present
- [ ] Shell completion works in bash and zsh
