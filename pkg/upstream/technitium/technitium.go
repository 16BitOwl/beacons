// Package technitium implements the Upstream adapter for Technitium DNS
// Server (https://technitium.com/dns/).
//
// Written against a Technitium DNS Server v15+ HTTP API (APIDOCS.md in the
// DnsServer repo): auth uses the Authorization: Bearer header directly (the
// `token` query parameter documented there is backward-compat only, kept for
// pre-v15 servers). Zone record endpoints are RPC-style and take every field
// as a query/form parameter rather than a JSON body; record and parameter
// names have changed across major server versions in the past, so re-check
// APIDOCS.md before upgrading the target server.
//
// Supports A, AAAA, CNAME, TXT, MX and NS records. SRV and CAA are not
// supported: Technitium requires structured fields (weight/port for SRV,
// flags/tag for CAA) that model.Record's flat Value/Priority pair cannot
// carry — the same limitation the Cloudflare adapter hits for these two
// types. Extending model.Record with type-specific structured fields would
// resolve this for both adapters.
package technitium

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/upstream/transport"
)

// Options configures a Technitium upstream adapter.
type Options struct {
	Name            string
	BaseURL         string
	APIToken        string
	Zone            string
	RetryOptions    transport.RetryOptions // zero value uses defaults
	MaxAuthFailures int                    // consecutive 401s before disabling; 0 uses transport default
	// Debug enables full request/response logging. Development use only.
	Debug transport.DebugLogOptions
}

// Upstream is the Technitium upstream adapter.
type Upstream struct {
	name   string
	zone   string
	client *tClient
}

func New(ctx context.Context, opts Options) (*Upstream, error) {
	baseURL := strings.TrimRight(opts.BaseURL, "/")

	// Startup zone validation uses its own client instance (fresh circuit
	// breaker) so a boot-time hiccup doesn't count against the runtime
	// client's breaker — same reasoning as the Cloudflare adapter's initClient.
	initClient := &tClient{
		http: transport.NewClient(transport.ClientOptions{
			Name:            opts.Name,
			Retry:           opts.RetryOptions,
			MaxAuthFailures: opts.MaxAuthFailures,
			Auth:            transport.Bearer(opts.APIToken),
			Debug:           opts.Debug,
		}),
		baseURL: baseURL,
		zone:    opts.Zone,
	}
	if err := initClient.checkZone(ctx); err != nil {
		return nil, fmt.Errorf("technitium fetch zone options: %w", err)
	}

	c := &tClient{
		http: transport.NewClient(transport.ClientOptions{
			Name:            opts.Name,
			Retry:           opts.RetryOptions,
			MaxAuthFailures: opts.MaxAuthFailures,
			Auth:            transport.Bearer(opts.APIToken),
			Debug:           opts.Debug,
		}),
		baseURL: baseURL,
		zone:    opts.Zone,
	}

	slog.Debug("technitium upstream initialized",
		"upstream", opts.Name,
		"zone", opts.Zone)
	return &Upstream{name: opts.Name, zone: opts.Zone, client: c}, nil
}

func (u *Upstream) Name() string { return u.name }

// supported reports whether t can be represented with the flat Value/Priority
// fields model.Record carries. SRV and CAA cannot — see the package doc comment.
func supported(t model.RecordType) bool {
	switch t {
	case model.RecordTypeA, model.RecordTypeAAAA, model.RecordTypeCNAME,
		model.RecordTypeTXT, model.RecordTypeMX, model.RecordTypeNS:
		return true
	default:
		return false
	}
}

