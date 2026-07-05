package config

import (
	"os"

	"github.com/16bitowl/beacons/internal/envutil"
	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/validate"
	"github.com/goccy/go-yaml"
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

	// HTTP configures the built-in HTTP server (healthz + metrics).
	// Leave Addr empty to disable the server entirely.
	HTTP HTTPConfig `yaml:"http"`
}

// HTTPConfig controls the built-in HTTP server.
type HTTPConfig struct {
	// Addr is the TCP address to listen on, e.g. ":9090".
	// An empty string disables the server.
	Addr string `yaml:"addr" validate:"omitempty,hostname_port"`

	// ReadTimeout configure the HTTP server read timeout
	// in seconds (0 = infinite)
	ReadTimeout int `yaml:"read_timeout" validate:"min=0"`

	// IdleTimeout configure the HTTP server read timeout
	// in seconds (0 = infinite)
	IdleTimeout int `yaml:"idle_timeout" validate:"min=0"`

	// ShutdownTimeout configure the HTTP server read timeout
	// in seconds, must be none zero
	ShutdownTimeout int `yaml:"shutdown_timeout" validate:"gt=0"`

	// Auth configures authentication for protected endpoints (currently /state).
	Auth AuthConfig `yaml:"auth"`
}

// AuthConfig selects and configures the authentication method for protected
// HTTP endpoints. Type is pluggable so other methods can be added later.
type AuthConfig struct {
	// Type is the auth method: "none" or "api_key".
	Type string `yaml:"type" validate:"omitempty,oneof=none api_key"`

	// APIKey is the shared secret required when Type is "api_key", sent via
	// the X-API-Key header. If empty, a random key is generated at startup
	// and printed to stdout — set this explicitly outside of local testing.
	APIKey string `yaml:"api_key"`
}

// StoreConfig controls how records are persisted between restarts.
type StoreConfig struct {
	// Type is the store backend: "memory" or "file".
	Type string `yaml:"type" validate:"required,oneof=memory file"`

	// Path is the file path used by the file store.
	Path string `yaml:"path" validate:"required_if=Type file"`
}

type SyncConfig struct {
	// PollInterval is the Docker polling interval in seconds (0 = disabled)
	PollInterval int `yaml:"poll_interval" validate:"min=0"`

	// UseEvents enables real-time Docker event watching alongside polling
	UseEvents bool `yaml:"use_events"`

	// DebounceDelay collapses rapid container events (kill/stop/die/start) into
	// a single action after this many milliseconds of quiet. 0 disables debouncing.
	DebounceDelay int `yaml:"debounce_ms" validate:"min=0"`

	// DryRun logs upstream operations instead of applying them
	DryRun bool `yaml:"dry_run"`

	// StrictEnv causes startup to fail if any ${VAR} references are unset
	StrictEnv bool `yaml:"strict_env"`

	// StrictValidation causes invalid records from sources (Docker labels,
	// YAML files) to be treated as fatal errors rather than warnings.
	// Defaults to false — invalid records are skipped with a warning.
	StrictValidation bool `yaml:"strict_validation"`

	// RetryInterval is how often (in seconds) the syncer re-attempts records
	// that previously failed to push to their upstream. 0 disables retries.
	RetryInterval int `yaml:"retry_interval" validate:"min=0"`
}

// Load various default values for the configurations of Beacons
func defaults() Config {
	return Config{
		Sync: SyncConfig{
			PollInterval:  300,
			DebounceDelay: 500,
			RetryInterval: 30,
			DryRun:        false,
			StrictEnv:     true,
			UseEvents:     true,
		},
		Store: StoreConfig{
			Type: "memory",
		},
		HTTP: HTTPConfig{
			Addr:            ":9090",
			ReadTimeout:     5,
			IdleTimeout:     60,
			ShutdownTimeout: 5,
			Auth:            AuthConfig{Type: "api_key"},
		},
	}
}

// Load reads and parses the config file at the given path, then overlays any
// BEACONS_* environment variables. The config file is optional — if path is
// empty or the file does not exist, config is sourced entirely from env vars.
func Load(path string) (*Config, error) {
	cfg := defaults()

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

	if err := validate.Struct(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
