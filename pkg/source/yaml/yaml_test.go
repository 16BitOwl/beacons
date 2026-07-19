package yaml

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/16bitowl/beacons/internal/model"
)

// writeYAML writes content to a temp file and returns its path.
func writeYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.yaml")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	_ = f.Close()
	return f.Name()
}

// ---------------------------------------------------------------------------
// parseFile
// ---------------------------------------------------------------------------

func TestParseFile_SingleRecord(t *testing.T) {
	path := writeYAML(t, `
records:
  web:
    cloudflare:
      type: A
      name: svc.example.com
      value: 1.2.3.4
`)
	records, err := parseFile("src", path, model.BaseRecord{}, false, false)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len = %d, want 1", len(records))
	}
	r := records[0]
	if r.ID != "web" {
		t.Errorf("ID = %q, want web", r.ID)
	}
	if r.Upstream != "cloudflare" {
		t.Errorf("Upstream = %q, want cloudflare", r.Upstream)
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
	if r.SourceID != path {
		t.Errorf("SourceID = %q, want %q", r.SourceID, path)
	}
	if r.SourceName != "src" {
		t.Errorf("SourceName = %q, want src", r.SourceName)
	}
}

func TestParseFile_RecordTypeUppercased(t *testing.T) {
	path := writeYAML(t, `
records:
  alias:
    cf:
      type: cname
      name: alias.example.com
      value: target.example.com
`)
	records, err := parseFile("src", path, model.BaseRecord{}, false, false)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if records[0].Type != model.RecordTypeCNAME {
		t.Errorf("Type = %q, want CNAME", records[0].Type)
	}
}

func TestParseFile_MultipleRecordsAndUpstreams(t *testing.T) {
	path := writeYAML(t, `
records:
  web:
    cf:
      type: A
      name: web.example.com
      value: 1.2.3.4
    pihole:
      type: A
      name: web.example.com
      value: 1.2.3.4
  api:
    cf:
      type: A
      name: api.example.com
      value: 5.6.7.8
`)
	records, err := parseFile("src", path, model.BaseRecord{}, false, false)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if len(records) != 3 {
		t.Errorf("len = %d, want 3", len(records))
	}
}

func TestParseFile_GlobalDefaultsTTLApplied(t *testing.T) {
	path := writeYAML(t, `
records:
  web:
    cf:
      type: A
      name: web.example.com
      value: 1.2.3.4
`)
	records, err := parseFile("src", path, model.BaseRecord{TTL: 300}, false, false)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if records[0].TTL != 300 {
		t.Errorf("TTL = %d, want 300 (from globalDefaults)", records[0].TTL)
	}
}

func TestParseFile_FileDefaultsOverrideGlobalDefaults(t *testing.T) {
	path := writeYAML(t, `
defaults:
  ttl: 600
records:
  web:
    cf:
      type: A
      name: web.example.com
      value: 1.2.3.4
`)
	records, err := parseFile("src", path, model.BaseRecord{TTL: 300}, false, false)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if records[0].TTL != 600 {
		t.Errorf("TTL = %d, want 600 (file defaults should override global)", records[0].TTL)
	}
}

func TestParseFile_PerFieldTTLOverride(t *testing.T) {
	path := writeYAML(t, `
records:
  web:
    cf:
      type: A
      name: web.example.com
      value: 1.2.3.4
      ttl: "7200"
`)
	records, err := parseFile("src", path, model.BaseRecord{TTL: 300}, false, false)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if records[0].TTL != 7200 {
		t.Errorf("TTL = %d, want 7200 (field-level override)", records[0].TTL)
	}
}

func TestParseFile_PriorityField(t *testing.T) {
	path := writeYAML(t, `
records:
  mail:
    cf:
      type: MX
      name: example.com
      value: mail.example.com
      priority: "10"
`)
	records, err := parseFile("src", path, model.BaseRecord{}, false, false)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if records[0].Priority != 10 {
		t.Errorf("Priority = %d, want 10", records[0].Priority)
	}
}

func TestParseFile_CommentField(t *testing.T) {
	path := writeYAML(t, `
records:
  web:
    cf:
      type: A
      name: web.example.com
      value: 1.2.3.4
      comment: "managed by beacons"
`)
	records, err := parseFile("src", path, model.BaseRecord{}, false, false)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if records[0].Comment != "managed by beacons" {
		t.Errorf("Comment = %q, want %q", records[0].Comment, "managed by beacons")
	}
}

func TestParseFile_InvalidRecordSkippedInLenientMode(t *testing.T) {
	// Missing required fields (no name, no value, no type) — should be skipped.
	path := writeYAML(t, `
records:
  bad:
    cf:
      type: ""
      name: ""
      value: ""
  good:
    cf:
      type: A
      name: good.example.com
      value: 1.2.3.4
`)
	records, err := parseFile("src", path, model.BaseRecord{}, false, false)
	if err != nil {
		t.Fatalf("unexpected error in lenient mode: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 valid record, got %d", len(records))
	}
	if records[0].ID != "good" {
		t.Errorf("expected good record, got %q", records[0].ID)
	}
}

