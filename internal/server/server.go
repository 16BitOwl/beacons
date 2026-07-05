// Package server provides the HTTP server for beacons, exposing the
// /healthz, /state and /metrics endpoints.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/registry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server is an HTTP server exposing /healthz, /state and /metrics.
type Server struct {
	addr    string
	store   registry.Store
	handler http.Handler
}

// Options configures a Server.
type Options struct {
	// Addr is the TCP address to listen on, e.g. ":9090".
	Addr string

	// Store backs /healthz and /state.
	Store registry.Store

	// Gatherer is used to serve /metrics (typically a *prometheus.Registry).
	Gatherer prometheus.Gatherer

	// Auth authenticates requests to protected endpoints (currently /state).
	// Nil allows all requests.
	Auth Authenticator
}

// New creates a Server from opts.
func New(opts Options) *Server {
	s := &Server{addr: opts.Addr, store: opts.Store}

	auth := opts.Auth
	if auth == nil {
		auth = noAuth{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /state", requireAuth(auth, s.handleState))
	mux.Handle("GET /metrics", promhttp.HandlerFor(opts.Gatherer, promhttp.HandlerOpts{}))
	s.handler = mux

	return s
}

// Timeouts configures the HTTP server timeout values.
type Timeouts struct {
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// Run starts the HTTP server and blocks until ctx is cancelled or a listen
// error occurs.
func (s *Server) Run(ctx context.Context, t Timeouts) error {
	srv := &http.Server{
		Handler:      s.handler,
		ReadTimeout:  t.ReadTimeout,
		WriteTimeout: t.WriteTimeout,
		IdleTimeout:  t.IdleTimeout,
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	slog.Info("http server listening",
		"addr", ln.Addr().String())

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), t.ShutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

type healthResponse struct {
	Status  string `json:"status"`
	Records int    `json:"records"`
}

// errorResponse is the JSON body returned for handler failures. Shared across
// endpoints so error bodies have one consistent schema.
type errorResponse struct {
	Error string `json:"error"`
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	records, err := s.store.List()
	if err != nil {
		slog.Warn("healthz: store list failed", "err", err)
		writeJSONError(w, http.StatusServiceUnavailable, "failed to list records")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthResponse{
		Status:  "ok",
		Records: len(records),
	})
}

// stateResponse is the JSON body returned by /state.
type stateResponse struct {
	Count   int            `json:"count"`
	Records []model.Record `json:"records"`
}

// handleState returns the full current state as JSON, independent of the
// backing store type. Protected by the server's configured Authenticator.
func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	records, err := s.store.List()
	if err != nil {
		slog.Warn("state: store list failed",
			"err", err)
		writeJSONError(w, http.StatusServiceUnavailable, "failed to list records")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stateResponse{
		Count:   len(records),
		Records: records,
	})
}
