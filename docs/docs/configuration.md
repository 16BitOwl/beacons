---
title: Configuration
sidebar_position: 3
---

# Configuration

Beacons is configured via a YAML file (default: `beacons.yaml`) with optional `BEACONS_*` environment variable overrides. The config file is optional — if it is missing, config is sourced entirely from environment variables.

Copy the annotated reference to get started:

```sh
cp beacons.example.yaml beacons.yaml
```

## Environment overrides

Every config field can be overridden by an environment variable without editing the file.

- Variables follow `BEACONS_<YAML_PATH>`, joining path segments with a single underscore — e.g. `BEACONS_SYNC_DRY_RUN=true`, `BEACONS_HTTP_ADDR=:9091`.
- Map keys (upstreams, sources) use double-underscore delimiters around the key — e.g. `BEACONS_UPSTREAMS__CF_ZONE_A__API_TOKEN=abc123`.
- Values inside the file can reference environment variables with `${VAR}` syntax. Set `sync.strict_env: true` to fail on startup if any referenced variable is unset.

## `defaults`

Applied to every record unless overridden per-record or per-upstream. Merge order is: global `defaults` → file-level `defaults` (YAML sources) → record level → per-upstream field.

```yaml
defaults:
  ttl: 300
  comment: "managed by beacons"
```

| Field | Description |
|-------|-------------|
| `ttl` | Default record TTL in seconds. |
| `priority` | Default priority (0–65535); used by `MX` and `SRV`. |
| `comment` | Default comment applied to managed records. |

## `sync`

Controls the sync and reconcile loops.

```yaml
sync:
  poll_interval: 600
  debounce_ms: 500
  use_events: true
  dry_run: false
  strict_env: false
  reconcile_interval: 300
```

| Field | Default | Description |
|-------|---------|-------------|
| `poll_interval` | `300` | Docker poll interval in seconds; `0` disables polling. |
| `use_events` | `true` | Also watch Docker events in real time. |
| `debounce_ms` | `500` | Collapse rapid container events into one action after this many ms of quiet; `0` disables debouncing. |
| `dry_run` | `false` | Log upstream operations instead of applying them. |
| `strict_env` | `true` | Fail on startup if any `${VAR}` reference is unset. |
| `strict_validation` | `false` | Treat invalid records from sources as fatal instead of skipping them with a warning. |
| `reconcile_interval` | `300` | Full reconcile pass interval in seconds; `0` disables the ticker. |

## `store`

How records persist between restarts. See [Operations](./operations/index.md) for how the store relates to reconciliation and drift detection.

```yaml
store:
  type: file
  path: /data/beacons-state.json
```

| Field | Default | Description |
|-------|---------|-------------|
| `type` | `memory` | `memory` (in-process, lost on restart) or `file` (persisted). |
| `path` | — | File path for the `file` store. Required when `type: file`. |

## `http`

The built-in HTTP server. Leave `addr` empty to disable the server and all endpoints. Full endpoint, auth, and metrics reference: [HTTP & metrics](./operations/http-metrics.md).

```yaml
http:
  addr: ":9090"
  read_timeout: 5
  idle_timeout: 60
  write_timeout: 10
  shutdown_timeout: 5
  auth:
    type: api_key
    api_key: ""
```

| Field | Default | Description |
|-------|---------|-------------|
| `addr` | `:9090` | Listen address; empty disables the server. |
| `read_timeout` | `5` | Read timeout in seconds; `0` = infinite. |
| `idle_timeout` | `60` | Keep-alive idle timeout in seconds; `0` = infinite. |
| `write_timeout` | `10` | Write timeout in seconds; `0` = infinite. |
| `shutdown_timeout` | `5` | Graceful shutdown timeout in seconds; must be `> 0`. |
| `auth.type` | `api_key` | `api_key` or `none`; guards protected endpoints (`/state`). |
| `auth.api_key` | — | Shared secret for the `X-API-Key` header. If empty, a random key is generated and printed to stdout on startup. |

## `sources` and `upstreams`

Named instances defining where records come from and where they go. Each name is arbitrary but must be unique; you can define multiple instances of the same type. Records reference an upstream by name.

```yaml
sources:
  docker-local:
    type: docker
    host: unix:///var/run/docker.sock
  static-records:
    type: yaml
    glob: /config/*.yaml

upstreams:
  cf-zone-a:
    type: cloudflare
    api_token: ${CF_API_TOKEN}
    zone_id: ${CF_ZONE_A_ID}
```

Per-type fields are documented on each adapter page:

- Sources — [Docker](./sources/docker.md), [YAML](./sources/yaml.md).
- Upstreams — [Cloudflare](./upstreams/cloudflare.md), [Pi-hole](./upstreams/pihole.md), [Technitium](./upstreams/technitium.md).

## Logging

Logging is configured by environment variables only, since it takes effect before the config file is parsed. These are not YAML fields.

| Variable | Values | Default |
|----------|--------|---------|
| `BEACONS_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `BEACONS_LOG_FORMAT` | `text`, `json` | `text` |

One related YAML field governs env-overlay logging:

```yaml
log:
  reveal_values: true # DEV ONLY
```

`log.reveal_values` includes the *values* set or overridden by `BEACONS_*` env vars in debug logs (keys only by default). It must be set in the config file — it governs the env-overlay logging itself, so it is not env-overridable — and requires `BEACONS_LOG_LEVEL=debug`.

:::warning

`log.reveal_values` may write API tokens or passwords to logs in plaintext. Use it only for local troubleshooting, never in production or anywhere logs are shipped or retained. See also the per-upstream `debug_log_secrets` flag on the upstream pages.

:::