func TestParseFile_InvalidRecordFailsInStrictMode(t *testing.T) {
	path := writeYAML(t, `
records:
  bad:
    cf:
      type: ""
      name: ""
      value: ""
`)
	_, err := parseFile("src", path, model.BaseRecord{}, false, true)
	if err == nil {
		t.Error("expected error in strict validation mode, got nil")
	}
}

func TestParseFile_EnvExpansionLenient(t *testing.T) {
	t.Setenv("YAML_TEST_TOKEN", "tok-xyz")
	path := writeYAML(t, `
records:
  web:
    cf:
      type: A
      name: web.example.com
      value: ${YAML_TEST_TOKEN}
`)
	records, err := parseFile("src", path, model.BaseRecord{}, false, false)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if records[0].Value != "tok-xyz" {
		t.Errorf("Value = %q, want tok-xyz (env expanded)", records[0].Value)
	}
}

func TestParseFile_EnvExpansionStrict_MissingVarErrors(t *testing.T) {
	path := writeYAML(t, `
records:
  web:
    cf:
      type: A
      name: web.example.com
      value: ${YAML_TEST_STRICT_MISSING_VAR_ZZZ}
`)
	_, err := parseFile("src", path, model.BaseRecord{}, true, false)
	if err == nil {
		t.Error("expected error for missing env var in strict mode")
	}
}

func TestParseFile_EnvExpansionLenient_MissingVarBecomesEmpty(t *testing.T) {
	// Missing var in lenient mode: value becomes empty → validation fails → record skipped.
	path := writeYAML(t, `
records:
  web:
    cf:
      type: A
      name: web.example.com
      value: ${YAML_LENIENT_MISSING_VAR_ZZZ}
`)
	records, err := parseFile("src", path, model.BaseRecord{}, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Value is empty → validation fails → record skipped in lenient mode.
	if len(records) != 0 {
		t.Errorf("expected 0 records (empty value fails validation), got %d", len(records))
	}
}

func TestParseFile_EmptyFile(t *testing.T) {
	path := writeYAML(t, "")
	records, err := parseFile("src", path, model.BaseRecord{}, false, false)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records from empty file, got %d", len(records))
	}
}

func TestParseFile_NoRecordsSection(t *testing.T) {
	path := writeYAML(t, `
defaults:
  ttl: 300
`)
	records, err := parseFile("src", path, model.BaseRecord{}, false, false)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

// ---------------------------------------------------------------------------
// mergeBase
// ---------------------------------------------------------------------------

func TestMergeBase_OverrideTTL(t *testing.T) {
	base := model.BaseRecord{TTL: 300, Priority: 10, Comment: "base"}
	override := model.BaseRecord{TTL: 600}
	got := mergeBase(base, override)
	if got.TTL != 600 {
		t.Errorf("TTL = %d, want 600", got.TTL)
	}
	if got.Priority != 10 {
		t.Errorf("Priority = %d, want 10 (unchanged)", got.Priority)
	}
	if got.Comment != "base" {
		t.Errorf("Comment = %q, want base (unchanged)", got.Comment)
	}
}

func TestMergeBase_OverridePriority(t *testing.T) {
	base := model.BaseRecord{TTL: 300, Priority: 5}
	override := model.BaseRecord{Priority: 20}
	got := mergeBase(base, override)
	if got.Priority != 20 {
		t.Errorf("Priority = %d, want 20", got.Priority)
	}
	if got.TTL != 300 {
		t.Errorf("TTL = %d, want 300 (unchanged)", got.TTL)
	}
}

func TestMergeBase_OverrideComment(t *testing.T) {
	base := model.BaseRecord{Comment: "original"}
	override := model.BaseRecord{Comment: "overridden"}
	got := mergeBase(base, override)
	if got.Comment != "overridden" {
		t.Errorf("Comment = %q, want overridden", got.Comment)
	}
}

func TestMergeBase_ZeroOverrideKeepsBase(t *testing.T) {
	base := model.BaseRecord{TTL: 300, Priority: 10, Comment: "keep"}
	override := model.BaseRecord{} // all zero — should not override
	got := mergeBase(base, override)
	if got.TTL != 300 {
		t.Errorf("TTL = %d, want 300", got.TTL)
	}
	if got.Priority != 10 {
		t.Errorf("Priority = %d, want 10", got.Priority)
	}
	if got.Comment != "keep" {
		t.Errorf("Comment = %q, want keep", got.Comment)
	}
}

func TestMergeBase_AllOverridden(t *testing.T) {
	base := model.BaseRecord{TTL: 300, Priority: 10, Comment: "old"}
	override := model.BaseRecord{TTL: 600, Priority: 20, Comment: "new"}
	got := mergeBase(base, override)
	if got.TTL != 600 || got.Priority != 20 || got.Comment != "new" {
		t.Errorf("got %+v, want TTL=600 Priority=20 Comment=new", got)
	}
}

// ---------------------------------------------------------------------------
// globDirs
// ---------------------------------------------------------------------------

func TestGlobDirs_SingleDir(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.yaml", "b.yaml"} {
		f, _ := os.Create(filepath.Join(dir, name))
		_ = f.Close()
	}
	glob := filepath.Join(dir, "*.yaml")
	dirs, err := globDirs(glob)
	if err != nil {
		t.Fatalf("globDirs: %v", err)
	}
	if len(dirs) != 1 {
		t.Errorf("dirs len = %d, want 1 (unique dir)", len(dirs))
	}
	if dirs[0] != dir {
		t.Errorf("dir = %q, want %q", dirs[0], dir)
	}
}

