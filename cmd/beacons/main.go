package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/16bitowl/beacons/internal/config"
	hc "github.com/16bitowl/beacons/internal/healthcheck"
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
	"github.com/16bitowl/beacons/pkg/upstream/transport"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// version and buildTime are set via -ldflags at build time.
var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	cfgPath := flag.String("config", "beacons.yaml", "path to config file")
	doHealthcheck := flag.Bool("healthcheck", false, "hit /healthz and exit 0/1 (for use as Docker HEALTHCHECK)")
	healthAddr := flag.String("healthcheck-addr", "http://localhost:9090", "base URL for -healthcheck")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s (built %s)\n", version, buildTime)
		os.Exit(0)
	}

	initLogger()

	if *doHealthcheck {
		if err := hc.Check(*healthAddr); err != nil {
			slog.Error("healthcheck failed",
				"err", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("failed to load config",
			"err", err)
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
			slog.Error("upstream failed to initialise, disabling until restart; fix the configuration and restart Beacons",
				"name", name,
				"err", err)
			upstreams[name] = upstream.NewDisabled(name, err)
			continue
		}
		if cfg.Sync.DryRun {
			u = upstream.NewDryRun(u)
		}
		upstreams[name] = u
	}

	// Build sources
	var sources []source.Source
	for name, scfg := range cfg.Sources {
		s, err := buildSource(buildSourceOptions{
			Name:             name,
			Config:           scfg,
			Defaults:         cfg.Defaults,
			PollInterval:     pollInterval,
			UseEvents:        cfg.Sync.UseEvents,
			DebounceDelay:    debounceDelay,
			StrictEnv:        cfg.Sync.StrictEnv,
			StrictValidation: cfg.Sync.StrictValidation,
		})
		if err != nil {
			slog.Error("failed to build source",
				"name", name,
				"err", err)
			os.Exit(1)
		}
		sources = append(sources, s)
	}

	store, err := buildStore(cfg.Store)
	if err != nil {
		slog.Error("failed to initialise store",
			"err", err)
		os.Exit(1)
	}

	// Set up Prometheus registry and metrics.
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	m := metrics.New(reg)

	retryInterval := time.Duration(cfg.Sync.RetryInterval) * time.Second
	syncer := internalsync.New(internalsync.Options{
		Store:         store,
		Upstreams:     upstreams,
		RetryInterval: retryInterval,
		Metrics:       m,
	})

	slog.Info("beacons starting",
		"version", version,
		"build_time", buildTime,
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
		"http_auth_type", cfg.HTTP.Auth.Type,
	)

	// Start the HTTP server if configured. Both it and the syncer are run to
	// completion before main returns, so shutdown (including the HTTP
	// server's graceful ShutdownTimeout) is never cut short.
	var wg sync.WaitGroup
	var httpErr error
	if cfg.HTTP.Addr != "" {
		auth, err := server.NewAuthenticator(server.AuthConfig{
			Type:   cfg.HTTP.Auth.Type,
			APIKey: cfg.HTTP.Auth.APIKey,
		})
		if err != nil {
			slog.Error("failed to initialise http auth",
				"err", err)
			os.Exit(1)
		}

		srv := server.New(server.Options{
			Addr:     cfg.HTTP.Addr,
			Store:    store,
			Gatherer: reg,
			Auth:     auth,
		})
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.Run(ctx, server.Timeouts{
				ReadTimeout:     time.Duration(cfg.HTTP.ReadTimeout) * time.Second,
				WriteTimeout:    time.Duration(cfg.HTTP.WriteTimeout) * time.Second,
				IdleTimeout:     time.Duration(cfg.HTTP.IdleTimeout) * time.Second,
				ShutdownTimeout: time.Duration(cfg.HTTP.ShutdownTimeout) * time.Second,
			}); err != nil {
				httpErr = err
				slog.Error("http server error, stopping beacons",
					"err", err)
				cancel() // treat a listen failure as fatal: stop the syncer too
			}
		}()
	}

	syncErr := syncer.Run(ctx, sources)
	cancel() // in case the syncer exited on its own, make sure the server shuts down too
	wg.Wait()

	if syncErr != nil {
		slog.Error("syncer exited with error",
			"err", syncErr)
	}
	if syncErr != nil || httpErr != nil {
		os.Exit(1)
	}
}

