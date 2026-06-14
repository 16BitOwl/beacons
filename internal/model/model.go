package model

import "time"

// RecordType represents a DNS record type.
type RecordType string

const (
	RecordTypeA     RecordType = "A"
	RecordTypeAAAA  RecordType = "AAAA"
	RecordTypeCNAME RecordType = "CNAME"
	RecordTypeTXT   RecordType = "TXT"
	RecordTypeMX    RecordType = "MX"
	RecordTypeSRV   RecordType = "SRV"
	RecordTypeNS    RecordType = "NS"
	RecordTypeCAA   RecordType = "CAA"
)

// RecordStatus represents the sync status of a record against its upstream.
type RecordStatus string

const (
	RecordStatusPending RecordStatus = "pending"
	RecordStatusSynced  RecordStatus = "synced"
	RecordStatusFailed  RecordStatus = "failed"
)

// BaseRecord holds fields common to all DNS records and shared defaults.
type BaseRecord struct {
	TTL      int    `yaml:"ttl"      json:"ttl"`
	Priority int    `yaml:"priority" json:"priority"` // used by MX, SRV
	Comment  string `yaml:"comment"  json:"comment"`
}

// Record is a fully resolved DNS record destined for a specific upstream instance.
type Record struct {
	BaseRecord `yaml:",inline"`

	// ID is the record identifier from the label/yaml (e.g. "web", "api").
	ID string `json:"id"`

	// SourceID is the originating source item identifier (container ID, file path, etc.).
	SourceID string `json:"source_id"`

	// SourceName is the name of the source adapter instance that produced this record.
	SourceName string `json:"source_name"`

	// Upstream is the named upstream instance this record targets.
	Upstream string `json:"upstream"`

	Type  RecordType `yaml:"type"  json:"type"`
	Name  string     `yaml:"name"  json:"name"`
	Value string     `yaml:"value" json:"value"`

	// Sync status — set by the Syncer after each upstream operation.
	Status    RecordStatus `json:"status,omitempty"`
	SyncedAt  time.Time    `json:"synced_at,omitempty"`
	SyncError string       `json:"sync_error,omitempty"`
}

// UpstreamConfig holds the configuration for a named upstream adapter instance.
type UpstreamConfig struct {
	Type string `yaml:"type"`

	// Cloudflare
	APIToken string `yaml:"api_token"`
	ZoneID   string `yaml:"zone_id"`

	// PiHole
	URL      string `yaml:"url"`
	Password string `yaml:"password"`
}

// SourceConfig holds the configuration for a named source adapter instance.
type SourceConfig struct {
	Type string `yaml:"type"`

	// Docker
	Host string `yaml:"host"` // e.g. unix:///var/run/docker.sock

	// YAML
	Glob string `yaml:"glob"` // e.g. /config/*.yaml
}
