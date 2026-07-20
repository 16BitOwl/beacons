---
title: Docker
sidebar_position: 1
---

# Docker

Reads DNS records from Docker container labels under the `dns.` prefix. A container is scanned only when it carries `dns.enable=true`; all other containers are ignored.

## Configuration

```yaml
sources:
  docker-local:
    type: docker
    host: unix:///var/run/docker.sock
```

| Field | Description |
|-------|-------------|
| `host` | Docker endpoint, e.g. `unix:///var/run/docker.sock` or `tcp://10.0.0.1:2375`. |

Discovery is driven by [`sync.poll_interval`, `sync.use_events`, and `sync.debounce_ms`](../configuration.md#sync). With events enabled, container `start`, `stop`, `kill`, and `die` transitions trigger a debounced re-scan in near real time; polling is the fallback.

## Label schema

```yaml
dns.enable: "true"
dns.ttl: "300"                          # base default for this container
dns.<id>.<upstream>.type: "A"
dns.<id>.<upstream>.name: "host.example.com"
dns.<id>.<upstream>.value: "1.2.3.4"
dns.<id>.<upstream>.ttl: "3600"         # optional, overrides dns.ttl
dns.<id>.<upstream>.priority: "10"      # optional, MX/SRV
dns.<id>.<upstream>.comment: "Managed by Beacons"
```

- `<id>` is an arbitrary record identifier, unique within the container (e.g. `web`, `api`).
- `<upstream>` is the name of an upstream instance defined in `beacons.yaml`.
- A single container can declare many records across several upstreams.
- `dns.ttl` sets a container-wide base; per-record `ttl`/`priority`/`comment` override it. `type` is upper-cased automatically.

Supported record types: `A`, `AAAA`, `CNAME`, `TXT`, `MX`, `SRV`, `NS`, `CAA`.

Invalid records are skipped with a warning by default; set [`sync.strict_validation: true`](../configuration.md#sync) to make them fatal.
