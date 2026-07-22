---
title: Getting started
sidebar_position: 2
---

# Getting started

Beacons ships as a container image. The quickest way to run it is with Docker Compose.

## Images

Published to both registries:

- Docker Hub — `16bitowl/beacons:<tag>`
- GitHub Container Registry — `ghcr.io/16bitowl/beacons:<tag>`

## Quick start

1. Copy `beacons.example.yaml` to `beacons.yaml` and configure your sources and upstreams. See [Configuration](./configuration.md).
2. Copy `.env.example` to `.env` and fill in your API tokens and passwords. The config references these via `${VAR}` syntax.
3. Start the service:

   ```sh
   docker compose up -d
   ```

:::tip

Set `sync.dry_run: true` on the first run. Beacons then logs the records it would create, update, or delete without touching any upstream — verify discovery is correct before pushing to your DNS providers.

:::

## What the compose file mounts

In the example Docker Compose file the `beacons` service mounts:

- the Docker socket — so the Docker source can read container labels;
- `beacons.yaml` — the main config file;
- a static config directory — for YAML source files matched by a glob;
- a data volume — persistent store state across restarts (see [`store` config](./configuration.md#store)).

## Next steps

- [Configuration](./configuration.md) — every config field and env-var override.
- [Sources](./sources/index.md) — define records via Docker labels or YAML.
- [Upstreams](./upstreams/index.md) — connect Cloudflare, Pi-hole, or Technitium.
- [Operations](./operations/index.md) — health, metrics, and drift detection.
- [CLI flags](./operations/cli.md) — every flag, including config validation.
