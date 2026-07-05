package docker

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/netutil"
	"github.com/16bitowl/beacons/internal/validate"
	"github.com/16bitowl/beacons/pkg/source"
	dockerevents "github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"
)

const labelPrefix = "dns."

// Special tokens recognised in a record's "value" field. Each is re-resolved
// on every label parse (poll or container-start event), so the record tracks
// address changes over time rather than being fixed at container creation.
const (
	// nodeIPToken resolves to the outbound-facing local IP of the host
	// Beacons' process runs on (see internal/netutil.LocalIP).
	nodeIPToken = "__NODE_IP__"
	// containerIPToken resolves to the labelled container's own IP address.
	containerIPToken = "__CONTAINER_IP__"
	// publicIPToken resolves to this host's public, ISP-assigned IP address
	// (see internal/netutil.PublicIP). Backed by a short-lived cache since,
	// unlike the other two tokens, resolving it costs a real network call.
	publicIPToken = "__PUBLIC_IP__"
)

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
	clientOpts := []dockerclient.Opt{}
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

	var eventsResult dockerclient.EventsResult
	if s.useEvents {
		slog.Info("docker event watching enabled",
			"source", s.name)
		f := make(dockerclient.Filters).Add("type", "container")
		eventsResult = s.client.Events(ctx, dockerclient.EventsListOptions{Filters: f})
	}

	// debounce state: per-container pending timer and generation counter.
	// seq guards against a stale timer fire being processed after a newer event
	// has already replaced the pending entry for the same container.
	type pending struct {
		timer *time.Timer
		seq   uint64
	}
	type debouncedMsg struct {
		msg dockerevents.Message
		seq uint64
	}

	var nextSeq uint64
	debounced := map[string]*pending{}
	debouncedC := make(chan debouncedMsg, 64)

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-eventsResult.Err:
			if err != nil {
				slog.Error("docker event stream error",
					"source", s.name,
					"err", err)
			}
		case msg := <-eventsResult.Messages:
			if s.debounceDelay <= 0 || msg.Action == dockerevents.ActionStart {
				// Cancel any pending stop-debounce for this container on start.
				if msg.Action == dockerevents.ActionStart {
					if p, ok := debounced[msg.Actor.ID]; ok {
						p.timer.Stop()
						delete(debounced, msg.Actor.ID)
					}
				}
				s.handleEvent(ctx, msg, ch)
				continue
			}
			id := msg.Actor.ID
			nextSeq++
			seq := nextSeq
			dm := debouncedMsg{msg: msg, seq: seq} // captured by value for the closure
			if p, ok := debounced[id]; ok {
				p.timer.Stop()
				p.seq = seq
				p.timer = time.AfterFunc(s.debounceDelay, func() { debouncedC <- dm })
			} else {
				p := &pending{seq: seq}
				p.timer = time.AfterFunc(s.debounceDelay, func() { debouncedC <- dm })
				debounced[id] = p
			}
		case dm := <-debouncedC:
			if p, ok := debounced[dm.msg.Actor.ID]; ok && p.seq == dm.seq {
				delete(debounced, dm.msg.Actor.ID)
				s.handleEvent(ctx, dm.msg, ch)
			}
			// stale fire (superseded by a newer event for the same container) — discard
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
	listed, err := s.client.ContainerList(ctx, dockerclient.ContainerListOptions{})
	if err != nil {
		return err
	}

	var records []model.Record
	for _, c := range listed.Items {
		var containerIP string
		if c.NetworkSettings != nil {
			containerIP = primaryContainerIP(c.NetworkSettings.Networks)
		}
		recs, err := parseLabels(ctx, parseLabelsOpts{
			SourceName:       s.name,
			ContainerID:      c.ID,
			ContainerIP:      containerIP,
			Labels:           c.Labels,
			Defaults:         s.defaults,
			StrictValidation: s.strictValidation,
		})
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

	slog.Info("docker poll complete",
		"source", s.name,
		"containers_with_records", uniqueSourceIDs(records),
		"total_containers", len(listed.Items),
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
	case dockerevents.ActionStart:
		slog.Info("container started, inspecting labels",
			"source", s.name,
			"container", shortID(msg.Actor.ID))
		result, err := s.client.ContainerInspect(ctx, msg.Actor.ID, dockerclient.ContainerInspectOptions{})
		if err != nil {
			slog.Error("docker inspect failed",
				"id", shortID(msg.Actor.ID),
				"err", err)
			return
		}
		var containerIP string
		if result.Container.NetworkSettings != nil {
			containerIP = primaryContainerIP(result.Container.NetworkSettings.Networks)
		}
		records, err := parseLabels(ctx, parseLabelsOpts{
			SourceName:       s.name,
			ContainerID:      result.Container.ID,
			ContainerIP:      containerIP,
			Labels:           result.Container.Config.Labels,
			Defaults:         s.defaults,
			StrictValidation: s.strictValidation,
		})
		if err != nil {
			slog.Error("docker label validation failed",
				"source", s.name,
				"container", shortID(msg.Actor.ID),
				"err", err)
			return
		}
		if len(records) > 0 {
			slog.Info("container has dns records, queuing upsert",
				"source", s.name,
				"container", shortID(msg.Actor.ID),
				"records", len(records))
			ch <- source.Event{
				Type:       source.EventUpsert,
				SourceName: s.name,
				SourceID:   result.Container.ID,
				Records:    records,
			}
		} else {
			slog.Debug("container has no dns labels, skipping",
				"source", s.name,
				"container", shortID(msg.Actor.ID))
		}
	case dockerevents.ActionDie, dockerevents.ActionStop, dockerevents.ActionKill:
		slog.Info("container stopped, queuing delete",
			"source", s.name,
			"container", shortID(msg.Actor.ID),
			"action", msg.Action)
		ch <- source.Event{
			Type:       source.EventDelete,
			SourceName: s.name,
			SourceID:   msg.Actor.ID,
		}
	}
}

