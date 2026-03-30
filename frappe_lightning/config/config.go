package config

import (
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration loaded from config.yaml.
type Config struct {
	Sites               []SiteConfig `yaml:"sites"`
	AIMode              string       `yaml:"ai_mode"`              // off | local | cloud
	EmbeddingServerURL  string       `yaml:"embedding_server_url"` // e.g. http://localhost:5000/embed
	VectorDimensions    int          `yaml:"vector_dimensions"`    // default 384 for MiniLM
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


