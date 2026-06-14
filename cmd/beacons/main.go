package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/16bitowl/beacons/internal/config"
	"github.com/16bitowl/beacons/internal/metrics"
	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/registry"
	"github.com/16bitowl/beacons/internal/server"
	internalsync "github.com/16bitowl/beacons/internal/sync"
	"github.com/16bitowl/beacons/pkg/source"
	sourcedocker "github.com/16bitowl/beacons/pkg/source/docker"
	sourceyaml "github.com/16bitowl/beacons/pkg/source/yaml"
	"github.com/16bitowl/beacons/pkg/upstream"
	upstreamcloudflare "github.com/16bitowl/beacons/pkg/upstream/cloudflare"
	upstreampihole "github.com/16bitowl/beacons/pkg/upstream/pihole"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	cfgPath := flag.String("config", "beacons.yaml", "path to config file")
	healthcheck := flag.Bool("healthcheck", false, "hit /healthz and exit 0/1 (for use as Docker HEALTHCHECK)")
	healthAddr := flag.String("healthcheck-addr", "http://localhost:9090", "base URL for -healthcheck")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel(os.Getenv("BEACONS_LOG_LEVEL")),
	})))

	if *healthcheck {
		resp, err := http.Get(*healthAddr + "/healthz") //nolint:noctx
		if err != nil {
			slog.Error("healthcheck failed", "err", err)
			os.Exit(1)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			slog.Error("healthcheck failed", "status", resp.StatusCode)
			os.Exit(1)
		}
		os.Exit(0)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	if cfg.Sync.DryRun {
		slog.Info("[dry-run] mode enabled: upstream changes will be logged only")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pollInterval := time.Duration(cfg.Sync.PollInterval) * time.Second
	debounceDelay := time.Duration(cfg.Sync.DebounceDelay) * time.Millisecond

	// Build upstreams
	upstreams := make(map[string]upstream.Upstream, len(cfg.Upstreams))
	for name, ucfg := range cfg.Upstreams {
		u, err := buildUpstream(ctx, name, ucfg)
		if err != nil {
			slog.Error("failed to build upstream",
				"name", name,
				"err", err)
			os.Exit(1)
		}
		if cfg.Sync.DryRun {
			u = upstream.NewDryRun(u)
		}
		upstreams[name] = u
	}

	// Build sources
	var sources []source.Source
	for name, scfg := range cfg.Sources {
		s, err := buildSource(name, scfg, cfg.Defaults, pollInterval, cfg.Sync.UseEvents, debounceDelay, cfg.Sync.StrictEnv)
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

	// Set up Prometheus registry and metrics.
	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	m := metrics.New(reg)

	retryInterval := time.Duration(cfg.Sync.RetryInterval) * time.Second
	syncer := internalsync.New(store, upstreams, retryInterval, m)

	slog.Info("beacons starting",
		"sources", len(sources),
		"upstreams", len(upstreams),
		"store", cfg.Store.Type,
		"dry_run", cfg.Sync.DryRun,
		"strict_env", cfg.Sync.StrictEnv,
		"poll_interval", pollInterval,
		"use_events", cfg.Sync.UseEvents,
		"debounce_delay", debounceDelay,
		"retry_interval", retryInterval,
		"http_addr", cfg.HTTP.Addr,
	)

	// Start the HTTP server if configured.
	if cfg.HTTP.Addr != "" {
		srv := server.New(cfg.HTTP.Addr, store, reg)
		go func() {
			if err := srv.Run(ctx); err != nil {
				slog.Error("http server error", "err", err)
			}
		}()
	}

	if err := syncer.Run(ctx, sources); err != nil {
		slog.Error("syncer exited with error", "err", err)
		os.Exit(1)
	}
}

func logLevel(l string) slog.Level {
	switch strings.ToLower(l) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
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

func buildUpstream(ctx context.Context, name string, cfg model.UpstreamConfig) (upstream.Upstream, error) {
	switch cfg.Type {
	case "cloudflare":
		return upstreamcloudflare.New(ctx, name, cfg.APIToken, cfg.ZoneID)
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
