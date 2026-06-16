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
	RecordStatusPending       RecordStatus = "pending"
	RecordStatusSynced        RecordStatus = "synced"
	RecordStatusFailed        RecordStatus = "failed"
	RecordStatusPendingDelete RecordStatus = "pending_delete"
)

// BaseRecord holds fields common to all DNS records and shared defaults.
type BaseRecord struct {
	TTL      int    `yaml:"ttl"      json:"ttl"`
	Priority int    `yaml:"priority" json:"priority" validate:"min=0,max=65535"` // used by MX, SRV; capped to uint16 range
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
	Upstream string `json:"upstream" validate:"required"`

	Type  RecordType `yaml:"type"  json:"type"  validate:"required,oneof=A AAAA CNAME TXT MX SRV NS CAA"`
	Name  string     `yaml:"name"  json:"name"  validate:"required"`
	Value string     `yaml:"value" json:"value" validate:"required"`

	// Sync status — set by the Syncer after each upstream operation.
	Status    RecordStatus `json:"status,omitempty"`
	SyncedAt  time.Time    `json:"synced_at,omitempty"`
	SyncError string       `json:"sync_error,omitempty"`

	// Failures counts how many consecutive upstream operation attempts have failed
	// for this record.
	Failures int `json:"failures,omitempty"`
}

// UpstreamHTTPConfig holds HTTP client tuning for an upstream adapter.
// Zero values fall back to the transport defaults
type UpstreamHTTPConfig struct {
	RetryMaxAttempts int `yaml:"retry_max_attempts"  validate:"min=0"`
	RetryBaseDelayMs int `yaml:"retry_base_delay_ms" validate:"min=0"`
	RetryMaxDelayMs  int `yaml:"retry_max_delay_ms"  validate:"min=0"`
}

// UpstreamConfig holds the configuration for a named upstream adapter instance.
type UpstreamConfig struct {
	Type string `yaml:"type" validate:"required,oneof=cloudflare pihole"`

	// Cloudflare
	APIToken string `yaml:"api_token" validate:"required_if=Type cloudflare"`
	ZoneID   string `yaml:"zone_id"   validate:"required_if=Type cloudflare"`

	// PiHole
	URL      string `yaml:"url"      validate:"required_if=Type pihole,omitempty,url"`
	Password string `yaml:"password"`

	// HTTP contains HTTP client tuning shared across upstream types.
	HTTP UpstreamHTTPConfig `yaml:"http"`
}

// SourceConfig holds the configuration for a named source adapter instance.
type SourceConfig struct {
	Type string `yaml:"type" validate:"required,oneof=docker yaml"`

	// Docker
	Host string `yaml:"host"` // e.g. unix:///var/run/docker.sock

	// YAML
	Glob string `yaml:"glob" validate:"required_if=Type yaml"` // e.g. /config/*.yaml
}
