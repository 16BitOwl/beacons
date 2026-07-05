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

// New creates a Server. gatherer is used to serve /metrics (typically a
// *prometheus.Registry). store is used by /healthz to verify liveness.
func New(addr string, store registry.Store, gatherer prometheus.Gatherer) *Server {
	s := &Server{addr: addr, store: store}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/state", s.handleState)
	mux.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))
	s.handler = mux

	return s
}

// Timeouts configures the HTTP server timeout values.
type Timeouts struct {
	ReadTimeout     time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// Run starts the HTTP server and blocks until ctx is cancelled or a listen
// error occurs.
func (s *Server) Run(ctx context.Context, t Timeouts) error {
	srv := &http.Server{
		Handler:     s.handler,
		ReadTimeout: t.ReadTimeout,
		IdleTimeout: t.IdleTimeout,
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

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	records, err := s.store.List()
	if err != nil {
		slog.Warn("healthz: store list failed", "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"error"}`))
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
// backing store type. It is unauthenticated; intended for local use only.
func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	records, err := s.store.List()
	if err != nil {
		slog.Warn("state: store list failed",
			"err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"error"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stateResponse{
		Count:   len(records),
		Records: records,
	})
}