func (u *Upstream) Upsert(ctx context.Context, r model.Record) error {
	if !supported(r.Type) {
		return fmt.Errorf("technitium: unsupported record type %s (requires structured fields model.Record's flat value can't carry)", r.Type)
	}

	existing, err := u.client.getRecords(ctx, r.Name)
	if err != nil {
		return fmt.Errorf("technitium get records: %w", err)
	}
	matches := filterByType(existing, r.Type)

	if len(matches) > 1 {
		slog.Warn("technitium found multiple records matching name and type, updating only the first",
			"upstream", u.name,
			"name", r.Name,
			"type", r.Type,
			"count", len(matches))
	}

	if len(matches) == 0 {
		slog.Debug("technitium creating new record",
			"upstream", u.name,
			"name", r.Name,
			"type", r.Type)
		return u.client.addRecord(ctx, addParams(u.zone, r))
	}

	cur := matches[0]
	if recordUpToDate(cur, r) {
		slog.Debug("technitium record already up to date, skipping",
			"upstream", u.name,
			"name", r.Name,
			"type", r.Type)
		return nil
	}

	slog.Debug("technitium updating existing record",
		"upstream", u.name,
		"name", r.Name,
		"type", r.Type)
	return u.client.updateRecord(ctx, updateParams(u.zone, cur, r))
}

func (u *Upstream) Delete(ctx context.Context, r model.Record) error {
	if !supported(r.Type) {
		return fmt.Errorf("technitium: unsupported record type %s", r.Type)
	}

	existing, err := u.client.getRecords(ctx, r.Name)
	if err != nil {
		return fmt.Errorf("technitium get records: %w", err)
	}
	if len(filterByType(existing, r.Type)) == 0 {
		slog.Warn("technitium record not found for deletion, skipping",
			"upstream", u.name,
			"name", r.Name,
			"type", r.Type)
		return nil
	}

	slog.Debug("technitium deleting record",
		"upstream", u.name,
		"name", r.Name,
		"type", r.Type)
	return u.client.deleteRecord(ctx, deleteParams(u.zone, r))
}

// DriftEqual implements upstream.DriftComparer. The zones/records/get
// endpoint does not return each record's comment (see recordData/zoneRecord),
// so List() never populates it — Comment is never compared. TTL and every
// other applied field are read back accurately and compared normally.
func (u *Upstream) DriftEqual(want, got model.Record) bool {
	return want.Type == got.Type &&
		want.Name == got.Name &&
		want.Value == got.Value &&
		want.TTL == got.TTL &&
		want.Priority == got.Priority
}

// List returns every supported-type record in the zone, for upstream-
// verification drift detection. Unlike Upsert/Delete's getRecords (scoped to
// one domain), it lists the whole zone via listZone.
func (u *Upstream) List(ctx context.Context) ([]model.Record, error) {
	records, err := u.client.listZoneRecords(ctx)
	if err != nil {
		return nil, fmt.Errorf("technitium list records: %w", err)
	}
	out := make([]model.Record, 0, len(records))
	for _, rec := range records {
		t := model.RecordType(rec.Type)
		if !supported(t) {
			continue // SRV/CAA etc.: not representable, never managed by beacons
		}
		out = append(out, model.Record{
			Upstream: u.name,
			Type:     t,
			Name:     rec.Name,
			Value:    recordValue(rec),
			BaseRecord: model.BaseRecord{
				TTL:      rec.TTL,
				Priority: recordPriority(rec),
			},
		})
	}
	return out, nil
}

// recordValue extracts the flat Value beacons tracks for rec's type, mirroring
// the type switch recordUpToDate and addParams use to build/compare records.
func recordValue(rec zoneRecord) string {
	switch rec.Type {
	case string(model.RecordTypeA), string(model.RecordTypeAAAA):
		return rec.RData.IPAddress
	case string(model.RecordTypeCNAME):
		return rec.RData.CNAME
	case string(model.RecordTypeTXT):
		return rec.RData.Text
	case string(model.RecordTypeMX):
		return rec.RData.Exchange
	case string(model.RecordTypeNS):
		return rec.RData.NameServer
	default:
		return ""
	}
}

// recordPriority extracts MX preference; other types carry no priority.
func recordPriority(rec zoneRecord) int {
	if rec.Type == string(model.RecordTypeMX) {
		return rec.RData.Preference
	}
	return 0
}

// ---------------------------------------------------------------------------
// Record matching and param building
// ---------------------------------------------------------------------------

// filterByType returns the records in records whose Type equals t.
func filterByType(records []zoneRecord, t model.RecordType) []zoneRecord {
	out := make([]zoneRecord, 0, len(records))
	for _, rec := range records {
		if rec.Type == string(t) {
			out = append(out, rec)
		}
	}
	return out
}

