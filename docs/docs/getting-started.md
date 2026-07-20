---
title: Getting started
sidebar_position: 2
---

# Getting started

:::info

Scaffold placeholder. Full content to be migrated from `README.md`.

:::

Images are published to Docker Hub (`16bitowl/beacons:<tag>`) and GitHub
Container Registry (`ghcr.io/16bitowl/beacons:<tag>`).

Quick start:

1. Copy `beacons.example.yaml` to `beacons.yaml` and configure your sources and upstreams.
2. Copy `.env.example` to `.env` and fill in your API tokens/passwords.
3. Run `docker compose up -d`.

:::info

Set `sync.dry_run: true` on the first run to verify records are discovered
correctly before anything is pushed to your DNS providers.

:::
