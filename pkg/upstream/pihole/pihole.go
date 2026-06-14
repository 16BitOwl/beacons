package pihole

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/16bitowl/beacons/internal/model"
)

// Upstream is the PiHole upstream adapter.
// Supports A, AAAA and CNAME records via the PiHole v6 config API.
// Other record types are not supported by PiHole.
type Upstream struct {
	mu       sync.Mutex
	name     string
	baseURL  string
	password string
	client   *http.Client
	session  session
}

type session struct {
	sid       string
	expiresAt time.Time
}

func (s *session) valid() bool {
	return s.sid != "" && time.Now().Before(s.expiresAt)
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
	Name     string
	BaseURL  string
	Password string
}

func New(opts Options) *Upstream {
	return &Upstream{
		name:     opts.Name,
		baseURL:  strings.TrimRight(opts.BaseURL, "/"),
		password: opts.Password,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (u *Upstream) Name() string { return u.name }

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
	updated := toggleEntry(current, entry, remove)
	if len(updated) == len(current) && !remove {
		slog.Debug("pihole host entry already present, skipping", "upstream", u.name, "entry", entry)
		return nil
	}

	action := "adding"
	if remove {
		action = "removing"
	}
	slog.Debug("pihole "+action+" host entry", "upstream", u.name, "entry", entry)
	return u.patch(ctx, map[string]any{
		"config": map[string]any{
			"dns": map[string]any{"hosts": updated},
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
	updated := toggleEntry(current, entry, remove)
	if len(updated) == len(current) && !remove {
		slog.Debug("pihole cname entry already present, skipping",
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
			"dns": map[string]any{"cnameRecords": updated},
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

// toggleEntry adds or removes an entry from a slice.
func toggleEntry(entries []string, entry string, remove bool) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e != entry {
			out = append(out, e)
		}
	}
	if !remove {
		out = append(out, entry)
	}
	return out
}

// ensureSession obtains or reuses a valid session token.
func (u *Upstream) ensureSession(ctx context.Context) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.session.valid() {
		return u.session.sid, nil
	}

	slog.Debug("pihole authenticating", "upstream", u.name, "url", u.baseURL)
	body, _ := json.Marshal(authRequest{Password: u.password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.baseURL+"/api/auth", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := u.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var ar authResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return "", fmt.Errorf("pihole: failed to decode auth response: %w", err)
	}
	if !ar.Session.Valid {
		return "", fmt.Errorf("pihole: authentication failed: %s", ar.Session.Message)
	}

	// validity=-1 means no auth required; sid will be empty/null — that's fine.
	ttl := ar.Session.Validity
	if ttl <= 0 {
		ttl = 1800 // default 30 min
	}
	u.session = session{
		sid:       ar.Session.SID,
		expiresAt: time.Now().Add(time.Duration(ttl)*time.Second - 30*time.Second),
	}
	slog.Info("pihole session established", "upstream", u.name, "validity_seconds", ttl)
	return u.session.sid, nil
}

// get performs an authenticated GET and decodes the JSON response into dst.
func (u *Upstream) get(ctx context.Context, path string, dst any) error {
	sid, err := u.ensureSession(ctx)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.baseURL+path, nil)
	if err != nil {
		return err
	}
	if sid != "" {
		req.Header.Set("X-FTL-SID", sid)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pihole: GET %s returned %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// patch performs an authenticated PATCH with a JSON body.
func (u *Upstream) patch(ctx context.Context, payload any) error {
	sid, err := u.ensureSession(ctx)
	if err != nil {
		return err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u.baseURL+"/api/config", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if sid != "" {
		req.Header.Set("X-FTL-SID", sid)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		var errBody map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return fmt.Errorf("pihole: PATCH /api/config returned %d: %v", resp.StatusCode, errBody)
	}
	return nil
}
