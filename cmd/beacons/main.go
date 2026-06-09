package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/16bitowl/beacons/internal/config"
	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/registry"
	internalsync "github.com/16bitowl/beacons/internal/sync"
	"github.com/16bitowl/beacons/pkg/source"
	sourcedocker "github.com/16bitowl/beacons/pkg/source/docker"
	sourceyaml "github.com/16bitowl/beacons/pkg/source/yaml"
	"github.com/16bitowl/beacons/pkg/upstream"
	upstreamcloudflare "github.com/16bitowl/beacons/pkg/upstream/cloudflare"
	upstreampihole "github.com/16bitowl/beacons/pkg/upstream/pihole"
)

func main() {
	cfgPath := flag.String("config", "beacons.yaml", "path to config file")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("failed to load config",
			"err", err)
		os.Exit(1)
	}

	// Env var takes precedence over config file.
	dryRun := cfg.Sync.DryRun || os.Getenv("BEACONS_DRY_RUN") == "true"
	if dryRun {
		slog.Info("[dry-run] mode enabled: upstream changes will be logged only")
	}

	strict := cfg.Sync.StrictEnv || os.Getenv("BEACONS_STRICT_ENV") == "true"
	useEvents := cfg.Sync.UseEvents || os.Getenv("BEACONS_USE_EVENTS") == "true"
	pollSeconds := cfg.Sync.PollInterval
	if v, err := strconv.Atoi(os.Getenv("BEACONS_POLL_INTERVAL")); err == nil && v > -1 {
		pollSeconds = v
	}
	pollInterval := time.Duration(pollSeconds) * time.Second

	debounceMS := cfg.Sync.DebounceDelay
	if v, err := strconv.Atoi(os.Getenv("BEACONS_DEBOUNCE_MS")); err == nil && v >= 0 {
		debounceMS = v
	}
	debounceDelay := time.Duration(debounceMS) * time.Millisecond

	// Build upstreams
	upstreams := make(map[string]upstream.Upstream, len(cfg.Upstreams))
	for name, ucfg := range cfg.Upstreams {
		u, err := buildUpstream(name, ucfg)
		if err != nil {
			slog.Error("failed to build upstream",
				"name", name,
				"err", err)
			os.Exit(1)
		}
		if dryRun {
			u = upstream.NewDryRun(u)
		}
		upstreams[name] = u
	}

	// Build sources
	var sources []source.Source
	for name, scfg := range cfg.Sources {
		s, err := buildSource(name, scfg, cfg.Defaults, pollInterval, useEvents, debounceDelay, strict)
		if err != nil {
			slog.Error("failed to build source", "name", name, "err", err)
			os.Exit(1)
		}
		sources = append(sources, s)
	}

	store, err := buildStore(cfg.Store)
	if err != nil {
		slog.Error("failed to initialise store", "err", err)
		os.Exit(1)
	}
	syncer := internalsync.New(store, upstreams)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	slog.Info("beacons starting",
		"sources", len(sources),
		"upstreams", len(upstreams),
		"store", cfg.Store.Type,
		"dry_run", dryRun,
		"strict_env", strict,
		"poll_interval", pollInterval,
		"use_events", useEvents,
		"debounce_delay", debounceDelay,
	)
	if err := syncer.Run(ctx, sources); err != nil {
		slog.Error("syncer exited with error", "err", err)
		os.Exit(1)
	}
}

func buildStore(cfg config.StoreConfig) (registry.Store, error) {
	switch cfg.Type {
	case "file":
		path := cfg.Path
		if path == "" {
			path = "/data/beacons-state.json"
		}
		return registry.NewFileStore(path)
	default: // "memory" or unset
		return registry.NewMemoryStore(), nil
	}
}

func buildUpstream(name string, cfg model.UpstreamConfig) (upstream.Upstream, error) {
	switch cfg.Type {
	case "cloudflare":
		return upstreamcloudflare.New(name, cfg.APIToken, cfg.ZoneID)
	case "pihole":
		return upstreampihole.New(name, cfg.URL, cfg.Password), nil
	default:
		slog.Warn("unknown upstream type",
			"name", name,
			"type", cfg.Type)
		return nil, nil
	}
}

func buildSource(name string, cfg model.SourceConfig, defaults model.BaseRecord, pollInterval time.Duration, useEvents bool, debounceDelay time.Duration, strict bool) (source.Source, error) {
	switch cfg.Type {
	case "docker":
		return sourcedocker.New(name, cfg.Host, defaults, pollInterval, useEvents, debounceDelay)
	case "yaml":
		return sourceyaml.New(name, cfg.Glob, defaults, strict), nil
	default:
		slog.Warn("unknown source type",
			"name", name,
			"type", cfg.Type)
		return nil, nil
	}
}
