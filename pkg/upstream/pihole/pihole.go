package pihole

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/upstream/transport"
)

// attemptTimeout bounds each individual HTTP attempt (not the retry chain).
const attemptTimeout = 10 * time.Second

// sidHeader carries the PiHole session token on authenticated requests.
const sidHeader = "X-FTL-SID"

// Upstream is the PiHole upstream adapter, targeting the v6 config API.
// Resilience runs on client's transport chain; session tokens are acquired via
// authenticate over authClient, which omits session middleware to avoid recursion.
type Upstream struct {
	name       string
	baseURL    string
	password   string
	client     *http.Client
	authClient *http.Client
}

// authRequest is the POST /api/auth request body.
type authRequest struct {
	Password string `json:"password"`
}

// authResponse is the POST /api/auth response body.
type authResponse struct {
	Session struct {
		Valid    bool   `json:"valid"`
		SID      string `json:"sid"`
		Validity int    `json:"validity"` // seconds; -1 means no auth required
		Message  string `json:"message"`
	} `json:"session"`
}

// Options configures a PiHole upstream adapter.
type Options struct {
	Name            string
	BaseURL         string
	Password        string
	RetryOptions    transport.RetryOptions // zero value uses defaults
	MaxAuthFailures int                    // consecutive 401s before disabling; 0 uses transport default
	// Debug enables full request/response logging. The auth exchange is only
	// logged with RevealSecrets, as its body carries the password.
	Debug transport.DebugLogOptions
	// Metrics is optional.
	Metrics transport.MetricsRecorder
}

func New(opts Options) *Upstream {
	// The auth request body carries the password, so logging the auth exchange
	// additionally requires RevealSecrets.
	authDebug := opts.Debug
	authDebug.Enabled = authDebug.Enabled && authDebug.RevealSecrets
	authDebug.Name = opts.Name + " (auth)"

	u := &Upstream{
		name:     opts.Name,
		baseURL:  strings.TrimRight(opts.BaseURL, "/"),
		password: opts.Password,
		// authClient acquires sessions: retries only, no session middleware (no
		// token yet) and no breaker. Rejected credentials wrap ErrAuthFailed,
		// which reaches the runtime client's breaker via SessionAuth.
		authClient: &http.Client{
			Transport: transport.Chain(nil,
				transport.Retry(opts.RetryOptions),
				transport.AttemptTimeout(attemptTimeout),
				transport.DebugLog(authDebug),
			),
		},
	}

	// Runtime client: circuit breaker (outermost) → retry → attempt timeout →
	// session auth.
	u.client = transport.NewClient(transport.ClientOptions{
		Name:            opts.Name,
		Timeout:         attemptTimeout,
		Retry:           opts.RetryOptions,
		MaxAuthFailures: opts.MaxAuthFailures,
		Auth: transport.SessionAuth(transport.SessionAuthOptions{
			Header:       sidHeader,
			Authenticate: u.authenticate,
		}),
		Debug:   opts.Debug,
		Metrics: opts.Metrics,
	})
	return u
}

func (u *Upstream) Name() string { return u.name }

// drainClose discards any remaining body and closes it so the underlying
// connection can be reused by keep-alive. Safe with a nil response.
func drainClose(resp *http.Response) {
	if resp == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func (u *Upstream) Upsert(ctx context.Context, r model.Record) error {
	switch r.Type {
	case model.RecordTypeA, model.RecordTypeAAAA:
		return u.patchHosts(ctx, r, false)
	case model.RecordTypeCNAME:
		return u.patchCNAME(ctx, r, false)
	default:
		return fmt.Errorf("pihole: unsupported record type %s", r.Type)
	}
}

func (u *Upstream) Delete(ctx context.Context, r model.Record) error {
	switch r.Type {
	case model.RecordTypeA, model.RecordTypeAAAA:
		return u.patchHosts(ctx, r, true)
	case model.RecordTypeCNAME:
		return u.patchCNAME(ctx, r, true)
	default:
		return fmt.Errorf("pihole: unsupported record type %s", r.Type)
	}
}

// DriftEqual implements upstream.DriftComparer. PiHole cannot represent
// comments at all (Upsert ignores them, see patchHosts/patchCNAME), so Comment
// is never compared. dns.hosts entries (A/AAAA) carry no TTL on the wire —
// only dns.cnameRecords entries have one — so TTL is compared for CNAME only.
func (u *Upstream) DriftEqual(want, got model.Record) bool {
	if want.Type != got.Type || want.Name != got.Name || want.Value != got.Value {
		return false
	}
	if got.Type == model.RecordTypeCNAME && want.TTL != got.TTL {
		return false
	}
	return true
}

// List returns every A/AAAA (dns.hosts) and CNAME (dns.cnameRecords) entry, for
// upstream-verification drift detection. Entries PiHole doesn't attribute to
// beacons (multi-host lines) are skipped rather than guessed at.
func (u *Upstream) List(ctx context.Context) ([]model.Record, error) {
	hosts, err := u.getHosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("pihole list hosts: %w", err)
	}
	cnames, err := u.getCNAMERecords(ctx)
	if err != nil {
		return nil, fmt.Errorf("pihole list cname records: %w", err)
	}

	out := make([]model.Record, 0, len(hosts)+len(cnames))
	for _, entry := range hosts {
		ip, name, ok := splitHostEntry(entry)
		if !ok {
			continue
		}
		out = append(out, model.Record{
			Upstream: u.name,
			Type:     hostRecordType(ip),
			Name:     name,
			Value:    ip,
		})
	}
	for _, entry := range cnames {
		alias, target, ttl := splitCNAMEEntry(entry)
		out = append(out, model.Record{
			Upstream:   u.name,
			Type:       model.RecordTypeCNAME,
			Name:       alias,
			Value:      target,
			BaseRecord: model.BaseRecord{TTL: ttl},
		})
	}
	return out, nil
}

