package yaml

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/16bitowl/beacons/internal/envutil"
	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/validate"
	"github.com/16bitowl/beacons/pkg/source"
	"github.com/fsnotify/fsnotify"
	"github.com/goccy/go-yaml"
)

// reloadDebounce coalesces bursts of fsnotify events (e.g. an editor's
// write-rename save) so a reload fires once the file settles, not mid-write.
const reloadDebounce = 300 * time.Millisecond

// Options configures a YAML source adapter.
type Options struct {
	Name             string
	Glob             string
	Defaults         model.BaseRecord
	Strict           bool
	StrictValidation bool
}

// Source is the YAML file source adapter.
type Source struct {
	name             string
	glob             string
	defaults         model.BaseRecord
	strict           bool
	strictValidation bool
}

func New(opts Options) *Source {
	return &Source{
		name:             opts.Name,
		glob:             opts.Glob,
		defaults:         opts.Defaults,
		strict:           opts.Strict,
		strictValidation: opts.StrictValidation,
	}
}

func (s *Source) Name() string { return s.name }

func (s *Source) Run(ctx context.Context, ch chan<- source.Event) error {
	slog.Info("yaml source starting",
		"source", s.name,
		"glob", s.glob)

	s.loadAll(ch)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			slog.Debug("yaml watcher close failed",
				"source", s.name,
				"err", err)
		}
	}()

	// Watch the directories that match the glob pattern. If nothing matches
	// yet, fall back to the glob's static prefix so new files are still seen.
	dirs, err := globDirs(s.glob)
	if err != nil {
		slog.Error("yaml glob dirs failed",
			"source", s.name,
			"glob", s.glob,
			"err", err)
	}
	if len(dirs) == 0 {
		dirs = []string{staticGlobDir(s.glob)}
	}
	for _, d := range dirs {
		slog.Debug("yaml source watching directory",
			"source", s.name,
			"dir", d)
		if err := watcher.Add(d); err != nil {
			slog.Error("yaml watcher add failed",
				"source", s.name,
				"dir", d,
				"err", err)
		}
	}

	// debounce coalesces rapid events; debounceC fires once they settle.
	var debounce *time.Timer
	var debounceC <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			if debounce != nil {
				debounce.Stop()
			}
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			matched, _ := filepath.Match(s.glob, event.Name)
			if !matched {
				continue
			}
			slog.Debug("yaml file changed, scheduling reload",
				"source", s.name,
				"file", event.Name)
			if debounce == nil {
				debounce = time.NewTimer(reloadDebounce)
				debounceC = debounce.C
			} else {
				debounce.Reset(reloadDebounce)
			}
		case <-debounceC:
			slog.Info("yaml reloading after change",
				"source", s.name)
			s.loadAll(ch)
		case err := <-watcher.Errors:
			slog.Error("yaml watcher error",
				"source", s.name,
				"err", err)
		}
	}
}

// loadAll reads all files matching the glob and emits a single EventSync
// containing every record found. If a previously loaded file has been deleted
// or no longer matches, its records will be absent from the snapshot and the
// syncer will clean them up.
//
// A read failure (glob error or any file that fails to parse) must never be
// mistaken for "no records": emitting a partial or empty snapshot would make the
// syncer orphan-delete live records. On any such error loadAll logs and returns
// without emitting, leaving the last good state in place until a clean read.
func (s *Source) loadAll(ch chan<- source.Event) {
	files, err := filepath.Glob(s.glob)
	if err != nil {
		slog.Error("yaml glob failed, skipping reload to preserve live records",
			"source", s.name,
			"glob", s.glob,
			"err", err)
		return
	}

	if len(files) == 0 {
		slog.Warn("yaml source found no files matching glob",
			"source", s.name,
			"glob", s.glob)
		// Emit an empty sync so the syncer removes any previously stored records.
		ch <- source.Event{Type: source.EventSync, SourceName: s.name}
		return
	}

	slog.Debug("yaml source loading files",
		"source", s.name,
		"count", len(files))

	var allRecords []model.Record
	for _, f := range files {
		records, err := parseFile(s.name, f, s.defaults, s.strict, s.strictValidation)
		if err != nil {
			// A partial snapshot would orphan-delete this file's records; skip
			// the whole reload and keep the last good state.
			slog.Error("yaml parse failed, skipping reload to preserve live records",
				"source", s.name,
				"file", f,
				"err", err)
			return
		}
		if len(records) > 0 {
			slog.Info("yaml file loaded",
				"source", s.name,
				"file", f,
				"records", len(records))
			allRecords = append(allRecords, records...)
		}
	}

	ch <- source.Event{
		Type:       source.EventSync,
		SourceName: s.name,
		Records:    allRecords,
	}
}

