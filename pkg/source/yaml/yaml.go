package yaml

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/16bitowl/beacons/internal/envutil"
	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/source"
	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// Source is the YAML file source adapter.
type Source struct {
	name     string
	glob     string
	defaults model.BaseRecord
	strict   bool
}

func New(name string, glob string, defaults model.BaseRecord, strict bool) *Source {
	return &Source{name: name, glob: glob, defaults: defaults, strict: strict}
}

func (s *Source) Name() string { return s.name }

func (s *Source) Run(ctx context.Context, ch chan<- source.Event) error {
	slog.Info("yaml source starting", "source", s.name, "glob", s.glob)

	// Initial load.
	s.loadAll(ch)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Watch the directories that match the glob pattern.
	dirs, _ := globDirs(s.glob)
	for _, d := range dirs {
		slog.Debug("yaml source watching directory",
			"source", s.name,
			"dir", d)
		_ = watcher.Add(d)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			matched, _ := filepath.Match(s.glob, event.Name)
			if matched {
				slog.Info("yaml file changed, reloading",
					"source", s.name,
					"file", event.Name)
				s.loadAll(ch)
			}
		case err := <-watcher.Errors:
			slog.Error("yaml watcher error",
				"source", s.name,
				"err", err)
		}
	}
}

func (s *Source) loadAll(ch chan<- source.Event) {
	files, err := filepath.Glob(s.glob)
	if err != nil || len(files) == 0 {
		slog.Warn("yaml source found no files matching glob",
			"source", s.name,
			"glob", s.glob)
		return
	}
	slog.Debug("yaml source loading files",
		"source", s.name,
		"count", len(files))
	for _, f := range files {
		records, err := parseFile(f, s.defaults, s.strict)
		if err != nil {
			slog.Error("yaml parse failed",
				"file", f,
				"err", err)
			continue
		}
		if len(records) > 0 {
			slog.Info("yaml file loaded",
				"source", s.name,
				"file", f,
				"records", len(records))
			ch <- source.Event{Type: source.EventUpsert, SourceID: f, Records: records}
		}
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

func parseFile(path string, globalDefaults model.BaseRecord, strict bool) ([]model.Record, error) {
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

// globDirs returns unique directories from a glob pattern.
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
