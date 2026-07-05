package transport

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// captureLogs redirects the default slog logger into a buffer for the duration
// of the test and returns it.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestDebugLog_DisabledIsNoOp(t *testing.T) {
	// Use a comparable RoundTripper (func values are not comparable).
	base := http.DefaultTransport
	if got := DebugLog(DebugLogOptions{})(base); got != base {
		t.Errorf("disabled DebugLog wrapped the transport; want passthrough")
	}
}

func TestDebugLog_PreservesBodies(t *testing.T) {
	logs := captureLogs(t)

	// Quote-free payloads so the text handler's escaping doesn't interfere
	// with the substring checks below.
	const reqPayload = "request-payload-body"
	const respPayload = "response-payload-body"

	var received string
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		received = string(b)
		resp := fakeResponse(http.StatusOK)
		resp.Body = io.NopCloser(strings.NewReader(respPayload))
		return resp, nil
	})

	tr := Chain(base, DebugLog(DebugLogOptions{Enabled: true, Name: "test"}))

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api?x=1", strings.NewReader(reqPayload))
	// Force the non-replayable path: the middleware must consume the body and
	// still forward it intact.
	req.GetBody = nil

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if received != reqPayload {
		t.Errorf("server received body %q, want %q", received, reqPayload)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if string(body) != respPayload {
		t.Errorf("caller read body %q, want %q", body, respPayload)
	}

	out := logs.String()
	for _, want := range []string{reqPayload, respPayload, "http://example.com/api?x=1", "status=200"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q:\n%s", want, out)
		}
	}
}

func TestDebugLog_RedactsSensitiveHeaders(t *testing.T) {
	logs := captureLogs(t)

	const token = "supersecrettoken1234567890"
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base,
		Bearer(token),
		DebugLog(DebugLogOptions{Enabled: true, Name: "test"}),
	)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	out := logs.String()
	if strings.Contains(out, token) {
		t.Errorf("log output contains the raw token:\n%s", out)
	}
	if !strings.Contains(out, "Authorization") {
		t.Errorf("log output missing the Authorization header:\n%s", out)
	}
}

func TestDebugLog_RevealSecrets(t *testing.T) {
	logs := captureLogs(t)

	const token = "supersecrettoken1234567890"
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base,
		Bearer(token),
		DebugLog(DebugLogOptions{Enabled: true, Name: "test", RevealSecrets: true}),
	)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	if out := logs.String(); !strings.Contains(out, token) {
		t.Errorf("log output should contain the raw token with RevealSecrets:\n%s", out)
	}
}

func TestDebugLog_TruncatesLoggedBody(t *testing.T) {
	logs := captureLogs(t)

	long := strings.Repeat("a", 100)
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		resp := fakeResponse(http.StatusOK)
		resp.Body = io.NopCloser(strings.NewReader(long))
		return resp, nil
	})

	tr := Chain(base, DebugLog(DebugLogOptions{Enabled: true, MaxBodyBytes: 10}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Caller still gets the full body.
	body, _ := io.ReadAll(resp.Body)
	if string(body) != long {
		t.Errorf("caller body truncated: got %d bytes, want %d", len(body), len(long))
	}
	// Log does not.
	out := logs.String()
	if strings.Contains(out, long) {
		t.Errorf("log output contains the full body; want truncation")
	}
	if !strings.Contains(out, "log truncated") {
		t.Errorf("log output missing truncation marker:\n%s", out)
	}
}

func TestRedactValue(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Bearer abcdefghijklmnopqrstuvwxyz", "Bearer abcd…wxyz"},
		{"Bearer short", "Bearer [REDACTED]"},
		{"rawtokenvalue1234567890", "rawt…7890"},
		{"tiny", "[REDACTED]"},
	}
	for _, tt := range tests {
		if got := redactValue(tt.in); got != tt.want {
			t.Errorf("redactValue(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
