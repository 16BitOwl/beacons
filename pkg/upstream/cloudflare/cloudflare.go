package cloudflare

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/cloudflare/cloudflare-go"
)

// Upstream is the Cloudflare upstream adapter.
type Upstream struct {
	name   string
	api    *cloudflare.API
	zoneID string
}

func New(name, apiToken, zoneID string) (*Upstream, error) {
	api, err := cloudflare.NewWithAPIToken(apiToken)
	if err != nil {
		return nil, err
	}
	return &Upstream{name: name, api: api, zoneID: zoneID}, nil
}

func (u *Upstream) Name() string { return u.name }

func (u *Upstream) Upsert(ctx context.Context, r model.Record) error {
	rc := cloudflare.ZoneIdentifier(u.zoneID)

	// Check if a record with this name and type already exists.
	existing, _, err := u.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{
		Type: string(r.Type),
		Name: r.Name,
	})
	if err != nil {
		return fmt.Errorf("cloudflare list records: %w", err)
	}

	updateParams := cloudflare.UpdateDNSRecordParams{
		Type:    string(r.Type),
		Name:    r.Name,
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
		slog.Debug("cloudflare updating existing record", "upstream", u.name, "name", r.Name, "type", r.Type, "id", existing[0].ID)
		_, err = u.api.UpdateDNSRecord(ctx, rc, updateParams)
		return err
	}

	slog.Debug("cloudflare creating new record", "upstream", u.name, "name", r.Name, "type", r.Type)
	_, err = u.api.CreateDNSRecord(ctx, rc, cloudflare.CreateDNSRecordParams{
		Type:    string(r.Type),
		Name:    r.Name,
		Content: r.Value,
		TTL:     r.TTL,
		Comment: r.Comment,
	})
	return err
}

func (u *Upstream) Delete(ctx context.Context, r model.Record) error {
	rc := cloudflare.ZoneIdentifier(u.zoneID)

	existing, _, err := u.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{
		Type: string(r.Type),
		Name: r.Name,
	})
	if err != nil {
		return fmt.Errorf("cloudflare list records: %w", err)
	}
	if len(existing) == 0 {
		slog.Warn("cloudflare record not found for deletion, skipping", "upstream", u.name, "name", r.Name, "type", r.Type)
		return nil
	}
	for _, rec := range existing {
		slog.Debug("cloudflare deleting record", "upstream", u.name, "name", r.Name, "type", r.Type, "id", rec.ID)
		if err := u.api.DeleteDNSRecord(ctx, rc, rec.ID); err != nil {
			return err
		}
	}
	return nil
}