// initLogger configures the default slog logger from environment variables.
// Settings for logs are intentionally env-only so logging is ready before the
// config file is parsed.
func initLogger() {
	opts := &slog.HandlerOptions{Level: logLevel(os.Getenv("BEACONS_LOG_LEVEL"))}

	var handler slog.Handler
	if strings.EqualFold(os.Getenv("BEACONS_LOG_FORMAT"), "json") {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}

func logLevel(level string) slog.Level {
	switch strings.ToLower(level) {
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
		return upstreamcloudflare.New(ctx, upstreamcloudflare.Options{
			Name:            name,
			APIToken:        cfg.APIToken,
			ZoneID:          cfg.ZoneID,
			MaxAuthFailures: cfg.HTTP.AuthFailureThreshold,
			RetryOptions:    httpRetryOptions(cfg.HTTP),
			Debug:           httpDebugOptions(name, cfg.HTTP),
		})
	case "pihole":
		return upstreampihole.New(upstreampihole.Options{
			Name:            name,
			BaseURL:         cfg.URL,
			Password:        cfg.Password,
			MaxAuthFailures: cfg.HTTP.AuthFailureThreshold,
			RetryOptions:    httpRetryOptions(cfg.HTTP),
			Debug:           httpDebugOptions(name, cfg.HTTP),
		}), nil
	default:
		return nil, fmt.Errorf("unknown upstream type %q for %q", cfg.Type, name)
	}
}

// httpRetryOptions maps the shared HTTP tuning config into transport retry
// options. Zero fields fall back to the transport defaults.
func httpRetryOptions(cfg model.UpstreamHTTPConfig) transport.RetryOptions {
	return transport.RetryOptions{
		MaxAttempts: cfg.RetryMaxAttempts,
		BaseDelay:   time.Duration(cfg.RetryBaseDelayMs) * time.Millisecond,
		MaxDelay:    time.Duration(cfg.RetryMaxDelayMs) * time.Millisecond,
	}
}

// httpDebugOptions maps the shared HTTP tuning config into debug-log options.
func httpDebugOptions(name string, cfg model.UpstreamHTTPConfig) transport.DebugLogOptions {
	if cfg.DebugLog {
		slog.Warn("upstream http debug logging enabled: full requests and responses are written at debug level (set BEACONS_LOG_LEVEL=debug to see them); development use only",
			"upstream", name,
			"reveal_secrets", cfg.DebugLogSecrets)
	}
	return transport.DebugLogOptions{
		Enabled:       cfg.DebugLog,
		Name:          name,
		RevealSecrets: cfg.DebugLogSecrets,
	}
}

type buildSourceOptions struct {
	Name             string
	Config           model.SourceConfig
	Defaults         model.BaseRecord
	PollInterval     time.Duration
	UseEvents        bool
	DebounceDelay    time.Duration
	StrictEnv        bool
	StrictValidation bool
}

func buildSource(opts buildSourceOptions) (source.Source, error) {
	switch opts.Config.Type {
	case "docker":
		return sourcedocker.New(sourcedocker.Options{
			Name:             opts.Name,
			Host:             opts.Config.Host,
			Defaults:         opts.Defaults,
			PollInterval:     opts.PollInterval,
			UseEvents:        opts.UseEvents,
			DebounceDelay:    opts.DebounceDelay,
			StrictValidation: opts.StrictValidation,
		})
	case "yaml":
		return sourceyaml.New(sourceyaml.Options{
			Name:             opts.Name,
			Glob:             opts.Config.Glob,
			Defaults:         opts.Defaults,
			Strict:           opts.StrictEnv,
			StrictValidation: opts.StrictValidation,
		}), nil
	default:
		return nil, fmt.Errorf("unknown source type %q for %q", opts.Config.Type, opts.Name)
	}
}
