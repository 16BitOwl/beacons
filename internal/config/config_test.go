package config

import (
	"os"
	"testing"

	"github.com/16bitowl/beacons/internal/model"
)

// setenv sets env vars for the duration of a test and restores them on cleanup.
func setenv(t *testing.T, kvs map[string]string) {
	t.Helper()
	for k, v := range kvs {
		t.Setenv(k, v)
	}
}

// loadYAML is a helper that writes a YAML string to a temp file and calls Load.
func loadYAML(t *testing.T, yaml string) *Config {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "beacons-*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(yaml); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

// TestStaticFieldsSetFromEnv verifies that static struct fields are populated
// purely from env vars when no config file is provided.
func TestStaticFieldsSetFromEnv(t *testing.T) {
	setenv(t, map[string]string{
		"BEACONS_DEFAULTS_TTL":      "300",
		"BEACONS_DEFAULTS_COMMENT":  "hello",
		"BEACONS_SYNC_POLL_INTERVAL": "60",
		"BEACONS_SYNC_DRY_RUN":      "true",
	})

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Defaults.TTL != 300 {
		t.Errorf("Defaults.TTL = %d, want 300", cfg.Defaults.TTL)
	}
	if cfg.Defaults.Comment != "hello" {
		t.Errorf("Defaults.Comment = %q, want %q", cfg.Defaults.Comment, "hello")
	}
	if cfg.Sync.PollInterval != 60 {
		t.Errorf("Sync.PollInterval = %d, want 60", cfg.Sync.PollInterval)
	}
	if !cfg.Sync.DryRun {
		t.Errorf("Sync.DryRun = false, want true")
	}
}

// TestStaticFieldsOverriddenByEnv verifies that env vars take precedence over
// values already loaded from a YAML file.
func TestStaticFieldsOverriddenByEnv(t *testing.T) {
	setenv(t, map[string]string{
		"BEACONS_DEFAULTS_TTL":      "600",
		"BEACONS_SYNC_POLL_INTERVAL": "120",
	})

	cfg := loadYAML(t, `
defaults:
  ttl: 300
sync:
  poll_interval: 30
`)

	if cfg.Defaults.TTL != 600 {
		t.Errorf("Defaults.TTL = %d, want 600 (env override)", cfg.Defaults.TTL)
	}
	if cfg.Sync.PollInterval != 120 {
		t.Errorf("Sync.PollInterval = %d, want 120 (env override)", cfg.Sync.PollInterval)
	}
}

// TestDynamicMapEntrySetFromEnv verifies that a new map entry (not in YAML) is
// created purely from env vars using the __KEY__ convention.
func TestDynamicMapEntrySetFromEnv(t *testing.T) {
	setenv(t, map[string]string{
		"BEACONS_UPSTREAMS__CF_ZONE_C__TYPE":      "cloudflare",
		"BEACONS_UPSTREAMS__CF_ZONE_C__API_TOKEN": "token-c",
		"BEACONS_UPSTREAMS__CF_ZONE_C__ZONE_ID":   "zone-c",
	})

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	u, ok := cfg.Upstreams["cf_zone_c"]
	if !ok {
		t.Fatalf("expected upstream cf_zone_c to be created, got keys: %v", mapKeys(cfg.Upstreams))
	}
	if u.Type != "cloudflare" {
		t.Errorf("Type = %q, want cloudflare", u.Type)
	}
	if u.APIToken != "token-c" {
		t.Errorf("APIToken = %q, want token-c", u.APIToken)
	}
	if u.ZoneID != "zone-c" {
		t.Errorf("ZoneID = %q, want zone-c", u.ZoneID)
	}
}

// TestDynamicMapEntryOverridesYAML verifies that env vars override fields on a
// map entry that was already loaded from YAML, including hyphenated keys.
func TestDynamicMapEntryOverridesYAML(t *testing.T) {
	setenv(t, map[string]string{
		"BEACONS_UPSTREAMS__CF_ZONE_A__API_TOKEN": "new-token",
		"BEACONS_UPSTREAMS__CF_ZONE_A__ZONE_ID":   "new-zone",
	})

	cfg := loadYAML(t, `
upstreams:
  cf-zone-a:
    type: cloudflare
    api_token: old-token
    zone_id: old-zone
`)

	u, ok := cfg.Upstreams["cf-zone-a"]
	if !ok {
		t.Fatalf("expected upstream cf-zone-a, got keys: %v", mapKeys(cfg.Upstreams))
	}
	if u.Type != "cloudflare" {
		t.Errorf("Type = %q, want cloudflare (should be unchanged)", u.Type)
	}
	if u.APIToken != "new-token" {
		t.Errorf("APIToken = %q, want new-token", u.APIToken)
	}
	if u.ZoneID != "new-zone" {
		t.Errorf("ZoneID = %q, want new-zone", u.ZoneID)
	}
}

// TestDynamicMapEntryWithUnderscoredKey verifies that map keys containing
// underscores (e.g. pihole_home) are handled correctly.
func TestDynamicMapEntryWithUnderscoredKey(t *testing.T) {
	setenv(t, map[string]string{
		"BEACONS_UPSTREAMS__PIHOLE_HOME__TYPE":     "pihole",
		"BEACONS_UPSTREAMS__PIHOLE_HOME__URL":      "http://pihole.home",
		"BEACONS_UPSTREAMS__PIHOLE_HOME__PASSWORD": "secret",
	})

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	u, ok := cfg.Upstreams["pihole_home"]
	if !ok {
		t.Fatalf("expected upstream pihole_home, got keys: %v", mapKeys(cfg.Upstreams))
	}
	if u.Type != "pihole" {
		t.Errorf("Type = %q, want pihole", u.Type)
	}
	if u.URL != "http://pihole.home" {
		t.Errorf("URL = %q, want http://pihole.home", u.URL)
	}
	if u.Password != "secret" {
		t.Errorf("Password = %q, want secret", u.Password)
	}
}

// TestMultipleDynamicMapEntries verifies that multiple new map entries can be
// created from env vars in the same section.
func TestMultipleDynamicMapEntries(t *testing.T) {
	setenv(t, map[string]string{
		"BEACONS_UPSTREAMS__CF_ZONE_A__TYPE":      "cloudflare",
		"BEACONS_UPSTREAMS__CF_ZONE_A__API_TOKEN": "tok-a",
		"BEACONS_UPSTREAMS__CF_ZONE_A__ZONE_ID":   "zone-a",
		"BEACONS_UPSTREAMS__CF_ZONE_B__TYPE":      "cloudflare",
		"BEACONS_UPSTREAMS__CF_ZONE_B__API_TOKEN": "tok-b",
		"BEACONS_UPSTREAMS__CF_ZONE_B__ZONE_ID":   "zone-b",
		"BEACONS_SOURCES__DOCKER_LOCAL__TYPE":     "docker",
		"BEACONS_SOURCES__DOCKER_LOCAL__HOST":     "unix:///var/run/docker.sock",
	})

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Upstreams) != 2 {
		t.Errorf("got %d upstreams, want 2", len(cfg.Upstreams))
	}
	if len(cfg.Sources) != 1 {
		t.Errorf("got %d sources, want 1", len(cfg.Sources))
	}
	if cfg.Sources["docker_local"].Host != "unix:///var/run/docker.sock" {
		t.Errorf("Sources[docker_local].Host = %q, want unix:///var/run/docker.sock", cfg.Sources["docker_local"].Host)
	}
}

