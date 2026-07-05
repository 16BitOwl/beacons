package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/upstream/transport"
)

const apiBase = "https://api.cloudflare.com/client/v4"

// errAlreadyExists is the Cloudflare API error code for a duplicate record.
const errAlreadyExists = 81058

// Options configures a Cloudflare upstream adapter.
type Options struct {
	Name            string
	APIToken        string
	ZoneID          string
	RetryOptions    transport.RetryOptions // zero value uses defaults
	MaxAuthFailures int                    // consecutive 401s before disabling; 0 uses transport default
	// Debug enables full request/response logging. Development use only.
	Debug transport.DebugLogOptions
}

// Upstream is the Cloudflare upstream adapter.
type Upstream struct {
	name     string
	client   *cfClient
	zoneName string // e.g. "example.com", fetched from Cloudflare on init
}

func New(ctx context.Context, opts Options) (*Upstream, error) {
	// Use a plain one-shot client for the startup zone validation.
	initClient := &cfClient{
		http: &http.Client{
			Timeout: 15 * time.Second,
			Transport: transport.Chain(nil,
				transport.Bearer(opts.APIToken),
				transport.DebugLog(opts.Debug),
			),
		},
		zoneID:  opts.ZoneID,
		baseURL: apiBase,
	}
	zone, err := initClient.getZone(ctx)
	if err != nil {
		return nil, fmt.Errorf("cloudflare fetch zone details: %w", err)
	}

	// Runtime client gets the full transport chain via the shared constructor:
	// circuit breaker (outermost) → retry → attempt timeout → bearer auth.
	c := &cfClient{
		http: transport.NewClient(transport.ClientOptions{
			Name:            opts.Name,
			Retry:           opts.RetryOptions,
			MaxAuthFailures: opts.MaxAuthFailures,
			Auth:            transport.Bearer(opts.APIToken),
			Debug:           opts.Debug,
		}),
		zoneID:  opts.ZoneID,
		baseURL: apiBase,
	}

	slog.Debug("cloudflare upstream initialized",
		"upstream", opts.Name,
		"zone", zone.Name)
	return &Upstream{name: opts.Name, client: c, zoneName: zone.Name}, nil
}

// fqdn returns name as a fully qualified domain name within the zone.
// If name already ends with the zone domain it is returned unchanged.
func (u *Upstream) fqdn(name string) string {
	suffix := "." + u.zoneName
	if name == u.zoneName || strings.HasSuffix(name, suffix) {
		return name
	}
	return name + suffix
}

func (u *Upstream) Name() string { return u.name }

func (u *Upstream) Upsert(ctx context.Context, r model.Record) error {
	// SRV and CAA require structured Data fields in the Cloudflare API, not a
	// plain Content string. The current model only carries a flat Value, so
	// these types cannot be represented correctly.
	if r.Type == model.RecordTypeSRV || r.Type == model.RecordTypeCAA {
		return fmt.Errorf("cloudflare upstream: record type %s is not supported (requires structured data fields)", r.Type)
	}

	fqdn := u.fqdn(r.Name)

	existing, err := u.client.listDNSRecords(ctx, string(r.Type), fqdn, "")
	if err != nil {
		return fmt.Errorf("cloudflare list records: %w", err)
	}

	if len(existing) > 1 {
		slog.Warn("cloudflare found multiple records matching name and type, updating only the first",
			"upstream", u.name,
			"name", fqdn,
			"type", r.Type,
			"count", len(existing))
	}

	req := dnsRecordRequest{
		Type:    string(r.Type),
		Name:    fqdn,
		Content: r.Value,
		TTL:     r.TTL,
		Comment: r.Comment,
	}
	if r.Priority > 0 {
		p := uint16(r.Priority)
		req.Priority = &p
	}

	if len(existing) > 0 {
		if recordUpToDate(existing[0], req) {
			slog.Debug("cloudflare record already up to date, skipping update",
				"upstream", u.name,
				"name", fqdn,
				"type", r.Type,
				"id", existing[0].ID)
			return nil
		}
		slog.Debug("cloudflare updating existing record",
			"upstream", u.name,
			"name", fqdn,
			"type", r.Type,
			"id", existing[0].ID)
		if _, err = u.client.updateDNSRecord(ctx, existing[0].ID, req); err != nil {
			return fmt.Errorf("cloudflare update record: %w", err)
		}
		return nil
	}

	slog.Debug("cloudflare creating new record",
		"upstream", u.name,
		"name", fqdn,
		"type", r.Type)
	if _, err = u.client.createDNSRecord(ctx, req); err != nil {
		// 81058: "An identical record already exists." — race between our list
		// check and the create. The desired state is already present.
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.HasCode(errAlreadyExists) {
			slog.Debug("cloudflare record already exists, skipping create",
				"upstream", u.name,
				"name", fqdn,
				"type", r.Type)
			return nil
		}
		return fmt.Errorf("cloudflare create record: %w", err)
	}
	return nil
}

