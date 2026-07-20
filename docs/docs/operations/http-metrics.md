---
title: HTTP & metrics
sidebar_position: 1
---

# HTTP & metrics

:::info

Scaffold placeholder. Full content to be migrated from `README.md`.

:::

The HTTP server runs only when `http.addr` is set.

| Path | Description | Auth |
|------|-------------|------|
| `GET /healthz` | `{"status":"ok","records":<n>}` or 503 | none |
| `GET /metrics` | Prometheus metrics | none |
| `GET /state` | Full store state as JSON | `http.auth.type` |

Application metrics: `beacons_sync_operations_total`,
`beacons_sync_duration_seconds`, `beacons_drift_corrections_total`, plus the
standard Go/process collectors.
