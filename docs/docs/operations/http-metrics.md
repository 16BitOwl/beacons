---
title: HTTP & metrics
sidebar_position: 1
---

# HTTP & metrics

The built-in HTTP server exposes health, metrics, and state endpoints. It runs only when [`http.addr`](../configuration.md#http) is set; leaving it empty disables the server and all endpoints.

## Endpoints

| Path | Description | Auth |
|------|-------------|------|
| `GET /healthz` | Returns `{"status":"ok","records":<n>}`, or 503 when unhealthy. | none |
| `GET /metrics` | Prometheus metrics. | none |
| `GET /state` | Full store state as JSON — every managed hostname, IP, and upstream. | `http.auth.type` |

`/healthz` and `/metrics` are always open. Only `/state` is protected.

## Authentication

`http.auth.type` controls who can read `/state`.

- **`api_key`** (default) — send the shared secret in the `X-API-Key` header. Set `http.auth.api_key` in config. If left empty, Beacons generates a random key at startup, logs a warning, and prints it to stdout. That key changes on every restart, so set it explicitly for anything beyond local testing.
- **`none`** — disables auth on `/state`. Use this only behind a trusted network boundary.

The auth method is pluggable, so other schemes can be added later without changing `/state` itself.

## Application metrics

Alongside the standard Go and process collectors (`go_*`, `process_*`), Beacons instruments these application-specific metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `beacons_sync_operations_total` | counter | Sync operations by `upstream`, `operation` (`upsert`, `delete`, `list`), and `result` (`success`, `failure`). |
| `beacons_sync_duration_seconds` | histogram | Upstream call latency by `upstream` and `operation`. |
| `beacons_drift_corrections_total` | counter | Drift corrections applied by `upstream` and `reason` (`missing`, `changed`). |
| `beacons_upstream_api_calls_total` | counter | HTTP attempts to upstream APIs by `upstream`, `method`, and `status` (HTTP status code, or `error` for a failed round trip). One per retry attempt. |
| `beacons_upstream_api_latency_seconds` | histogram | Latency of individual HTTP attempts by `upstream` and `method`. |
| `beacons_circuit_breaker_open` | gauge | `1` if an upstream's circuit breaker has tripped (too many consecutive auth failures), else `0`, by `upstream`. |
| `beacons_backoff_gated_total` | counter | Reconcile ops skipped because the record is still within its post-failure backoff window, by `upstream`. |
