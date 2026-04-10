package config

import (
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration loaded from config.yaml.
type Config struct {
	Sites              []SiteConfig   `yaml:"sites"`
	AIMode             string         `yaml:"ai_mode"`              // off | local | cloud
	EmbeddingServerURL string         `yaml:"embedding_server_url"` // e.g. http://localhost:5000/embed
	VectorDimensions   int            `yaml:"vector_dimensions"`    // default 384 for MiniLM
	APIToken           string         `yaml:"api_token"`            // Global token for universal access
	Gateway            GatewayConfig  `yaml:"gateway"`              // optional reverse proxy config
	JobRunner          JobRunnerConfig `yaml:"job_runner"`           // optional background job runner
}

// JobRunnerConfig configures the Go-based background job runner.
type JobRunnerConfig struct {
	Enabled   bool              `yaml:"enabled"`
	BenchPath string            `yaml:"bench_path"` // path to the bench root, e.g. /home/user/frappe-bench
	Queues    []QueueConfig     `yaml:"queues"`
	MaxRetries int              `yaml:"max_retries"` // default 3
}

// QueueConfig holds per-queue concurrency settings.
type QueueConfig struct {
	Name        string `yaml:"name"`        // e.g. "high", "default", "low"
	Concurrency int    `yaml:"concurrency"` // max goroutines processing this queue
}

// GatewayConfig configures the optional API Gateway server.
type GatewayConfig struct {
	Enabled        bool             `yaml:"enabled"`
	ListenPort     int              `yaml:"listen_port"`      // default 7000
	Sites          []GatewaySite    `yaml:"sites"`
	Auth           GatewayAuth      `yaml:"auth"`
	RateLimits     []RateLimitRule  `yaml:"rate_limits"`
	Cache          GatewayCache     `yaml:"cache"`
	CircuitBreaker CircuitBreakerCfg `yaml:"circuit_breaker"`
}

// GatewaySite maps a Frappe site name to its upstream Gunicorn worker URLs.
type GatewaySite struct {
	Name            string   `yaml:"name"`
	UpstreamWorkers []string `yaml:"upstream_workers"` // e.g. ["http://127.0.0.1:8000"]
}

// GatewayAuth lists URL path prefixes that bypass session validation.
type GatewayAuth struct {
	SkipPaths []string `yaml:"skip_paths"`
}

// RateLimitRule caps requests per user per second on a matching path prefix.
type RateLimitRule struct {
	PathPrefix  string `yaml:"path_prefix"`
	PerUserRPS  int    `yaml:"per_user_rps"`
}

// GatewayCache configures response caching.
type GatewayCache struct {
	Enabled bool        `yaml:"enabled"`
	Rules   []CacheRule `yaml:"rules"`
}

// CacheRule caches GET responses for paths matching a prefix, for a given TTL.
type CacheRule struct {
	PathPrefix string `yaml:"path_prefix"`
	TTLSeconds int    `yaml:"ttl_seconds"`
}

// CircuitBreakerCfg controls the upstream circuit breaker.
type CircuitBreakerCfg struct {
	FailureThreshold      int `yaml:"failure_threshold"`        // open after N consecutive failures, default 5
	OpenDurationSec       int `yaml:"open_duration_sec"`        // seconds before attempting half-open, default 30
	HalfOpenProbeInterval int `yaml:"half_open_probe_interval"` // seconds between probes, default 10
}

// SiteConfig holds per-site configuration.
type SiteConfig struct {
	Name        string            `yaml:"name"`
	MariaDB     MariaDBConfig     `yaml:"mariadb"`
	Meilisearch MeilisearchConfig `yaml:"meilisearch"`
	Redis       RedisConfig       `yaml:"redis"`
	DocTypes    []DocTypeEntry    `yaml:"doctypes"`
	API         APIConfig         `yaml:"api"`
}

// MariaDBConfig holds the binlog listener connection details.
type MariaDBConfig struct {
	Host     string `yaml:"host"`
	Port     uint16 `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	ServerID uint32 `yaml:"server_id"`
}

// MeilisearchConfig holds Meilisearch connection details.
type MeilisearchConfig struct {
	Host      string `yaml:"host"`
	MasterKey string `yaml:"master_key"`
}

// RedisConfig holds Redis connection details (used for Frappe session validation).
type RedisConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// Addr returns the Redis address string.
func (r RedisConfig) Addr() string {
	return r.Host + ":" + strconv.Itoa(r.Port)
}

// DocTypeEntry is a simple name → table mapping for the canal filter.
type DocTypeEntry struct {
	Name  string `yaml:"name"`
	Table string `yaml:"table"`
}

// APIConfig holds HTTP server settings.
type APIConfig struct {
	Port           int    `yaml:"port"`            // default 8765
	AllowedOrigins string `yaml:"allowed_origins"` // CORS whitelist
}

// Load reads and parses the YAML config file at the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	// Apply defaults
	for i := range cfg.Sites {
		if cfg.Sites[i].API.Port == 0 {
			cfg.Sites[i].API.Port = 8765
		}
		if cfg.Sites[i].MariaDB.Port == 0 {
			cfg.Sites[i].MariaDB.Port = 3306
		}
	}
	if cfg.AIMode == "" {
		cfg.AIMode = "off"
	}
	if cfg.VectorDimensions == 0 {
		cfg.VectorDimensions = 384
	}
	if cfg.EmbeddingServerURL == "" {
		cfg.EmbeddingServerURL = "http://localhost:5000/embed"
	}
	if cfg.APIToken == "" {
		cfg.APIToken = "lightning-secret-dev"
	}
	// JobRunner defaults
	if cfg.JobRunner.MaxRetries == 0 {
		cfg.JobRunner.MaxRetries = 3
	}
	if len(cfg.JobRunner.Queues) == 0 {
		cfg.JobRunner.Queues = []QueueConfig{
			{Name: "high", Concurrency: 10},
			{Name: "default", Concurrency: 20},
			{Name: "low", Concurrency: 5},
			{Name: "long", Concurrency: 3},
		}
	}
	// Gateway defaults
	if cfg.Gateway.ListenPort == 0 {
		cfg.Gateway.ListenPort = 7000
	}
	if cfg.Gateway.CircuitBreaker.FailureThreshold == 0 {
		cfg.Gateway.CircuitBreaker.FailureThreshold = 5
	}
	if cfg.Gateway.CircuitBreaker.OpenDurationSec == 0 {
		cfg.Gateway.CircuitBreaker.OpenDurationSec = 30
	}
	if cfg.Gateway.CircuitBreaker.HalfOpenProbeInterval == 0 {
		cfg.Gateway.CircuitBreaker.HalfOpenProbeInterval = 10
	}
	return &cfg, nil
}

// GetSite returns the SiteConfig for a given site name, or false if not found.
func (c *Config) GetSite(name string) (*SiteConfig, bool) {
	for i := range c.Sites {
		if c.Sites[i].Name == name {
			return &c.Sites[i], true
		}
	}
	return nil, false
}


