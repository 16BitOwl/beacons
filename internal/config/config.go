package config

import (
	"os"

	"github.com/16bitowl/beacons/internal/envutil"
	"github.com/16bitowl/beacons/internal/model"
	"gopkg.in/yaml.v3"
)

// Config is the top-level beacons configuration
type Config struct {
	// Defaults applied to all records unless overridden
	Defaults model.BaseRecord `yaml:"defaults"`

	// Sources is a map of named source adapter instances
	Sources map[string]model.SourceConfig `yaml:"sources"`

	// Upstreams is a map of named upstream adapter instances
	Upstreams map[string]model.UpstreamConfig `yaml:"upstreams"`

	// Sync controls the sync loop behaviour
	Sync SyncConfig `yaml:"sync"`

	// Store controls record persistence
	Store StoreConfig `yaml:"store"`
}

// StoreConfig controls how records are persisted between restarts.
type StoreConfig struct {
	// Type is the store backend: "memory" (default) or "file".
	Type string `yaml:"type"`

	// Path is the file path used by the file store.
	Path string `yaml:"path"`
}

type SyncConfig struct {
	// PollInterval is the Docker polling interval in seconds (0 = disabled)
	// Can also be set via the BEACONS_POLL_INTERVAL=int environment variable
	PollInterval int `yaml:"poll_interval"`

	// UseEvents enables real-time Docker event watching alongside polling
	// Can also be enabled via the BEACONS_USE_EVENTS=bool environment variable
	UseEvents bool `yaml:"use_events"`

	// DryRun logs upstream operations instead of applying them
	// Can also be enabled via the BEACONS_DRY_RUN=bool environment variable
	DryRun bool `yaml:"dry_run"`

	// StrictEnv causes startup to fail if any ${VAR} references are unset
	// Can also be enabled via the BEACONS_STRICT_ENV=bool environment variable
	StrictEnv bool `yaml:"strict_env"`
}

// Load reads and parses the config file at the given path.
// It performs a lenient first pass to read strict_env, then re-expands strictly if enabled.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// First pass: lenient expansion to read the strict_env setting.
	var cfg Config
	if err := yaml.Unmarshal([]byte(envutil.ExpandLenient(string(raw))), &cfg); err != nil {
		return nil, err
	}

	// Env var takes precedence over config file.
	strict := cfg.Sync.StrictEnv || os.Getenv("BEACONS_STRICT_ENV") == "true"
	if !strict {
		return &cfg, nil
	}

	// Second pass: strict expansion now that we know it's required.
	expanded, err := envutil.Expand(string(raw))
	if err != nil {
		return nil, err
	}
	var strictCfg Config
	if err := yaml.Unmarshal([]byte(expanded), &strictCfg); err != nil {
		return nil, err
	}
	return &strictCfg, nil
}
