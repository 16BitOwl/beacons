package config

import (
	"os"

	"github.com/16bitowl/beacons/internal/envutil"
	"github.com/16bitowl/beacons/internal/model"
	"github.com/goccy/go-yaml"
)

// Config is the top-level beacons configuration
type Config struct {
	// LogLevel sets the log verbosity: debug, info, warn, error (default: info)
	LogLevel string `yaml:"log_level"`

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
	PollInterval int `yaml:"poll_interval"`

	// UseEvents enables real-time Docker event watching alongside polling
	UseEvents bool `yaml:"use_events"`

	// DebounceDelay collapses rapid container events (kill/stop/die/start) into
	// a single action after this many milliseconds of quiet. 0 disables debouncing.
	DebounceDelay int `yaml:"debounce_ms"`

	// DryRun logs upstream operations instead of applying them
	DryRun bool `yaml:"dry_run"`

	// StrictEnv causes startup to fail if any ${VAR} references are unset
	StrictEnv bool `yaml:"strict_env"`

	// RetryInterval is how often (in seconds) the syncer re-attempts records
	// that previously failed to push to their upstream. 0 disables retries.
	RetryInterval int `yaml:"retry_interval"`
}

// Load reads and parses the config file at the given path, then overlays any
// BEACONS_* environment variables. The config file is optional — if path is
// empty or the file does not exist, config is sourced entirely from env vars.
func Load(path string) (*Config, error) {
	var cfg Config

	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			// First pass: lenient expansion to read the strict_env setting.
			if err := yaml.Unmarshal([]byte(envutil.ExpandLenient(string(raw))), &cfg); err != nil {
				return nil, err
			}
			// Second pass: strict expansion if required.
			if cfg.Sync.StrictEnv {
				expanded, err := envutil.Expand(string(raw))
				if err != nil {
					return nil, err
				}
				if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
					return nil, err
				}
			}
		}
	}

	overlayEnv(&cfg)
	return &cfg, nil
}
