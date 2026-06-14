package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/cloudflare/cloudflare-go"
)

// Upstream is the Cloudflare upstream adapter.
type Upstream struct {
	name     string
	api      *cloudflare.API
	zoneID   string
	zoneName string // e.g. "example.com", fetched from Cloudflare on init
}

func New(ctx context.Context, name, apiToken, zoneID string) (*Upstream, error) {
	api, err := cloudflare.NewWithAPIToken(apiToken)
	if err != nil {
		return nil, err
	}
	zone, err := api.ZoneDetails(ctx, zoneID)
	if err != nil {
		return nil, fmt.Errorf("cloudflare fetch zone details: %w", err)
	}
	slog.Debug("cloudflare upstream initialised",
		"upstream", name,
		"zone", zone.Name)
	return &Upstream{name: name, api: api, zoneID: zoneID, zoneName: zone.Name}, nil
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
	rc := cloudflare.ZoneIdentifier(u.zoneID)

	// Check if a record with this name and type already exists.
	existing, _, err := u.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{
		Type: string(r.Type),
		Name: fqdn,
	})
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

	updateParams := cloudflare.UpdateDNSRecordParams{
		Type:    string(r.Type),
		Name:    fqdn,
		Content: r.Value,
		TTL:     r.TTL,
		Comment: &r.Comment,
	}
	if r.Priority > 0 {
		p := uint16(r.Priority)
		updateParams.Priority = &p
	}

	if len(existing) > 0 {
		updateParams.ID = existing[0].ID
		slog.Debug("cloudflare updating existing record",
			"upstream", u.name,
			"name", fqdn,
			"type", r.Type,
			"id", existing[0].ID)
		if _, err = u.api.UpdateDNSRecord(ctx, rc, updateParams); err != nil {
			return fmt.Errorf("cloudflare update record: %w", err)
		}
		return nil
	}

	createParams := cloudflare.CreateDNSRecordParams{
		Type:    string(r.Type),
		Name:    fqdn,
		Content: r.Value,
		TTL:     r.TTL,
		Comment: r.Comment,
	}
	if r.Priority > 0 {
		p := uint16(r.Priority)
		createParams.Priority = &p
	}

	slog.Debug("cloudflare creating new record",
		"upstream", u.name,
		"name", fqdn,
		"type", r.Type)
	if _, err = u.api.CreateDNSRecord(ctx, rc, createParams); err != nil {
		// 81058: "An identical record already exists." — race between our list
		// check and the create. The desired state is already present.
		var cfErr *cloudflare.RequestError
		if errors.As(err, &cfErr) {
			for _, code := range cfErr.ErrorCodes() {
				if code == 81058 {
					slog.Debug("cloudflare record already exists, skipping create",
						"upstream", u.name,
						"name", fqdn,
						"type", r.Type)
					return nil
				}
			}
		}
		return fmt.Errorf("cloudflare create record: %w", err)
	}
	return nil
}

func (u *Upstream) Delete(ctx context.Context, r model.Record) error {
	fqdn := u.fqdn(r.Name)
	rc := cloudflare.ZoneIdentifier(u.zoneID)

	// Filter by content (value) so we only delete the record Beacons owns,
	// leaving any manually-created records with the same name+type untouched.
	existing, _, err := u.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{
		Type:    string(r.Type),
		Name:    fqdn,
		Content: r.Value,
	})
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
		if err := u.api.DeleteDNSRecord(ctx, rc, rec.ID); err != nil {
			return fmt.Errorf("cloudflare delete record: %w", err)
		}
	}
	return nil
}
