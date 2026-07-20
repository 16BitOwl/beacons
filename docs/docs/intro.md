---
title: Introduction
sidebar_position: 1
slug: /
---

# Beacons

Beacons watches Docker containers and static YAML files for DNS record definitions and syncs them to one or more upstream DNS providers. When a container starts or stops, its records are created or removed automatically. A periodic reconcile loop re-checks the desired state against each upstream, and optional drift detection restores records that were hand-edited or deleted upstream.

:::warning

Beacons is still in a heavy development phase and may ship breaking changes between versions, even minor ones. Read the changelog before updating and avoid critical workloads for now.

:::

## How it works

1. **Sources** produce the desired set of DNS records — from Docker container labels (`dns.*`) or from static YAML files.
2. Each record names the **upstream** it targets. Upstreams are the DNS providers records are pushed to: Cloudflare, Pi-hole, or Technitium.
3. The **reconciler** diffs desired state against a persisted store and applies the minimal set of create/update/delete operations to each upstream.
4. A periodic reconcile pass self-heals transient failures. With per-upstream drift detection enabled, Beacons also reads records back from the provider and restores any that were changed or removed out of band.

## Sections

- [Getting started](./getting-started.md) — install and run with Docker Compose.
- [Configuration](./configuration.md) — the `beacons.yaml` file and `BEACONS_*` env overrides.
- [Sources](./sources/index.md) — Docker labels and static YAML.
- [Upstreams](./upstreams/index.md) — Cloudflare, Pi-hole, Technitium.
- [Operations](./operations/index.md) — HTTP endpoints, metrics, and drift detection.
- [Contributing](./contributing.md).