// recordUpToDate reports whether an existing Technitium record already
// matches the desired record, letting Upsert skip a no-op update call.
func recordUpToDate(cur zoneRecord, r model.Record) bool {
	if cur.TTL != r.TTL {
		return false
	}
	switch r.Type {
	case model.RecordTypeA, model.RecordTypeAAAA:
		return cur.RData.IPAddress == r.Value
	case model.RecordTypeCNAME:
		return cur.RData.CNAME == r.Value
	case model.RecordTypeTXT:
		return cur.RData.Text == r.Value
	case model.RecordTypeMX:
		return cur.RData.Exchange == r.Value && cur.RData.Preference == r.Priority
	case model.RecordTypeNS:
		return cur.RData.NameServer == r.Value
	default:
		return false
	}
}

// addParams builds the records/add query parameters for r.
func addParams(zone string, r model.Record) url.Values {
	p := url.Values{
		"domain": {r.Name},
		"zone":   {zone},
		"type":   {string(r.Type)},
		"ttl":    {strconv.Itoa(r.TTL)},
	}
	if r.Comment != "" {
		p.Set("comments", r.Comment)
	}
	switch r.Type {
	case model.RecordTypeA, model.RecordTypeAAAA:
		p.Set("ipAddress", r.Value)
	case model.RecordTypeCNAME:
		p.Set("cname", r.Value)
	case model.RecordTypeTXT:
		p.Set("text", r.Value)
	case model.RecordTypeMX:
		p.Set("exchange", r.Value)
		p.Set("preference", strconv.Itoa(r.Priority))
	case model.RecordTypeNS:
		p.Set("nameServer", r.Value)
	}
	return p
}

// updateParams builds the records/update query parameters, identifying the
// record via cur's current values and setting r's values as the new ones.
// CNAME has no "current value" identifying parameter: domain+type is unique
// per RFC (only one CNAME per name), so the param sets the new target directly.
func updateParams(zone string, cur zoneRecord, r model.Record) url.Values {
	p := url.Values{
		"domain": {r.Name},
		"zone":   {zone},
		"type":   {string(r.Type)},
		"ttl":    {strconv.Itoa(r.TTL)},
	}
	if r.Comment != "" {
		p.Set("comments", r.Comment)
	}
	switch r.Type {
	case model.RecordTypeA, model.RecordTypeAAAA:
		p.Set("ipAddress", cur.RData.IPAddress)
		p.Set("newIpAddress", r.Value)
	case model.RecordTypeCNAME:
		p.Set("cname", r.Value)
	case model.RecordTypeTXT:
		p.Set("text", cur.RData.Text)
		p.Set("newText", r.Value)
	case model.RecordTypeMX:
		p.Set("exchange", cur.RData.Exchange)
		p.Set("newExchange", r.Value)
		p.Set("preference", strconv.Itoa(cur.RData.Preference))
		p.Set("newPreference", strconv.Itoa(r.Priority))
	case model.RecordTypeNS:
		p.Set("nameServer", cur.RData.NameServer)
		p.Set("newNameServer", r.Value)
	}
	return p
}

// deleteParams builds the records/delete query parameters that identify r.
// CNAME needs no identifying value parameter: domain+type alone is unique.
func deleteParams(zone string, r model.Record) url.Values {
	p := url.Values{
		"domain": {r.Name},
		"zone":   {zone},
		"type":   {string(r.Type)},
	}
	switch r.Type {
	case model.RecordTypeA, model.RecordTypeAAAA:
		p.Set("ipAddress", r.Value)
	case model.RecordTypeTXT:
		p.Set("text", r.Value)
	case model.RecordTypeMX:
		p.Set("exchange", r.Value)
		p.Set("preference", strconv.Itoa(r.Priority))
	case model.RecordTypeNS:
		p.Set("nameServer", r.Value)
	}
	return p
}

// ---------------------------------------------------------------------------
// Technitium API types
// ---------------------------------------------------------------------------

