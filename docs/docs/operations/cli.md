---
title: CLI flags
sidebar_position: 4
---

# CLI flags

```
beacons [flags]
```

| Flag | Default | Description |
|---|---|---|
| `-config` | `beacons.yaml` | Path to the config file. |
| `-validate-config` | `false` | Parse the config and any YAML source files, then exit `0`/`1`. Does not contact Docker or upstream APIs, and does not start the server. See [below](#-validate-config). |
| `-healthcheck` | `false` | Hit `/healthz` on a running instance and exit `0`/`1`. Used as the container's `HEALTHCHECK` command. |
| `-healthcheck-addr` | `http://localhost:9090` | Base URL used by `-healthcheck`. |
| `-version` | `false` | Print the build version and exit. |

## `-validate-config`

Checks a config file is well-formed before deploying it:

```sh
beacons -config beacons.yaml -validate-config
```

What it checks:

- The config file parses and passes struct validation (required fields per source/upstream type, URL formats, etc.) — the same checks a normal start performs.
- Each `yaml` source's glob is read and every matched file is parsed, catching malformed record files or bad field values.

What it does **not** do, by design — so it's safe to run in CI or before credentials/socket access are available:

- It does not dial the Docker daemon for `docker` sources — only the client is constructed.
- It does not contact any upstream API (Cloudflare, Pi-hole, Technitium) — upstream config is checked by struct validation only, not by building a real client.
- It does not bind the HTTP port or start the reconcile loop.

On success it logs a per-source/upstream summary and exits `0`. On the first failure it logs the error and exits `1`.