// patchHosts adds or removes an entry from dns.hosts.
// Entry format: "IP hostname"
func (u *Upstream) patchHosts(ctx context.Context, r model.Record, remove bool) error {
	if r.Comment != "" {
		slog.Debug("pihole does not support record comments, ignoring",
			"upstream", u.name,
			"name", r.Name,
			"type", r.Type)
	}

	current, err := u.getHosts(ctx)
	if err != nil {
		return err
	}

	entry := r.Value + " " + r.Name
	desired, changed := applyByName(current, r.Name, entry, remove, hostName)
	if !changed {
		slog.Debug("pihole host entry already up to date, skipping",
			"upstream", u.name,
			"entry", entry)
		return nil
	}

	action := "adding"
	if remove {
		action = "removing"
	}
	slog.Debug("pihole "+action+" host entry",
		"upstream", u.name,
		"entry", entry)
	return u.patch(ctx, map[string]any{
		"config": map[string]any{
			"dns": map[string]any{"hosts": desired},
		},
	})
}

// patchCNAME adds or removes an entry from dns.cnameRecords.
// Entry format: "alias,target" or "alias,target,ttl"
func (u *Upstream) patchCNAME(ctx context.Context, r model.Record, remove bool) error {
	if r.Comment != "" {
		slog.Debug("pihole does not support record comments, ignoring",
			"upstream", u.name,
			"name", r.Name,
			"type", r.Type)
	}

	current, err := u.getCNAMERecords(ctx)
	if err != nil {
		return err
	}

	entry := r.Name + "," + r.Value
	if r.TTL > 0 {
		entry = fmt.Sprintf("%s,%d", entry, r.TTL)
	}
	desired, changed := applyByName(current, r.Name, entry, remove, cnameAlias)
	if !changed {
		slog.Debug("pihole cname entry already up to date, skipping",
			"upstream", u.name,
			"entry", entry)
		return nil
	}

	action := "adding"
	if remove {
		action = "removing"
	}
	slog.Debug("pihole "+action+" cname entry",
		"upstream", u.name,
		"entry", entry)
	return u.patch(ctx, map[string]any{
		"config": map[string]any{
			"dns": map[string]any{"cnameRecords": desired},
		},
	})
}

// getHosts fetches the current dns.hosts list.
func (u *Upstream) getHosts(ctx context.Context) ([]string, error) {
	var result struct {
		Config struct {
			DNS struct {
				Hosts []string `json:"hosts"`
			} `json:"dns"`
		} `json:"config"`
	}
	if err := u.get(ctx, "/api/config/dns/hosts", &result); err != nil {
		return nil, err
	}
	return result.Config.DNS.Hosts, nil
}

// getCNAMERecords fetches the current dns.cnameRecords list.
func (u *Upstream) getCNAMERecords(ctx context.Context) ([]string, error) {
	var result struct {
		Config struct {
			DNS struct {
				CNAMERecords []string `json:"cnameRecords"`
			} `json:"dns"`
		} `json:"config"`
	}
	if err := u.get(ctx, "/api/config/dns/cnameRecords", &result); err != nil {
		return nil, err
	}
	return result.Config.DNS.CNAMERecords, nil
}

