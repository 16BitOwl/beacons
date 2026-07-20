---
title: Configuration
sidebar_position: 3
---

# Configuration

:::info

Scaffold placeholder. Full content to be migrated from `README.md` and
`beacons.example.yaml`.

:::

Beacons is configured via a YAML file (default: `beacons.yaml`) with optional
`BEACONS_*` environment variable overrides.

- Env vars follow `BEACONS_<YAML_PATH>` with single-underscore path segments, e.g. `BEACONS_SYNC_DRY_RUN=true`.
- Map keys (upstreams, sources) use double-underscore delimiters, e.g. `BEACONS_UPSTREAMS__CF_ZONE_A__API_TOKEN`.
- Logging is env-only and applied before the config file loads: `BEACONS_LOG_LEVEL`, `BEACONS_LOG_FORMAT`.

See `beacons.example.yaml` in the repository for the full annotated reference.
