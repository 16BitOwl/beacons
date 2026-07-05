package transport

import "net/http"

// Bearer returns a Middleware that injects a static Bearer token into every
// request's Authorization header.
func Bearer(token string) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return &bearerTransport{next: next, token: token}
	}
}

type bearerTransport struct {
	next  http.RoundTripper
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.next.RoundTrip(r)
}
