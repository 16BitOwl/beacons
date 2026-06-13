package docker

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/source"
	"github.com/docker/docker/api/types/container"
	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	dockerclient "github.com/docker/docker/client"
)

const labelPrefix = "dns."

// Source is the Docker source adapter.
type Source struct {
	name          string
	client        *dockerclient.Client
	defaults      model.BaseRecord
	pollInterval  time.Duration
	useEvents     bool
	debounceDelay time.Duration
}

// New creates a new Docker source adapter.
func New(name string, host string, defaults model.BaseRecord, pollInterval time.Duration, useEvents bool, debounceDelay time.Duration) (*Source, error) {
	opts := []dockerclient.Opt{dockerclient.WithAPIVersionNegotiation()}
	if host != "" {
		opts = append(opts, dockerclient.WithHost(host))
	}
	c, err := dockerclient.NewClientWithOpts(opts...)
	if err != nil {
		return nil, err
	}
	return &Source{
		name:          name,
		client:        c,
		defaults:      defaults,
		pollInterval:  pollInterval,
		useEvents:     useEvents,
		debounceDelay: debounceDelay,
	}, nil
}

func (s *Source) Name() string { return s.name }

func (s *Source) Run(ctx context.Context, ch chan<- source.Event) error {
	slog.Info("docker source starting",
		"source", s.name,
		"poll_interval", s.pollInterval,
		"use_events", s.useEvents)

	// Initial full sync — emits EventSync so the syncer can remove any records
	// from containers that were removed while Beacons was offline.
	if err := s.poll(ctx, ch); err != nil {
		slog.Error("docker initial poll failed",
			"source", s.name,
			"err", err)
	}

	var pollC <-chan time.Time
	if s.pollInterval > 0 {
		t := time.NewTicker(s.pollInterval)
		defer t.Stop()
		pollC = t.C
	}

	var eventC <-chan dockerevents.Message
	var errC <-chan error
	if s.useEvents {
		slog.Info("docker event watching enabled",
			"source", s.name)
		f := filters.NewArgs(filters.Arg("type", "container"))
		eventC, errC = s.client.Events(ctx, dockerevents.ListOptions{Filters: f})
	}

	// debounce state: per-container pending timer and last event
	type pending struct {
		timer *time.Timer
		msg   dockerevents.Message
	}
	debounced := map[string]*pending{}
	debouncedC := make(chan dockerevents.Message, 64)

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errC:
			if err != nil {
				slog.Error("docker event stream error",
					"source", s.name,
					"err", err)
			}
		case msg := <-eventC:
			if s.debounceDelay <= 0 || msg.Action == "start" {
				// Cancel any pending stop-debounce for this container on start.
				if msg.Action == "start" {
					if p, ok := debounced[msg.Actor.ID]; ok {
						p.timer.Stop()
						delete(debounced, msg.Actor.ID)
					}
				}
				s.handleEvent(ctx, msg, ch)
				continue
			}
			id := msg.Actor.ID
			if p, ok := debounced[id]; ok {
				p.timer.Stop()
				p.msg = msg
				p.timer = time.AfterFunc(s.debounceDelay, func() { debouncedC <- p.msg })
			} else {
				p := &pending{msg: msg}
				p.timer = time.AfterFunc(s.debounceDelay, func() { debouncedC <- p.msg })
				debounced[id] = p
			}
		case msg := <-debouncedC:
			delete(debounced, msg.Actor.ID)
			s.handleEvent(ctx, msg, ch)
		case <-pollC:
			if err := s.poll(ctx, ch); err != nil {
				slog.Error("docker poll failed",
					"source", s.name,
					"err", err)
			}
		}
	}
}

// poll scans all running containers and emits a single EventSync containing
// every DNS record found. The syncer uses this to detect and clean up records
// from containers that are no longer running.
func (s *Source) poll(ctx context.Context, ch chan<- source.Event) error {
	containers, err := s.client.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return err
	}

	var records []model.Record
	for _, c := range containers {
		recs := parseLabels(s.name, c.ID, c.Labels, s.defaults)
		if len(recs) == 0 {
			continue
		}
		slog.Debug("discovered container with dns labels",
			"source", s.name,
			"container", c.ID[:12],
			"records", len(recs))
		records = append(records, recs...)
	}

	slog.Info("docker poll complete",
		"source", s.name,
		"containers_with_records", uniqueSourceIDs(records),
		"total_containers", len(containers),
		"total_records", len(records))

	ch <- source.Event{
		Type:       source.EventSync,
		SourceName: s.name,
		Records:    records,
	}
	return nil
}

func (s *Source) handleEvent(ctx context.Context, msg dockerevents.Message, ch chan<- source.Event) {
	switch msg.Action {
	case "start":
		slog.Info("container started, inspecting labels",
			"source", s.name,
			"container", msg.Actor.ID[:12])
		info, err := s.client.ContainerInspect(ctx, msg.Actor.ID)
		if err != nil {
			slog.Error("docker inspect failed",
				"id", msg.Actor.ID[:12],
				"err", err)
			return
		}
		records := parseLabels(s.name, info.ID, info.Config.Labels, s.defaults)
		if len(records) > 0 {
			slog.Info("container has dns records, queuing upsert",
				"source", s.name,
				"container", msg.Actor.ID[:12],
				"records", len(records))
			ch <- source.Event{
				Type:       source.EventUpsert,
				SourceName: s.name,
				SourceID:   info.ID,
				Records:    records,
			}
		} else {
			slog.Debug("container has no dns labels, skipping",
				"source", s.name,
				"container", msg.Actor.ID[:12])
		}
	case "die", "stop", "kill":
		slog.Info("container stopped, queuing delete",
			"source", s.name,
			"container", msg.Actor.ID[:12],
			"action", msg.Action)
		ch <- source.Event{
			Type:       source.EventDelete,
			SourceName: s.name,
			SourceID:   msg.Actor.ID,
		}
	}
}

// parseLabels extracts DNS records from Docker labels following the schema:
//
//	dns.enable=true
//	dns.ttl=300                          (base default)
//	dns.<record-id>.<upstream>.name=...
//	dns.<record-id>.<upstream>.type=...
//	dns.<record-id>.<upstream>.value=...
//	dns.<record-id>.<upstream>.ttl=...   (overrides base)
func parseLabels(sourceName, containerID string, labels map[string]string, defaults model.BaseRecord) []model.Record {
	if labels[labelPrefix+"enable"] != "true" {
		return nil
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
			records = append(records, r)
		}
	}
	return records
}

// uniqueSourceIDs counts distinct SourceIDs in a record slice.
func uniqueSourceIDs(records []model.Record) int {
	seen := make(map[string]struct{}, len(records))
	for _, r := range records {
		seen[r.SourceID] = struct{}{}
	}
	return len(seen)
}