// apiResponse is the common Technitium API response envelope.
type apiResponse struct {
	Status       string          `json:"status"`
	ErrorMessage string          `json:"errorMessage"`
	Response     json.RawMessage `json:"response"`
}

// recordData holds the rData fields used by the record types this adapter
// supports. Fields for other types are omitted; Technitium ignores unknown
// response fields and never expects these unmarshalled back into a request.
type recordData struct {
	IPAddress  string `json:"ipAddress,omitempty"`
	CNAME      string `json:"cname,omitempty"`
	Text       string `json:"text,omitempty"`
	Exchange   string `json:"exchange,omitempty"`
	Preference int    `json:"preference,omitempty"`
	NameServer string `json:"nameServer,omitempty"`
}

// zoneRecord is a single record entry as returned by records/get.
type zoneRecord struct {
	Name  string     `json:"name"`
	Type  string     `json:"type"`
	TTL   int        `json:"ttl"`
	RData recordData `json:"rData"`
}

// APIError is returned when the Technitium API responds with a non-"ok" status.
type APIError struct {
	Status  string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("technitium api error (%s): %s", e.Status, e.Message)
}

// ---------------------------------------------------------------------------
// HTTP client
// ---------------------------------------------------------------------------

type tClient struct {
	http    *http.Client
	baseURL string
	zone    string
}

// drainClose discards any remaining body and closes it so the underlying
// connection can be reused by keep-alive. Safe with a nil response.
func drainClose(resp *http.Response) {
	if resp == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// do executes a Technitium API call and decodes the response envelope.
// GET requests carry params in the query string; every other method sends
// them as a application/x-www-form-urlencoded body, matching the content
// type the API requires for POST calls.
func (c *tClient) do(ctx context.Context, method, path string, params url.Values) (*apiResponse, error) {
	var req *http.Request
	var err error
	if method == http.MethodGet {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+params.Encode(), nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, c.baseURL+path, strings.NewReader(params.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)

	var env apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("technitium: decode response: %w", err)
	}
	if env.Status != "ok" {
		return nil, &APIError{Status: env.Status, Message: env.ErrorMessage}
	}
	return &env, nil
}

// checkZone validates that zone exists, used once at startup so a
// misconfigured zone name fails fast at boot rather than on first sync.
func (c *tClient) checkZone(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/api/zones/options/get", url.Values{"zone": {c.zone}})
	return err
}

// getRecords fetches the records at domain, scoped to that name (not the whole zone).
func (c *tClient) getRecords(ctx context.Context, domain string) ([]zoneRecord, error) {
	env, err := c.do(ctx, http.MethodGet, "/api/zones/records/get", url.Values{
		"domain": {domain},
		"zone":   {c.zone},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Records []zoneRecord `json:"records"`
	}
	if err := json.Unmarshal(env.Response, &out); err != nil {
		return nil, fmt.Errorf("technitium: decode records: %w", err)
	}
	return out.Records, nil
}

// listZoneRecords fetches every record in the zone (domain=zone, listZone=true
// lists the whole zone starting at the apex, rather than getRecords' single
// domain scope).
func (c *tClient) listZoneRecords(ctx context.Context) ([]zoneRecord, error) {
	env, err := c.do(ctx, http.MethodGet, "/api/zones/records/get", url.Values{
		"domain":   {c.zone},
		"zone":     {c.zone},
		"listZone": {"true"},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Records []zoneRecord `json:"records"`
	}
	if err := json.Unmarshal(env.Response, &out); err != nil {
		return nil, fmt.Errorf("technitium: decode records: %w", err)
	}
	return out.Records, nil
}

func (c *tClient) addRecord(ctx context.Context, params url.Values) error {
	_, err := c.do(ctx, http.MethodPost, "/api/zones/records/add", params)
	return err
}

func (c *tClient) updateRecord(ctx context.Context, params url.Values) error {
	_, err := c.do(ctx, http.MethodPost, "/api/zones/records/update", params)
	return err
}

func (c *tClient) deleteRecord(ctx context.Context, params url.Values) error {
	_, err := c.do(ctx, http.MethodPost, "/api/zones/records/delete", params)
	return err
}
