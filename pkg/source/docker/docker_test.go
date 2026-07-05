package docker

import (
	"testing"

	"github.com/16bitowl/beacons/internal/model"
)

// ---------------------------------------------------------------------------
// parseLabels
// ---------------------------------------------------------------------------

func TestParseLabels_NotEnabled_ReturnsNil(t *testing.T) {
	labels := map[string]string{
		"dns.web.cf.type":  "A",
		"dns.web.cf.name":  "svc.example.com",
		"dns.web.cf.value": "1.2.3.4",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records when dns.enable != true, got %d", len(records))
	}
}

func TestParseLabels_EnableFalse_ReturnsNil(t *testing.T) {
	labels := map[string]string{
		"dns.enable":       "false",
		"dns.web.cf.type":  "A",
		"dns.web.cf.name":  "svc.example.com",
		"dns.web.cf.value": "1.2.3.4",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestParseLabels_SingleRecord(t *testing.T) {
	labels := map[string]string{
		"dns.enable":       "true",
		"dns.web.cf.type":  "A",
		"dns.web.cf.name":  "svc.example.com",
		"dns.web.cf.value": "1.2.3.4",
	}
	records, err := parseLabels("src", "containerABC", labels, model.BaseRecord{}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len = %d, want 1", len(records))
	}
	r := records[0]
	if r.ID != "web" {
		t.Errorf("ID = %q, want web", r.ID)
	}
	if r.Upstream != "cf" {
		t.Errorf("Upstream = %q, want cf", r.Upstream)
	}
	if r.Type != model.RecordTypeA {
		t.Errorf("Type = %q, want A", r.Type)
	}
	if r.Name != "svc.example.com" {
		t.Errorf("Name = %q, want svc.example.com", r.Name)
	}
	if r.Value != "1.2.3.4" {
		t.Errorf("Value = %q, want 1.2.3.4", r.Value)
	}
	if r.SourceID != "containerABC" {
		t.Errorf("SourceID = %q, want containerABC", r.SourceID)
	}
	if r.SourceName != "src" {
		t.Errorf("SourceName = %q, want src", r.SourceName)
	}
}

func TestParseLabels_TypeUppercased(t *testing.T) {
	labels := map[string]string{
		"dns.enable":         "true",
		"dns.alias.cf.type":  "cname",
		"dns.alias.cf.name":  "alias.example.com",
		"dns.alias.cf.value": "target.example.com",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if records[0].Type != model.RecordTypeCNAME {
		t.Errorf("Type = %q, want CNAME", records[0].Type)
	}
}

func TestParseLabels_MultipleRecordsAndUpstreams(t *testing.T) {
	labels := map[string]string{
		"dns.enable":           "true",
		"dns.web.cf.type":      "A",
		"dns.web.cf.name":      "web.example.com",
		"dns.web.cf.value":     "1.2.3.4",
		"dns.web.pihole.type":  "A",
		"dns.web.pihole.name":  "web.example.com",
		"dns.web.pihole.value": "1.2.3.4",
		"dns.api.cf.type":      "A",
		"dns.api.cf.name":      "api.example.com",
		"dns.api.cf.value":     "5.6.7.8",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if len(records) != 3 {
		t.Errorf("len = %d, want 3", len(records))
	}
}

func TestParseLabels_BaseTTLFromDefaults(t *testing.T) {
	labels := map[string]string{
		"dns.enable":       "true",
		"dns.web.cf.type":  "A",
		"dns.web.cf.name":  "web.example.com",
		"dns.web.cf.value": "1.2.3.4",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{TTL: 300}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if records[0].TTL != 300 {
		t.Errorf("TTL = %d, want 300 (from defaults)", records[0].TTL)
	}
}

func TestParseLabels_BaseTTLOverriddenByLabel(t *testing.T) {
	labels := map[string]string{
		"dns.enable":       "true",
		"dns.ttl":          "600",
		"dns.web.cf.type":  "A",
		"dns.web.cf.name":  "web.example.com",
		"dns.web.cf.value": "1.2.3.4",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{TTL: 300}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if records[0].TTL != 600 {
		t.Errorf("TTL = %d, want 600 (dns.ttl override)", records[0].TTL)
	}
}

func TestParseLabels_PerRecordTTLOverride(t *testing.T) {
	labels := map[string]string{
		"dns.enable":         "true",
		"dns.web.cf.type":    "A",
		"dns.web.cf.name":    "web.example.com",
		"dns.web.cf.value":   "1.2.3.4",
		"dns.web.cf.ttl":     "7200",
		"dns.web.cf.comment": "high ttl",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{TTL: 300}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if records[0].TTL != 7200 {
		t.Errorf("TTL = %d, want 7200", records[0].TTL)
	}
	if records[0].Comment != "high ttl" {
		t.Errorf("Comment = %q, want %q", records[0].Comment, "high ttl")
	}
}

func TestParseLabels_PriorityLabel(t *testing.T) {
	labels := map[string]string{
		"dns.enable":           "true",
		"dns.mail.cf.type":     "MX",
		"dns.mail.cf.name":     "example.com",
		"dns.mail.cf.value":    "mail.example.com",
		"dns.mail.cf.priority": "10",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if records[0].Priority != 10 {
		t.Errorf("Priority = %d, want 10", records[0].Priority)
	}
}

func TestParseLabels_InvalidRecordSkippedInLenientMode(t *testing.T) {
	labels := map[string]string{
		"dns.enable":        "true",
		"dns.bad.cf.type":   "",
		"dns.bad.cf.name":   "",
		"dns.bad.cf.value":  "",
		"dns.good.cf.type":  "A",
		"dns.good.cf.name":  "good.example.com",
		"dns.good.cf.value": "1.2.3.4",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{}, false)
	if err != nil {
		t.Fatalf("unexpected error in lenient mode: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 valid record, got %d", len(records))
	}
}

func TestParseLabels_InvalidRecordErrorsInStrictMode(t *testing.T) {
	labels := map[string]string{
		"dns.enable":       "true",
		"dns.bad.cf.type":  "",
		"dns.bad.cf.name":  "",
		"dns.bad.cf.value": "",
	}
	_, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{}, true)
	if err == nil {
		t.Error("expected error in strict validation mode")
	}
}

func TestParseLabels_LabelsWithoutDNSPrefixIgnored(t *testing.T) {
	labels := map[string]string{
		"dns.enable":           "true",
		"com.docker.compose.x": "irrelevant",
		"traefik.enable":       "true",
		"dns.web.cf.type":      "A",
		"dns.web.cf.name":      "web.example.com",
		"dns.web.cf.value":     "1.2.3.4",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 record, got %d", len(records))
	}
}

func TestParseLabels_InvalidBaseTTLFallsBackToDefault(t *testing.T) {
	labels := map[string]string{
		"dns.enable":       "true",
		"dns.ttl":          "notanumber",
		"dns.web.cf.type":  "A",
		"dns.web.cf.name":  "web.example.com",
		"dns.web.cf.value": "1.2.3.4",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{TTL: 300}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if records[0].TTL != 300 {
		t.Errorf("TTL = %d, want 300 (invalid dns.ttl should fall back to default)", records[0].TTL)
	}
}

func TestParseLabels_InvalidPerRecordTTLFallsBackToBase(t *testing.T) {
	labels := map[string]string{
		"dns.enable":       "true",
		"dns.web.cf.type":  "A",
		"dns.web.cf.name":  "web.example.com",
		"dns.web.cf.value": "1.2.3.4",
		"dns.web.cf.ttl":   "notanumber",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{TTL: 300}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if records[0].TTL != 300 {
		t.Errorf("TTL = %d, want 300 (invalid per-record ttl should fall back to base)", records[0].TTL)
	}
}

func TestParseLabels_InvalidPriorityFallsBackToZero(t *testing.T) {
	labels := map[string]string{
		"dns.enable":           "true",
		"dns.mail.cf.type":     "MX",
		"dns.mail.cf.name":     "example.com",
		"dns.mail.cf.value":    "mail.example.com",
		"dns.mail.cf.priority": "notanumber",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if records[0].Priority != 0 {
		t.Errorf("Priority = %d, want 0 (invalid priority should fall back to zero)", records[0].Priority)
	}
}

func TestParseLabels_EnabledButNoRecordLabels(t *testing.T) {
	labels := map[string]string{
		"dns.enable": "true",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestParseLabels_MalformedLabelIgnored(t *testing.T) {
	// Labels with fewer than 3 parts after the "dns." prefix are silently ignored.
	labels := map[string]string{
		"dns.enable":       "true",
		"dns.tooshort":     "value", // only 1 part — ignored
		"dns.web.cf.type":  "A",
		"dns.web.cf.name":  "web.example.com",
		"dns.web.cf.value": "1.2.3.4",
	}
	records, err := parseLabels("src", "aabbccddeeff", labels, model.BaseRecord{}, false)
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 record, got %d", len(records))
	}
}

// ---------------------------------------------------------------------------
// uniqueSourceIDs
// ---------------------------------------------------------------------------

func TestUniqueSourceIDs_Empty(t *testing.T) {
	if n := uniqueSourceIDs(nil); n != 0 {
		t.Errorf("got %d, want 0", n)
	}
}

func TestUniqueSourceIDs_AllSame(t *testing.T) {
	records := []model.Record{
		{SourceID: "abc"},
		{SourceID: "abc"},
		{SourceID: "abc"},
	}
	if n := uniqueSourceIDs(records); n != 1 {
		t.Errorf("got %d, want 1", n)
	}
}

func TestUniqueSourceIDs_AllDifferent(t *testing.T) {
	records := []model.Record{
		{SourceID: "a"},
		{SourceID: "b"},
		{SourceID: "c"},
	}
	if n := uniqueSourceIDs(records); n != 3 {
		t.Errorf("got %d, want 3", n)
	}
}

func TestUniqueSourceIDs_Mixed(t *testing.T) {
	records := []model.Record{
		{SourceID: "a"},
		{SourceID: "b"},
		{SourceID: "a"},
		{SourceID: "c"},
		{SourceID: "b"},
	}
	if n := uniqueSourceIDs(records); n != 3 {
		t.Errorf("got %d, want 3", n)
	}
}

// ---------------------------------------------------------------------------
// shortID
// ---------------------------------------------------------------------------

func TestShortID_LongID_TruncatedTo12(t *testing.T) {
	id := "abcdefghijklmnopqrstuvwxyz"
	got := shortID(id)
	if len(got) != 12 {
		t.Errorf("len = %d, want 12", len(got))
	}
	if got != "abcdefghijkl" {
		t.Errorf("got %q, want abcdefghijkl", got)
	}
}

func TestShortID_ExactlyTwelve_Unchanged(t *testing.T) {
	id := "123456789012"
	if got := shortID(id); got != id {
		t.Errorf("got %q, want %q", got, id)
	}
}

func TestShortID_ShorterThanTwelve_Unchanged(t *testing.T) {
	id := "short"
	if got := shortID(id); got != id {
		t.Errorf("got %q, want %q", got, id)
	}
}

func TestShortID_Empty_Unchanged(t *testing.T) {
	if got := shortID(""); got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}
