---
title: Introduction
sidebar_position: 1
slug: /
---

# Beacons

Beacons watches Docker containers and static YAML files for DNS record
definitions and syncs them to one or more upstream DNS providers. When a
container starts or stops, its records are created or removed automatically. A
periodic reconcile loop re-checks the desired state against each upstream, and
optional drift detection restores records that were hand-edited or deleted
upstream.

:::warning

Beacons is still in a heavy development phase and may ship breaking changes
between versions, even minor ones. Read the changelog before updating and avoid
critical workloads for now.

:::

:::info

Scaffold in progress — pages are being migrated from the repository `README.md`.

:::

## Sections

- [Getting started](./getting-started.md) — install and run with Docker Compose.
- [Configuration](./configuration.md) — the `beacons.yaml` file and `BEACONS_*` env overrides.
- [Sources](./sources/index.md) — Docker and YAML.
- [Upstreams](./upstreams/index.md) — Cloudflare, Pi-hole, Technitium.
- [Operations](./operations/index.md) — HTTP & metrics, drift detection.
- [Contributing](./contributing.md).