func (u *Upstream) Delete(ctx context.Context, r model.Record) error {
	fqdn := u.fqdn(r.Name)

	// Filter by content so we only delete the record Beacons owns,
	// leaving any manually-created records with the same name+type untouched.
	existing, err := u.client.listDNSRecords(ctx, string(r.Type), fqdn, r.Value)
	if err != nil {
		return fmt.Errorf("cloudflare list records: %w", err)
	}
	if len(existing) == 0 {
		slog.Warn("cloudflare record not found for deletion, skipping",
			"upstream", u.name,
			"name", fqdn,
			"type", r.Type,
			"value", r.Value)
		return nil
	}
	for _, rec := range existing {
		slog.Debug("cloudflare deleting record",
			"upstream", u.name,
			"name", fqdn,
			"type", r.Type,
			"id", rec.ID)
		if err := u.client.deleteDNSRecord(ctx, rec.ID); err != nil {
			return fmt.Errorf("cloudflare delete record: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Cloudflare API types
// ---------------------------------------------------------------------------

// apiError is a single error entry returned by the Cloudflare API.
type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// resultInfo is the pagination metadata returned by list endpoints.
type resultInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalPages int `json:"total_pages"`
}

// apiResponse is the common Cloudflare API response envelope.
type apiResponse struct {
	Success    bool            `json:"success"`
	Errors     []apiError      `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo resultInfo      `json:"result_info"`
}

// zone holds the fields we need from a Zone Details response.
type zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// dnsRecord is the Cloudflare representation of a DNS record.
type dnsRecord struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	Content  string  `json:"content"`
	TTL      int     `json:"ttl"`
	Comment  string  `json:"comment,omitempty"`
	Priority *uint16 `json:"priority,omitempty"` // present for MX/SRV only
}

// recordUpToDate reports whether an existing Cloudflare record already matches
// the desired request, letting Upsert skip a no-op PUT.
func recordUpToDate(existing dnsRecord, req dnsRecordRequest) bool {
	if existing.Content != req.Content || existing.TTL != req.TTL || existing.Comment != req.Comment {
		return false
	}
	return priorityEqual(existing.Priority, req.Priority)
}

// priorityEqual treats two nil priorities as equal; used to compare MX/SRV priority.
func priorityEqual(a, b *uint16) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// dnsRecordRequest is the body for create and update calls.
type dnsRecordRequest struct {
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	Content  string  `json:"content"`
	TTL      int     `json:"ttl"`
	Comment  string  `json:"comment,omitempty"`
	Priority *uint16 `json:"priority,omitempty"`
}

// APIError is returned when the Cloudflare API responds with one or more errors.
type APIError struct {
	errors []apiError
}

func (e *APIError) Error() string {
	msgs := make([]string, len(e.errors))
	for i, ae := range e.errors {
		msgs[i] = fmt.Sprintf("%d: %s", ae.Code, ae.Message)
	}
	return "cloudflare api error: " + strings.Join(msgs, "; ")
}

// HasCode reports whether any of the API errors carries the given error code.
func (e *APIError) HasCode(code int) bool {
	for _, ae := range e.errors {
		if ae.Code == code {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// HTTP client
// ---------------------------------------------------------------------------

type cfClient struct {
	http    *http.Client
	zoneID  string
	baseURL string
}

// doRaw executes an HTTP request against the Cloudflare API and returns the
// decoded response envelope. The caller is responsible for unmarshalling Result.
func (c *cfClient) doRaw(ctx context.Context, method, path string, body any) (*apiResponse, error) {
	var reqBody *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(b)
	}

	var req *http.Request
	var err error
	if reqBody != nil {
		req, err = http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	}
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var env apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("cloudflare: decode response: %w", err)
	}
	if !env.Success {
		return nil, &APIError{errors: env.Errors}
	}
	return &env, nil
}

// do executes an HTTP request against the Cloudflare API, decodes the response
// envelope, and unmarshals the result into out (may be nil).
func (c *cfClient) do(ctx context.Context, method, path string, body any, out any) error {
	env, err := c.doRaw(ctx, method, path, body)
	if err != nil {
		return err
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func (c *cfClient) getZone(ctx context.Context) (*zone, error) {
	var z zone
	if err := c.do(ctx, http.MethodGet, "/zones/"+c.zoneID, nil, &z); err != nil {
		return nil, err
	}
	return &z, nil
}

// listDNSRecords lists all records filtered by type and name, fetching every
// page until the result set is exhausted.
// content is optional; pass an empty string to omit the filter.
func (c *cfClient) listDNSRecords(ctx context.Context, recordType, name, content string) ([]dnsRecord, error) {
	const perPage = 100

	var all []dnsRecord
	for page := 1; ; page++ {
		params := url.Values{}
		params.Set("type", recordType)
		params.Set("name", name)
		if content != "" {
			params.Set("content", content)
		}
		params.Set("page", strconv.Itoa(page))
		params.Set("per_page", strconv.Itoa(perPage))

		env, err := c.doRaw(ctx, http.MethodGet, "/zones/"+c.zoneID+"/dns_records?"+params.Encode(), nil)
		if err != nil {
			return nil, err
		}

		var pageRecords []dnsRecord
		if len(env.Result) > 0 {
			if err := json.Unmarshal(env.Result, &pageRecords); err != nil {
				return nil, fmt.Errorf("cloudflare: decode dns records: %w", err)
			}
		}
		all = append(all, pageRecords...)

		if page >= env.ResultInfo.TotalPages || len(pageRecords) == 0 {
			break
		}
	}
	return all, nil
}

func (c *cfClient) createDNSRecord(ctx context.Context, r dnsRecordRequest) (*dnsRecord, error) {
	var rec dnsRecord
	if err := c.do(ctx, http.MethodPost, "/zones/"+c.zoneID+"/dns_records", r, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (c *cfClient) updateDNSRecord(ctx context.Context, id string, r dnsRecordRequest) (*dnsRecord, error) {
	var rec dnsRecord
	if err := c.do(ctx, http.MethodPut, "/zones/"+c.zoneID+"/dns_records/"+id, r, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (c *cfClient) deleteDNSRecord(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/zones/"+c.zoneID+"/dns_records/"+id, nil, nil)
}
