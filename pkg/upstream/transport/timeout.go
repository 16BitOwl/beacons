package transport

import (
	"context"
	"io"
	"net/http"
	"time"
)

// AttemptTimeout returns a Middleware that applies timeout to every request
// passing through it, covering the attempt itself and reading its response
// body. Place it inside Retry (after it in the Chain call) so each retry
// attempt gets a fresh deadline — unlike http.Client.Timeout, which spans the
// whole chain including backoff sleeps.
func AttemptTimeout(timeout time.Duration) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return &attemptTimeoutTransport{next: next, timeout: timeout}
	}
}

type attemptTimeoutTransport struct {
	next    http.RoundTripper
	timeout time.Duration
}

func (t *attemptTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(req.Context(), t.timeout)
	resp, err := t.next.RoundTrip(req.Clone(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	// Keep the deadline active while the caller consumes the body; release the
	// context as soon as the body is closed.
	resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnCloseBody cancels its context when the response body is closed, so
// the attempt deadline covers body reads without leaking the context.
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