func TestGlobDirs_MultipleDirs(t *testing.T) {
	root := t.TempDir()
	dir1 := filepath.Join(root, "d1")
	dir2 := filepath.Join(root, "d2")
	_ = os.MkdirAll(dir1, 0o750)
	_ = os.MkdirAll(dir2, 0o750)

	f1, _ := os.Create(filepath.Join(dir1, "x.yaml"))
	_ = f1.Close()
	f2, _ := os.Create(filepath.Join(dir2, "y.yaml"))
	_ = f2.Close()

	dirs, err := globDirs(filepath.Join(root, "*", "*.yaml"))
	if err != nil {
		t.Fatalf("globDirs: %v", err)
	}
	if len(dirs) != 2 {
		t.Errorf("dirs len = %d, want 2", len(dirs))
	}
}

func TestGlobDirs_NoMatchReturnsEmpty(t *testing.T) {
	dirs, err := globDirs(filepath.Join(t.TempDir(), "*.yaml"))
	if err != nil {
		t.Fatalf("globDirs: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("expected empty dirs, got %d", len(dirs))
	}
}

// ---------------------------------------------------------------------------
// Snapshot: empty-match disambiguation
// ---------------------------------------------------------------------------

func TestSnapshot_EmptyDirIsLegitEmpty(t *testing.T) {
	// Present directory, no matching files: a legitimate "nothing desired".
	s := New(Options{Name: "src", Glob: filepath.Join(t.TempDir(), "*.yaml")})
	recs, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: unexpected error %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected 0 records, got %d", len(recs))
	}
}

func TestSnapshot_MissingDirIsError(t *testing.T) {
	// The base directory does not exist (vanished mount / unmounted volume):
	// must be a read failure so the caller keeps the last good state, not an
	// empty set that would delete every record the source owns.
	glob := filepath.Join(t.TempDir(), "nonexistent", "*.yaml")
	s := New(Options{Name: "src", Glob: glob})
	if _, err := s.Snapshot(context.Background()); err == nil {
		t.Fatal("expected error when base dir is missing, got nil")
	}
}

// ---------------------------------------------------------------------------
// staticGlobDir / hasMeta
// ---------------------------------------------------------------------------

func TestStaticGlobDir(t *testing.T) {
	cases := []struct {
		name string
		glob string
		want string
	}{
		{"single wildcard component", "/etc/beacons/*.yaml", "/etc/beacons"},
		{"wildcard directory component", "/etc/beacons/*/site.yaml", "/etc/beacons"},
		{"multiple wildcard components", "/etc/beacons/*/*.yaml", "/etc/beacons"},
		{"no metacharacters", "/etc/beacons/site.yaml", "/etc/beacons"},
		{"relative glob, no dir", "*.yaml", "."},
		{"character class", "/data/[ab]/*.yaml", "/data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := staticGlobDir(tc.glob); got != tc.want {
				t.Errorf("staticGlobDir(%q) = %q, want %q", tc.glob, got, tc.want)
			}
		})
	}
}

func TestHasMeta(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"/etc/beacons", false},
		{"/etc/beacons/*", true},
		{"/etc/beacons/?", true},
		{"/etc/beacons/[a]", true},
	}
	for _, tc := range cases {
		if got := hasMeta(tc.s); got != tc.want {
			t.Errorf("hasMeta(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Notify: watcher fallback when nothing matches at startup
// ---------------------------------------------------------------------------

func TestNotify_WatchesForNewFilesWhenNoneMatchAtStartup(t *testing.T) {
	dir := t.TempDir()
	glob := filepath.Join(dir, "*.yaml")
	s := New(Options{Name: "src", Glob: glob})

	ch := make(chan struct{}, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Notify(ctx, ch)

	content := `
records:
  web:
    cf:
      type: A
      name: web.example.com
      value: 1.2.3.4
`
	path := filepath.Join(dir, "new.yaml")

	// Write repeatedly until the watcher (added despite the empty initial
	// match set) signals the new file; guards against a race with watcher.Add.
	// Each attempt waits past the reload debounce so a picked-up write can fire.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		select {
		case <-ch:
			recs, err := s.Snapshot(ctx)
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if len(recs) == 1 {
				return
			}
		case <-time.After(reloadDebounce + 200*time.Millisecond):
		}
	}
	t.Fatal("expected new file to be picked up by watcher after empty startup")
}
