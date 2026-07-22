// Package validateconfig implements the beacons -validate-config check.
package validateconfig

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/16bitowl/beacons/internal/config"
	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/source"
)

// SourceBuilder constructs a source adapter, mirroring cmd/beacons's own source wiring.
type SourceBuilder func(name string, cfg model.SourceConfig, defaults model.BaseRecord, strictEnv, strictValidation bool) (source.Snapshotter, error)

// Run never dials Docker or an upstream API; only yaml sources are
// snapshotted, since that's a pure file read.
func Run(ctx context.Context, cfg *config.Config, buildSource SourceBuilder) error {
	for name, scfg := range cfg.Sources {
		s, err := buildSource(name, scfg, cfg.Defaults, cfg.Sync.StrictEnv, cfg.Sync.StrictValidation)
		if err != nil {
			return fmt.Errorf("source %q: %w", name, err)
		}

		if scfg.Type != "yaml" {
			slog.Info("config validate: source OK (not read)",
				"source", name,
				"type", scfg.Type)
			continue
		}

		records, err := s.Snapshot(ctx)
		if err != nil {
			return fmt.Errorf("source %q: %w", name, err)
		}
		slog.Info("config validate: source OK",
			"source", name,
			"type", scfg.Type,
			"records", len(records))
	}

	for name, ucfg := range cfg.Upstreams {
		slog.Info("config validate: upstream OK (not contacted)",
			"upstream", name,
			"type", ucfg.Type)
	}

	return nil
}
