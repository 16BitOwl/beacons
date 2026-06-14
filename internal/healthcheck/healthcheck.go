// Package healthcheck provides a client for the Beacons /healthz endpoint.
// It is used as the implementation behind the -healthcheck CLI flag, which
// Docker invokes as the container HEALTHCHECK command.
package healthcheck

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const timeout = 5 * time.Second

// Check performs a GET /healthz against baseURL with a 5-second timeout.
// Returns nil on HTTP 200, a descriptive error otherwise.
func Check(baseURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
