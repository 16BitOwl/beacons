package transport

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// defaultDebugMaxBody caps how much of a request or response body is written
// to the log. The full body is always preserved for the caller.
const defaultDebugMaxBody = 64 * 1024

// sensitiveHeaders lists headers (canonical form) whose values are redacted
// in debug logs unless RevealSecrets is set.
var sensitiveHeaders = map[string]bool{
	"Authorization":       true,
	"Proxy-Authorization": true,
	"Cookie":              true,
	"Set-Cookie":          true,
	"X-Api-Key":           true,
	"X-Ftl-Sid":           true,
}

// debugSeq numbers requests so concurrent request/response log lines can be
// correlated via the shared "id" field.
var debugSeq atomic.Uint64

// DebugLogOptions configures the DebugLog middleware.
type DebugLogOptions struct {
	// Enabled turns the middleware on; when false DebugLog is a no-op.
	Enabled bool
	// Name identifies the upstream in log lines. Optional.
	Name string
	// RevealSecrets logs sensitive headers verbatim instead of redacted.
	RevealSecrets bool
	// MaxBodyBytes caps how much of each body is logged (caller always gets
	// the full body). Zero uses the default (64 KiB).
	MaxBodyBytes int
}

// DebugLog returns a Middleware that logs every request and response in full:
// method, complete URL, all headers, bodies, status, and duration. A shared
// "id" field pairs request and response lines under concurrency.
//
// Development use only. Lines are emitted at debug level as a second gate
// against accidental exposure, sensitive headers are redacted unless
// RevealSecrets is set, and bodies are never redacted. Place it innermost in
// the chain so it records the exact wire request and each retry attempt.
func DebugLog(opts DebugLogOptions) Middleware {
	if !opts.Enabled {
		return func(next http.RoundTripper) http.RoundTripper { return next }
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = defaultDebugMaxBody
	}
	return func(next http.RoundTripper) http.RoundTripper {
		return &debugTransport{next: next, opts: opts}
	}
}

type debugTransport struct {
	next http.RoundTripper
	opts DebugLogOptions
}

func (t *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	id := debugSeq.Add(1)

	req, reqBody, err := t.captureRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("transport: debug log failed to read request body: %w", err)
	}

	slog.Debug("transport: http request",
		"upstream", t.opts.Name,
		"id", id,
		"method", req.Method,
		"url", req.URL.String(),
		"headers", headersForLog(req.Header, t.opts.RevealSecrets),
		"body", reqBody)

	start := time.Now()
	resp, err := t.next.RoundTrip(req)
	duration := time.Since(start)

	if err != nil {
		slog.Debug("transport: http request failed",
			"upstream", t.opts.Name,
			"id", id,
			"method", req.Method,
			"url", req.URL.String(),
			"duration", duration,
			"err", err)
		return nil, err
	}

	respBody := t.captureResponseBody(resp)

	slog.Debug("transport: http response",
		"upstream", t.opts.Name,
		"id", id,
		"status", resp.StatusCode,
		"url", req.URL.String(),
		"duration", duration,
		"headers", headersForLog(resp.Header, t.opts.RevealSecrets),
		"body", respBody)

	return resp, nil
}

// captureRequestBody returns the request to forward and its body rendered for
// logging. It reads a fresh copy via GetBody when available; otherwise it
// consumes the body and clones the request with a buffered replacement.
func (t *debugTransport) captureRequestBody(req *http.Request) (*http.Request, string, error) {
	if req.Body == nil {
		return req, "", nil
	}

	src := req.Body
	replayable := req.GetBody != nil
	if replayable {
		fresh, err := req.GetBody()
		if err != nil {
			return nil, "", err
		}
		src = fresh
	}

	data, err := io.ReadAll(src)
	src.Close()
	if err != nil {
		return nil, "", err
	}

	if !replayable {
		r := req.Clone(req.Context())
		r.Body = io.NopCloser(bytes.NewReader(data))
		req = r
	}
	return req, t.bodyForLog(data), nil
}

// captureResponseBody buffers the response body for logging and replaces
// resp.Body so the caller still reads the full payload, including any
// mid-stream read error.
func (t *debugTransport) captureResponseBody(resp *http.Response) string {
	if resp.Body == nil {
		return ""
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = &replayBody{r: bytes.NewReader(data), err: err}

	body := t.bodyForLog(data)
	if err != nil {
		body += fmt.Sprintf(" [body read error: %v]", err)
	}
	return body
}

// replayBody replays buffered data, returning err (if any) instead of io.EOF
// once exhausted.
type replayBody struct {
	r   *bytes.Reader
	err error
}

func (b *replayBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err == io.EOF && b.err != nil {
		return n, b.err
	}
	return n, err
}

func (b *replayBody) Close() error { return nil }

// bodyForLog renders a body for logging, truncating beyond MaxBodyBytes.
func (t *debugTransport) bodyForLog(data []byte) string {
	if len(data) <= t.opts.MaxBodyBytes {
		return string(data)
	}
	return fmt.Sprintf("%s… [log truncated, %d of %d bytes shown]",
		data[:t.opts.MaxBodyBytes], t.opts.MaxBodyBytes, len(data))
}

// headersForLog flattens headers into a map for logging, redacting sensitive
// values unless reveal is set.
func headersForLog(h http.Header, reveal bool) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		v := strings.Join(vals, ", ")
		if !reveal && sensitiveHeaders[http.CanonicalHeaderKey(k)] {
			v = redactValue(v)
		}
		out[k] = v
	}
	return out
}

// redactValue reduces a sensitive header value to its first and last four
// characters, keeping any auth-scheme prefix readable. Short values are fully
// masked.
func redactValue(v string) string {
	prefix := ""
	if scheme, rest, found := strings.Cut(v, " "); found {
		prefix, v = scheme+" ", rest
	}
	if len(v) < 16 {
		return prefix + "[REDACTED]"
	}
	return prefix + v[:4] + "…" + v[len(v)-4:]
}