// parseLabelsOpts groups the inputs needed to parse DNS records from a single
// container's Docker labels.
type parseLabelsOpts struct {
	SourceName  string
	ContainerID string
	// ContainerIP is the container's own resolved network IP, used to expand
	// containerIPToken. Empty if the container has no assigned address yet.
	ContainerIP      string
	Labels           map[string]string
	Defaults         model.BaseRecord
	StrictValidation bool
}

// parseLabels extracts DNS records from Docker labels following the schema:
//
//	dns.enable=true
//	dns.ttl=300                          (base default)
//	dns.<record-id>.<upstream>.name=...
//	dns.<record-id>.<upstream>.type=...
//	dns.<record-id>.<upstream>.value=...
//	dns.<record-id>.<upstream>.ttl=...   (overrides base)
//
// The value field additionally supports three tokens, re-resolved on every
// call so records track address changes over time:
//
//	__NODE_IP__       outbound-facing local IP of the host Beacons runs on
//	__CONTAINER_IP__  the labelled container's own IP address
//	__PUBLIC_IP__     this host's public, ISP-assigned IP address
func parseLabels(ctx context.Context, opts parseLabelsOpts) ([]model.Record, error) {
	labels := opts.Labels
	if labels[labelPrefix+"enable"] != "true" {
		return nil, nil
	}

	// Parse base TTL override from labels.
	base := opts.Defaults
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
			value, err := expandValueTokens(ctx, fields["value"], opts.ContainerIP)
			if err != nil {
				slog.Warn("dns record value token expansion failed, skipping",
					"source", opts.SourceName,
					"container", shortID(opts.ContainerID),
					"record", recordID,
					"upstream", upstreamName,
					"err", err)
				continue
			}

			r := model.Record{
				BaseRecord: base,
				ID:         recordID,
				SourceID:   opts.ContainerID,
				SourceName: opts.SourceName,
				Upstream:   upstreamName,
				Type:       model.RecordType(strings.ToUpper(fields["type"])),
				Name:       fields["name"],
				Value:      value,
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

			path := fmt.Sprintf("docker://%s/%s/%s", shortID(opts.ContainerID), recordID, upstreamName)
			if err := validate.StructWithPrefix(&r, path); err != nil {
				if opts.StrictValidation {
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

// expandValueTokens replaces nodeIPToken, containerIPToken, and
// publicIPToken in value with their currently resolved addresses.
// containerIP is the labelled container's own IP, already resolved by the
// caller from Docker's network settings; it may be empty if the container
// has no address yet.
func expandValueTokens(ctx context.Context, value, containerIP string) (string, error) {
	if strings.Contains(value, containerIPToken) {
		if containerIP == "" {
			return "", fmt.Errorf("%s requested but container has no network IP yet", containerIPToken)
		}
		value = strings.ReplaceAll(value, containerIPToken, containerIP)
	}
	if strings.Contains(value, nodeIPToken) {
		ip, err := netutil.LocalIP()
		if err != nil {
			return "", fmt.Errorf("resolving %s: %w", nodeIPToken, err)
		}
		value = strings.ReplaceAll(value, nodeIPToken, ip)
	}
	if strings.Contains(value, publicIPToken) {
		ip, err := netutil.PublicIP(ctx)
		if err != nil {
			return "", fmt.Errorf("resolving %s: %w", publicIPToken, err)
		}
		value = strings.ReplaceAll(value, publicIPToken, ip)
	}
	return value, nil
}

// primaryContainerIP returns the first valid IP address across a container's
// attached networks, chosen deterministically by sorting network names.
// Returns "" if the container has no attached network with an assigned
// address yet (e.g. it is still starting).
func primaryContainerIP(networks map[string]*network.EndpointSettings) string {
	if len(networks) == 0 {
		return ""
	}

	names := make([]string, 0, len(networks))
	for name := range networks {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		ep := networks[name]
		if ep != nil && ep.IPAddress.IsValid() {
			return ep.IPAddress.String()
		}
	}
	return ""
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
