package netutil

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// publicIPEndpoints are queried in order until one succeeds; each must
// respond with nothing but the plain-text address. Var (not const) so tests
// can point it at a local httptest server instead of the real internet.
var publicIPEndpoints = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
}

// publicIPCacheTTL bounds how often the lookup services are actually
// queried. An ISP-assigned address changes rarely, and re-querying
// third-party services on every label parse would be wasteful and risks
// rate-limiting.
const publicIPCacheTTL = 5 * time.Minute

var publicIPHTTPClient = &http.Client{Timeout: 5 * time.Second}

var publicIPCache struct {
	mu      sync.Mutex
	ip      string
	fetched time.Time
}

// PublicIP returns this host's public, ISP-assigned IP address as seen from
// outside the local network, caching results for publicIPCacheTTL to avoid
// hammering the lookup services. It is a package variable rather than a
// plain function so tests can substitute a fake instead of depending on real
// network access.
var PublicIP = func(ctx context.Context) (string, error) {
	publicIPCache.mu.Lock()
	if publicIPCache.ip != "" && time.Since(publicIPCache.fetched) < publicIPCacheTTL {
		ip := publicIPCache.ip
		publicIPCache.mu.Unlock()
		return ip, nil
	}
	publicIPCache.mu.Unlock()

	var errs []string
	for _, endpoint := range publicIPEndpoints {
		ip, err := fetchPublicIP(ctx, endpoint)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", endpoint, err))
			continue
		}
		publicIPCache.mu.Lock()
		publicIPCache.ip = ip
		publicIPCache.fetched = time.Now()
		publicIPCache.mu.Unlock()
		return ip, nil
	}
	return "", fmt.Errorf("netutil: all public ip lookups failed: %s", strings.Join(errs, "; "))
}

// fetchPublicIP performs a single lookup attempt against endpoint.
func fetchPublicIP(ctx context.Context, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := publicIPHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}

	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("response is not a valid ip: %q", ip)
	}
	return ip, nil
}
