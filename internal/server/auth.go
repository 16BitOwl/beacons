package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
)

// Authenticator verifies an incoming request before it reaches a handler.
// Implementations are pluggable so future auth methods (mTLS, OIDC, etc.)
// can be added without changing the server or its handlers.
type Authenticator interface {
	Authenticate(r *http.Request) bool
}

// noAuth allows every request through.
type noAuth struct{}

func (noAuth) Authenticate(*http.Request) bool { return true }

// apiKeyAuth requires a matching key in the X-API-Key header.
type apiKeyAuth struct {
	key string
}

func (a apiKeyAuth) Authenticate(r *http.Request) bool {
	got := r.Header.Get("X-API-Key")
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(a.key)) == 1
}

// AuthConfig selects and configures the authentication method for protected
// HTTP endpoints.
type AuthConfig struct {
	// Type is the auth method: "none" or "api_key".
	Type string

	// APIKey is the shared secret used when Type is "api_key". If empty,
	// NewAuthenticator generates one and prints it to stdout.
	APIKey string
}

// NewAuthenticator builds an Authenticator from cfg. For "api_key" with no
// key set, a random key is generated and printed to stdout — the caller
// should set APIKey explicitly outside of local testing, since a generated
// key changes on every restart.
func NewAuthenticator(cfg AuthConfig) (Authenticator, error) {
	switch cfg.Type {
	case "", "none":
		return noAuth{}, nil
	case "api_key":
		key := cfg.APIKey
		if key == "" {
			generated, err := generateAPIKey()
			if err != nil {
				return nil, fmt.Errorf("generate api key: %w", err)
			}
			key = generated
			slog.Warn("http.auth.api_key not set; generated a random key for this run, set it explicitly or secure endpoint access breaks on every restart")
			slog.Info("generated API key for secure endpoints, pass it via the X-API-Key header",
				"key", key)
		}
		return apiKeyAuth{key: key}, nil
	default:
		return nil, fmt.Errorf("unknown http auth type %q", cfg.Type)
	}
}

// generateAPIKey returns a random 32-byte hex-encoded key.
func generateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// requireAuth wraps h, rejecting requests that fail auth with 401.
func requireAuth(auth Authenticator, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth.Authenticate(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":"unauthorized"}`))
			return
		}
		h(w, r)
	}
}