// TestEnvOnlyConfig verifies a complete config can be built with no file at all.
func TestEnvOnlyConfig(t *testing.T) {
	setenv(t, map[string]string{
		"BEACONS_DEFAULTS_TTL":                    "120",
		"BEACONS_SYNC_POLL_INTERVAL":              "30",
		"BEACONS_SYNC_USE_EVENTS":                 "true",
		"BEACONS_STORE_TYPE":                      "file",
		"BEACONS_STORE_PATH":                      "/data/state.json",
		"BEACONS_UPSTREAMS__CF__TYPE":             "cloudflare",
		"BEACONS_UPSTREAMS__CF__API_TOKEN":        "tok",
		"BEACONS_UPSTREAMS__CF__ZONE_ID":          "z1",
		"BEACONS_SOURCES__DOCKER__TYPE":           "docker",
	})

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Defaults.TTL != 120 {
		t.Errorf("Defaults.TTL = %d, want 120", cfg.Defaults.TTL)
	}
	if cfg.Sync.PollInterval != 30 {
		t.Errorf("Sync.PollInterval = %d, want 30", cfg.Sync.PollInterval)
	}
	if !cfg.Sync.UseEvents {
		t.Errorf("Sync.UseEvents = false, want true")
	}
	if cfg.Store.Type != "file" {
		t.Errorf("Store.Type = %q, want file", cfg.Store.Type)
	}
	if cfg.Store.Path != "/data/state.json" {
		t.Errorf("Store.Path = %q, want /data/state.json", cfg.Store.Path)
	}
	if u := cfg.Upstreams["cf"]; u.Type != "cloudflare" || u.APIToken != "tok" || u.ZoneID != "z1" {
		t.Errorf("Upstreams[cf] = %+v, want cloudflare/tok/z1", u)
	}
	if s := cfg.Sources["docker"]; s.Type != "docker" {
		t.Errorf("Sources[docker].Type = %q, want docker", s.Type)
	}
}

// TestYAMLOnlyNoEnv verifies that a plain YAML load without any env vars works
// and is not affected by the overlay.
func TestYAMLOnlyNoEnv(t *testing.T) {
	cfg := loadYAML(t, `
defaults:
  ttl: 300
upstreams:
  cf-zone-a:
    type: cloudflare
    api_token: tok
    zone_id: z1
`)

	if cfg.Defaults.TTL != 300 {
		t.Errorf("Defaults.TTL = %d, want 300", cfg.Defaults.TTL)
	}
	u, ok := cfg.Upstreams["cf-zone-a"]
	if !ok {
		t.Fatalf("expected upstream cf-zone-a")
	}
	if u.APIToken != "tok" {
		t.Errorf("APIToken = %q, want tok", u.APIToken)
	}
}

// TestEnvDoesNotClobberUnsetYAMLFields verifies that an env var for one field
// does not zero out sibling fields that were set in YAML.
func TestEnvDoesNotClobberUnsetYAMLFields(t *testing.T) {
	setenv(t, map[string]string{
		"BEACONS_UPSTREAMS__CF_ZONE_A__API_TOKEN": "new-token",
	})

	cfg := loadYAML(t, `
upstreams:
  cf-zone-a:
    type: cloudflare
    api_token: old-token
    zone_id: should-be-kept
`)

	u := cfg.Upstreams["cf-zone-a"]
	if u.ZoneID != "should-be-kept" {
		t.Errorf("ZoneID = %q, want should-be-kept (must not be clobbered)", u.ZoneID)
	}
	if u.APIToken != "new-token" {
		t.Errorf("APIToken = %q, want new-token", u.APIToken)
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// Ensure model is used (it's referenced via Config fields).
var _ model.BaseRecord
