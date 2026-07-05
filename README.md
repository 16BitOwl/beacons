# Beacons

Beacons watches Docker containers and static YAML files for DNS record definitions and syncs them to one or more upstream DNS providers. When a container starts or stops, its records are created or removed automatically.

## Running with Docker Compose

> [!CAUTION]
> Beacons is still in a heavy development phase and as such might come with breaking changes between versions, even minor. Be sure to read the changelogs carefully before updating and do not use for critical workloads for now.

Images are available on both Docker hub (`16bitowl/beacons:<tag>`) or on Github Container Registry (`ghcr.io/16bitowl/beacons:<tag>`).

Quick start:
1. Copy `beacons.example.yaml` to `beacons.yaml` and prepare in your upstreams and config
2. Copy `.env.example` to `.env` and fill in your API tokens/passwords
3. Run:
```sh
docker compose up -d
```

> [!TIP]
> set `dry_run: true` in `beacons.yaml` on first run to verify records are discovered correctly before anything is pushed to your DNS providers.

By default in the example Docker compose the `beacons` service mounts the Docker socket, `beacons.yaml`, a static config directory, and a data volume for persistent state.

## Supported sources

- **Docker** — reads records from container labels (`dns.*`)
- **YAML** — reads records from static files matched by a glob pattern

## Supported upstreams

- **Cloudflare**
- **Pi-hole** (v6+)

## Configuration

Beacons is configured via a YAML file (default: `beacons.yaml`) with optional `BEACONS_*` environment variable overrides. Copy `beacons.example.yaml` to get started:

```sh
cp beacons.example.yaml beacons.yaml
```

Environment variables follow the pattern `BEACONS_<YAML_PATH>`, e.g. `BEACONS_SYNC_DRY_RUN=true`. Map keys (upstreams, sources) use double-underscore delimiters: `BEACONS_UPSTREAMS__CF_ZONE_A__API_TOKEN`.

## Docker labels

```yaml
dns.enable: "true"
dns.ttl: "300"
dns.<id>.<upstream>.type: "A"
dns.<id>.<upstream>.name: "host.example.com"
dns.<id>.<upstream>.value: "1.2.3.4"
dns.<id>.<upstream>.comment: "Managed by Beacons"
```

## Static YAML records

```yaml
records:
    <id>:
        <upstream>:
            type: "A"
            name: "host.example.com"
            value: "1.2.3.4"
            comment: "Managed by Beacons"
```

## HTTP endpoints

The HTTP endpoints are only available if the HTTP server is configured to run. Set `http.addr` in `beacons.yaml` to enable, see example configuration file. Omitting this value will disable the HTTP server and all endpoints.

| Path | Description |
|------|-------------|
| `GET /healthz` | Returns `{"status":"ok","records":<n>}` or 503 |
| `GET /metrics` | Prometheus metrics |
| `GET /state`   | Returns the full store state as JSON. Requires auth, see below |

### Authentication

`/state` dumps every managed hostname, IP, and upstream — `http.auth.type` controls who can read it. `/healthz` and `/metrics` are always open.

- `api_key` (default): send the shared secret in the `X-API-Key` header. Set `http.auth.api_key` in config; if left empty, beacons generates a random key on startup, logs a warning, and prints it to stdout (it changes on every restart, so set it explicitly for anything beyond local testing).
- `none`: disables auth on `/state`. Only use this behind a trusted network boundary.

The auth method is pluggable, so other schemes can be added later without changing `/state` itself.

### Application metrics

In addition to the standard Go metrics, these application specific metrics are also instrumented:

| Metric | Type | Description |
|--------|------|-------------|
| `beacons_sync_operations_total` | counter | Sync operations by `upstream`, `operation`, `result` |
| `beacons_sync_duration_seconds` | histogram | Upstream call latency by `upstream`, `operation` |

## Building

Requires Go 1.26+.

```sh
make build        # compile binary for Linux
make docker       # build Docker image
make tidy         # tidy go.mod and go.sum
```

The binary is statically linked (`CGO_ENABLED=0`) and targets Linux only.

## Contributing

```sh
make fmt          # format code
make vet          # run go vet
make lint         # run golangci-lint (must be installed separately)
make test         # run tests
```

A [Bruno](https://www.usebruno.com/) collection for the HTTP endpoints lives in `./bruno`. Open it in Bruno, select the `Local` environment, and set `api_key_value` to match your `http.auth.api_key`.

Create an issue on Github before starting any work that you wish to merge into this project. Any PR:s without a relevant issue will be ignored.

When adding a new upstream or source, implement the relevant interface in `pkg/upstream` or `pkg/source` respectively and register it in `cmd/beacons/main.go`. Keep new config fields in the appropriate struct in `internal/config` and document them in `beacons.example.yaml`.

### Debug logging

Each upstream accepts two development-only flags under `http`, both of which also require `BEACONS_LOG_LEVEL=debug` to take effect:

- `debug_log`: logs full request/response bodies for that upstream. Auth headers and tokens are redacted.
- `debug_log_secrets`: disables that redaction and, for Pi-hole, also logs the authentication exchange.

A separate top-level flag governs config logging:

- `log.reveal_values`: when the config loader logs which values were set or overridden by `BEACONS_*` env vars, it logs keys only by default. Set this to include the values. Requires `BEACONS_LOG_LEVEL=debug`, and must be set in the config file (it governs the env-overlay logging itself, so it isn't env-overridable).

> [!WARNING]
> `debug_log_secrets` logs auth headers, and `log.reveal_values` logs env-override values — either might write API tokens or passwords to the logs in plaintext. Use them only for local troubleshooting, never in production or anywhere logs are shipped or retained.