// applyByName upserts or removes entry, keyed by DNS name rather than exact
// string. nameOf extracts the owning name from an entry. Removing every entry
// that shares the record's name is what stops a value/TTL change leaving a
// stale duplicate behind. This assumes one entry per name (PiHole is not used
// for multi-value round-robin).
//
// changed reports whether the result differs from entries, so a no-op can skip
// the PATCH.
func applyByName(entries []string, name, entry string, remove bool, nameOf func(string) string) (out []string, changed bool) {
	out = make([]string, 0, len(entries)+1)
	owned, exact := 0, false
	for _, e := range entries {
		if nameOf(e) == name {
			owned++
			if e == entry {
				exact = true
			}
			continue
		}
		out = append(out, e)
	}
	if remove {
		return out, owned > 0
	}
	out = append(out, entry)
	// Unchanged only when the sole existing entry for the name already matches.
	return out, !exact || owned != 1
}

// hostName returns the hostname from a "IP hostname" hosts entry. Beacons writes
// single-host entries; a multi-host line returns the whole trailing segment and
// so won't match a single record name (leaving hand-managed lines untouched).
func hostName(entry string) string {
	if i := strings.IndexByte(entry, ' '); i >= 0 {
		return strings.TrimSpace(entry[i+1:])
	}
	return ""
}

// cnameAlias returns the alias from an "alias,target[,ttl]" cname entry.
func cnameAlias(entry string) string {
	if i := strings.IndexByte(entry, ','); i >= 0 {
		return entry[:i]
	}
	return entry
}

// splitHostEntry parses a "IP hostname" hosts entry into its IP and hostname.
// A multi-host line returns ok=false: beacons only ever writes single-host
// entries, so a multi-host line is hand-managed and not attributable to one record.
func splitHostEntry(entry string) (ip, name string, ok bool) {
	i := strings.IndexByte(entry, ' ')
	if i < 0 {
		return "", "", false
	}
	ip = entry[:i]
	rest := strings.TrimSpace(entry[i+1:])
	if rest == "" || strings.ContainsAny(rest, " \t") {
		return "", "", false
	}
	return ip, rest, true
}

// hostRecordType classifies a hosts-entry IP as A or AAAA.
func hostRecordType(ip string) model.RecordType {
	if strings.Contains(ip, ":") {
		return model.RecordTypeAAAA
	}
	return model.RecordTypeA
}

// splitCNAMEEntry parses an "alias,target[,ttl]" cname entry.
func splitCNAMEEntry(entry string) (alias, target string, ttl int) {
	parts := strings.SplitN(entry, ",", 3)
	alias = parts[0]
	if len(parts) > 1 {
		target = parts[1]
	}
	if len(parts) > 2 {
		ttl, _ = strconv.Atoi(parts[2])
	}
	return alias, target, ttl
}

// authenticate acquires a PiHole session token. It is supplied to the
// SessionAuth middleware, which caches the token and re-invokes this on HTTP
// 401. It uses authClient (no session middleware) to avoid recursion.
func (u *Upstream) authenticate(ctx context.Context) (transport.Session, error) {
	slog.Debug("pihole authenticating",
		"upstream", u.name,
		"url", u.baseURL)

	body, err := json.Marshal(authRequest{Password: u.password})
	if err != nil {
		return transport.Session{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.baseURL+"/api/auth", bytes.NewReader(body))
	if err != nil {
		return transport.Session{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := u.authClient.Do(req)
	if err != nil {
		return transport.Session{}, err
	}
	defer drainClose(resp)

	var ar authResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return transport.Session{}, fmt.Errorf("pihole: failed to decode auth response: %w", err)
	}
	if !ar.Session.Valid {
		// Wrap ErrAuthFailed so Retry does not retry this and the circuit
		// breaker counts it towards disabling the upstream.
		return transport.Session{}, fmt.Errorf("pihole: %w: %s", transport.ErrAuthFailed, ar.Session.Message)
	}

	// validity=-1 means no auth required; SID will be empty — the SessionAuth
	// middleware then omits the header. A non-positive validity caches for the
	// middleware's default window.
	slog.Info("pihole session established",
		"upstream", u.name,
		"validity_seconds", ar.Session.Validity)
	return transport.Session{
		Token:     ar.Session.SID,
		ExpiresIn: time.Duration(ar.Session.Validity) * time.Second,
	}, nil
}

// get performs an authenticated GET and decodes the JSON response into dst.
// Session handling, retry, and circuit-breaking are applied by client's
// transport chain.
func (u *Upstream) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pihole: GET %s returned %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// patch performs an authenticated PATCH with a JSON body. Session handling,
// retry, and circuit-breaking are applied by client's transport chain.
func (u *Upstream) patch(ctx context.Context, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u.baseURL+"/api/config", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		var errBody map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return fmt.Errorf("pihole: PATCH /api/config returned %d: %v", resp.StatusCode, errBody)
	}
	return nil
}
