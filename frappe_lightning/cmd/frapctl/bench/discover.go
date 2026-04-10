package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FindRoot walks up from startDir until it finds a directory that contains
// sites/common_site_config.json — the canonical bench root marker.
func FindRoot(startDir string) (string, error) {
	dir := startDir
	for {
		if _, err := os.Stat(filepath.Join(dir, "sites", "common_site_config.json")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside a frappe-bench directory (no sites/common_site_config.json found)")
		}
		dir = parent
	}
}

// ListSites returns every directory under <benchRoot>/sites that looks like a
// Frappe site (contains site_config.json).
func ListSites(benchRoot string) ([]string, error) {
	sitesDir := filepath.Join(benchRoot, "sites")
	entries, err := os.ReadDir(sitesDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read sites dir: %w", err)
	}
	var sites []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(sitesDir, e.Name(), "site_config.json")); err == nil {
			sites = append(sites, e.Name())
		}
	}
	return sites, nil
}

// InstalledApps returns the list of apps installed for a site by reading
// <benchRoot>/sites/<site>/apps.txt (one app per line).
func InstalledApps(benchRoot, site string) ([]string, error) {
	path := filepath.Join(benchRoot, "sites", site, "apps.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var apps []string
	for _, line := range splitLines(string(data)) {
		if line != "" {
			apps = append(apps, line)
		}
	}
	return apps, nil
}

// AppVersion reads the version from <benchRoot>/apps/<app>/<app>/__version__.py
// or returns "unknown" if not found.
func AppVersion(benchRoot, app string) string {
	candidates := []string{
		filepath.Join(benchRoot, "apps", app, app, "__version__.py"),
		filepath.Join(benchRoot, "apps", app, "VERSION"),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		// __version__.py typically: __version__ = "15.28.0"
		raw := string(data)
		if v := extractVersion(raw); v != "" {
			return v
		}
		return strings.TrimSpace(raw)
	}
	return "unknown"
}

// extractVersion pulls the version string from a __version__.py file.
func extractVersion(src string) string {
	// match: __version__ = "x.y.z" or __version__ = 'x.y.z'
	for _, line := range splitLines(src) {
		if idx := strings.Index(line, "__version__"); idx >= 0 {
			for _, q := range []string{`"`, `'`} {
				start := strings.Index(line, q)
				if start < 0 {
					continue
				}
				end := strings.Index(line[start+1:], q)
				if end < 0 {
					continue
				}
				return line[start+1 : start+1+end]
			}
		}
	}
	return ""
}

// RunBench executes the bench binary from <benchRoot>/env/bin/bench with the
// given arguments, streaming stdout/stderr directly to the terminal.
func RunBench(benchRoot string, args ...string) error {
	benchBin := filepath.Join(benchRoot, "env", "bin", "bench")
	if _, err := os.Stat(benchBin); os.IsNotExist(err) {
		// Fall back to PATH bench if the venv one is missing.
		benchBin = "bench"
	}
	cmd := newCmd(benchBin, args...)
	cmd.Dir = benchRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// CommonConfig holds the fields from sites/common_site_config.json that frapctl needs.
type CommonConfig struct {
	RedisCache    string `json:"redis_cache"`
	RedisQueue    string `json:"redis_queue"`
	WebserverPort int    `json:"webserver_port"`
	DefaultSite   string `json:"default_site"`
}

// LoadCommonConfig parses sites/common_site_config.json in the bench root.
func LoadCommonConfig(benchRoot string) (*CommonConfig, error) {
	path := filepath.Join(benchRoot, "sites", "common_site_config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg CommonConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SiteConfig holds the fields from sites/<site>/site_config.json.
type SiteConfig struct {
	DBName     string `json:"db_name"`
	DBPassword string `json:"db_password"`
	DBUser     string `json:"db_user,omitempty"`
	DBHost     string `json:"db_host,omitempty"`
	DBPort     int    `json:"db_port,omitempty"`
}

// LoadSiteConfig parses sites/<site>/site_config.json.
func LoadSiteConfig(benchRoot, site string) (*SiteConfig, error) {
	path := filepath.Join(benchRoot, "sites", site, "site_config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("site %q not found: %w", site, err)
	}
	var cfg SiteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SetSiteConfigKey writes a single key/value into sites/<site>/site_config.json
// atomically (write to .tmp then rename).
func SetSiteConfigKey(benchRoot, site, key string, value interface{}) error {
	path := filepath.Join(benchRoot, "sites", site, "site_config.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	raw[key] = value

	out, err := json.MarshalIndent(raw, "", " ")
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// GetSiteConfigKey returns a value from site_config.json as a string.
func GetSiteConfigKey(benchRoot, site, key string) (string, error) {
	path := filepath.Join(benchRoot, "sites", site, "site_config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", err
	}
	v, ok := raw[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in site_config.json", key)
	}
	return fmt.Sprintf("%v", v), nil
}