// fileRecord mirrors the YAML schema for a single record entry.
// Schema matches the label schema: record-id → upstream → fields.
//
//	records:
//	  web:
//	    ttl: 300
//	    cloudflare:
//	      type: CNAME
//	      name: svc.domain.com
//	      value: domain.com
//	      ttl: 3600
type fileRecord struct {
	TTL      int                       `yaml:"ttl"`
	Priority int                       `yaml:"priority"`
	Comment  string                    `yaml:"comment"`
	Upstream map[string]upstreamFields `yaml:",inline"`
}

type upstreamFields struct {
	Type     string `yaml:"type"`
	Name     string `yaml:"name"`
	Value    string `yaml:"value"`
	TTL      string `yaml:"ttl"`
	Priority string `yaml:"priority"`
	Comment  string `yaml:"comment"`
}

type fileSchema struct {
	Defaults model.BaseRecord      `yaml:"defaults"`
	Records  map[string]fileRecord `yaml:"records"`
}

func parseFile(sourceName, path string, globalDefaults model.BaseRecord, strict bool, strictValidation bool) ([]model.Record, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var expanded string
	if strict {
		expanded, err = envutil.Expand(string(raw))
		if err != nil {
			return nil, err
		}
	} else {
		expanded = envutil.ExpandLenient(string(raw))
	}

	var f fileSchema
	if err := yaml.Unmarshal([]byte(expanded), &f); err != nil {
		return nil, err
	}

	// Merge: global defaults → file defaults.
	base := mergeBase(globalDefaults, f.Defaults)

	var records []model.Record
	for recordID, entry := range f.Records {
		// Per-record base (inherits merged base).
		recordBase := base
		if entry.TTL != 0 {
			recordBase.TTL = entry.TTL
		}
		if entry.Priority != 0 {
			recordBase.Priority = entry.Priority
		}
		if entry.Comment != "" {
			recordBase.Comment = entry.Comment
		}

		for upstreamName, fields := range entry.Upstream {
			r := model.Record{
				BaseRecord: recordBase,
				ID:         recordID,
				SourceID:   path,
				SourceName: sourceName,
				Upstream:   upstreamName,
				Type:       model.RecordType(strings.ToUpper(fields.Type)),
				Name:       fields.Name,
				Value:      fields.Value,
			}
			if fields.TTL != "" {
				if n, err := strconv.Atoi(fields.TTL); err == nil {
					r.TTL = n
				}
			}
			if fields.Priority != "" {
				if n, err := strconv.Atoi(fields.Priority); err == nil {
					r.Priority = n
				}
			}
			if fields.Comment != "" {
				r.Comment = fields.Comment
			}

			recPath := fmt.Sprintf("yaml://%s/records/%s/%s", path, recordID, upstreamName)
			if err := validate.StructWithPrefix(&r, recPath); err != nil {
				if strictValidation {
					return nil, err
				}
				slog.Warn("invalid yaml record, skipping",
					"path", recPath,
					"errors", err.Error())
				continue
			}

			records = append(records, r)
		}
	}
	return records, nil
}

func mergeBase(base, override model.BaseRecord) model.BaseRecord {
	if override.TTL != 0 {
		base.TTL = override.TTL
	}
	if override.Priority != 0 {
		base.Priority = override.Priority
	}
	if override.Comment != "" {
		base.Comment = override.Comment
	}
	return base
}

// staticGlobDir returns the longest directory prefix of glob that contains
// no pattern metacharacters, so it can be watched even before any file matches.
func staticGlobDir(glob string) string {
	dir := filepath.Dir(glob)
	for hasMeta(dir) {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return dir
}

func hasMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// globDirs returns unique directories from a glob pattern's currently matched files.
func globDirs(glob string) ([]string, error) {
	files, err := filepath.Glob(glob)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var dirs []string
	for _, f := range files {
		d := filepath.Dir(f)
		if _, ok := seen[d]; !ok {
			seen[d] = struct{}{}
			dirs = append(dirs, d)
		}
	}
	return dirs, nil
}
