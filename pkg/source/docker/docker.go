package docker

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/validate"
	dockerevents "github.com/moby/moby/api/types/events"
	dockerclient "github.com/moby/moby/client"
)

const labelPrefix = "dns."

// eventResubscribeDelay is how long to wait before reopening a dead event stream.
const eventResubscribeDelay = 5 * time.Second

// Options configures a Docker source adapter.
type Options struct {
	Name             string
	Host             string
	Defaults         model.BaseRecord
	PollInterval     time.Duration
	UseEvents        bool
	DebounceDelay    time.Duration
	StrictValidation bool
}

// Source is the Docker source adapter.
type Source struct {
	name             string
	client           *dockerclient.Client
	defaults         model.BaseRecord
	pollInterval     time.Duration
	useEvents        bool
	debounceDelay    time.Duration
	strictValidation bool
}

// New creates a new Docker source adapter.
func New(opts Options) (*Source, error) {
	// FromEnv honors DOCKER_HOST et al; an explicit host in config wins.
	clientOpts := []dockerclient.Opt{dockerclient.FromEnv}
	if opts.Host != "" {
		clientOpts = append(clientOpts, dockerclient.WithHost(opts.Host))
	}
	c, err := dockerclient.New(clientOpts...)
	if err != nil {
		return nil, err
	}
	return &Source{
		name:             opts.Name,
		client:           c,
		defaults:         opts.Defaults,
		pollInterval:     opts.PollInterval,
		useEvents:        opts.UseEvents,
		debounceDelay:    opts.DebounceDelay,
		strictValidation: opts.StrictValidation,
	}, nil
}

func (s *Source) Name() string { return s.name }

// Snapshot lists all running containers and returns every DNS record found. It
// returns a non-nil error on a Docker API failure so the reconciler keeps the
// last good state; a successful scan that finds no records returns a nil slice
// and a nil error. Per-container label validation failures are logged and
// skipped, not propagated, so one bad container never voids the whole snapshot.
func (s *Source) Snapshot(ctx context.Context) ([]model.Record, error) {
	listed, err := s.client.ContainerList(ctx, dockerclient.ContainerListOptions{})
	if err != nil {
		return nil, err
	}

	var records []model.Record
	for _, c := range listed.Items {
		recs, err := parseLabels(s.name, c.ID, c.Labels, s.defaults, s.strictValidation)
		if err != nil {
			slog.Error("docker label validation failed",
				"source", s.name,
				"container", shortID(c.ID),
				"err", err)
			continue
		}
		if len(recs) == 0 {
			continue
		}
		slog.Debug("discovered container with dns labels",
			"source", s.name,
			"container", shortID(c.ID),
			"records", len(recs))
		records = append(records, recs...)
	}

	slog.Info("docker snapshot complete",
		"source", s.name,
		"containers_with_records", uniqueSourceIDs(records),
		"total_containers", len(listed.Items),
		"total_records", len(records))

	return records, nil
}

// Notify subscribes to Docker container events and/or a poll ticker and signals
// ch whenever container state may have changed, prompting the reconciler to
// re-snapshot. It returns when ctx is canceled and does not close ch. Because
// Snapshot re-reads all containers, no per-container debouncing is needed here;
// the reconciler coalesces signals across sources.
func (s *Source) Notify(ctx context.Context, ch chan<- struct{}) {
	var pollC <-chan time.Time
	if s.pollInterval > 0 {
		t := time.NewTicker(s.pollInterval)
		defer t.Stop()
		pollC = t.C
	}

	var eventsResult dockerclient.EventsResult
	var resubC <-chan time.Time
	subscribe := func() {
		f := make(dockerclient.Filters).Add("type", "container")
		eventsResult = s.client.Events(ctx, dockerclient.EventsListOptions{Filters: f})
	}
	if s.useEvents {
		subscribe()
	}

	signal := func() {
		select {
		case ch <- struct{}{}:
		case <-ctx.Done():
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-eventsResult.Err:
			// The client sends at most one error and closes Err; drop the channels
			// and schedule a reopen so a closed channel can't spin the loop.
			eventsResult = dockerclient.EventsResult{}
			if ctx.Err() != nil {
				continue
			}
			slog.Error("docker event stream lost, resubscribing",
				"source", s.name,
				"retry_in", eventResubscribeDelay,
				"err", err)
			resubC = time.After(eventResubscribeDelay)
		case <-resubC:
			resubC = nil
			subscribe()
			signal() // re-snapshot to catch anything missed while the stream was down
		case msg := <-eventsResult.Messages:
			switch msg.Action {
			case dockerevents.ActionStart, dockerevents.ActionDie,
				dockerevents.ActionStop, dockerevents.ActionKill:
				signal()
			}
		case <-pollC:
			signal()
		}
	}
}

// parseLabels extracts DNS records from a container's dns.* labels.
func parseLabels(sourceName, containerID string, labels map[string]string, defaults model.BaseRecord, strictValidation bool) ([]model.Record, error) {
	if labels[labelPrefix+"enable"] != "true" {
		return nil, nil
	}

	// Parse base TTL override from labels.
	base := defaults
	if v, ok := labels[labelPrefix+"ttl"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			base.TTL = n
		}
	}

	// Collect per-record-per-upstream raw values.
	// structure: raw[recordID][upstream][field] = value
	raw := map[string]map[string]map[string]string{}

	for k, v := range labels {
		if !strings.HasPrefix(k, labelPrefix) {
			continue
		}
		rest := strings.TrimPrefix(k, labelPrefix)
		parts := strings.SplitN(rest, ".", 3)
		if len(parts) != 3 {
			continue
		}
		recordID, upstreamName, field := parts[0], parts[1], parts[2]
		if raw[recordID] == nil {
			raw[recordID] = map[string]map[string]string{}
		}
		if raw[recordID][upstreamName] == nil {
			raw[recordID][upstreamName] = map[string]string{}
		}
		raw[recordID][upstreamName][field] = v
	}

	var records []model.Record
	for recordID, upstreams := range raw {
		for upstreamName, fields := range upstreams {
			r := model.Record{
				BaseRecord: base,
				ID:         recordID,
				SourceID:   containerID,
				SourceName: sourceName,
				Upstream:   upstreamName,
				Type:       model.RecordType(strings.ToUpper(fields["type"])),
				Name:       fields["name"],
				Value:      fields["value"],
			}
			if v, ok := fields["ttl"]; ok {
				if n, err := strconv.Atoi(v); err == nil {
					r.TTL = n
				}
			}
			if v, ok := fields["priority"]; ok {
				if n, err := strconv.Atoi(v); err == nil {
					r.Priority = n
				}
			}
			if v, ok := fields["comment"]; ok {
				r.Comment = v
			}

			path := fmt.Sprintf("docker://%s/%s/%s", shortID(containerID), recordID, upstreamName)
			if err := validate.StructWithPrefix(&r, path); err != nil {
				if strictValidation {
					return nil, err
				}
				slog.Warn("invalid docker label record, skipping",
					"path", path,
					"errors", err.Error())
				continue
			}

			records = append(records, r)
		}
	}
	return records, nil
}

// uniqueSourceIDs counts distinct SourceIDs in a record slice.
func uniqueSourceIDs(records []model.Record) int {
	seen := make(map[string]struct{}, len(records))
	for _, r := range records {
		seen[r.SourceID] = struct{}{}
	}
	return len(seen)
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
