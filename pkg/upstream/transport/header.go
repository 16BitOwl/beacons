package transport

import "net/http"

// Header returns a Middleware that sets a static header on every request.
func Header(name, value string) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return &headerTransport{next: next, name: name, value: value}
	}
}

type headerTransport struct {
	next  http.RoundTripper
	name  string
	value string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set(t.name, t.value)
	return t.next.RoundTrip(r)
}
