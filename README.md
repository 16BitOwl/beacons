# Beacons

Beacons watches Docker containers and static YAML files for DNS record definitions and syncs them to one or more upstream DNS providers. When a container starts or stops, its records are created or removed automatically.

## Running with Docker Compose

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
- **Pi-hole**

## Configuration

Beacons is configured via a YAML file (default: `beacons.yaml`) with optional `BEACONS_*` environment variable overrides. Copy `beacons.example.yaml` to get started:

```sh
cp beacons.example.yaml beacons.yaml
```

Environment variables follow the pattern `BEACONS_<YAML_PATH>`, e.g. `BEACONS_SYNC_DRY_RUN=true`. Map keys (upstreams, sources) use double-underscore delimiters: `BEACONS_UPSTREAMS__CF_ZONE_A__API_TOKEN`.

## Docker labels

```
dns.enable: "true"
dns.ttl: "300"
dns.<id>.<upstream>.type: "A"
dns.<id>.<upstream>.name: "host.example.com"
dns.<id>.<upstream>.value: "1.2.3.4"
dns.<id>.<upstream>.comment: "Managed by Beacons"
```

The `value` field supports three special tokens, resolved fresh on every poll or container-start event so the record tracks address changes over time (kinda like DDNS):

- `__NODE_IP__` — the outbound-facing local IP of the host Beacons' process runs on. If Beacons runs in a container, this is the container's own network namespace unless it uses `network_mode: host`.
- `__CONTAINER_IP__` — the labelled container's own IP address, taken from its lowest-sorted attached Docker network.
- `__PUBLIC_IP__` — this host's public, ISP-assigned IP address, as seen from outside your network. Looked up via a small set of external IP-echo services (`api.ipify.org`, `ifconfig.me`, `icanhazip.com`, tried in order) and cached for 5 minutes so Beacons doesn't hammer them.

```
dns.web.pihole-home.value: "__NODE_IP__"
dns.web.pihole-home.value: "__CONTAINER_IP__"
dns.web.pihole-home.value: "__PUBLIC_IP__"
```

If a token can't be resolved (e.g. `__CONTAINER_IP__` before the container has an assigned address, or `__PUBLIC_IP__` when all lookup services are unreachable), the record is skipped with a warning until the next poll or event.

## HTTP endpoints

The HTTP endpoints are only available if the HTTP server is configured to run. Set `http.addr` in `beacons.yaml` to enable, see example configuration file. Omitting this value will disable the HTTP server and all endpoints.

| Path | Description |
|------|-------------|
| `GET /healthz` | Returns `{"status":"ok","records":<n>}` or 503 |
| `GET /metrics` | Prometheus metrics |

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

Create an issue on Github before starting any work that you wish to merge into this project. Any PR:s without a relevant issue will be ignored.

When adding a new upstream or source, implement the relevant interface in `pkg/upstream` or `pkg/source` respectively and register it in `cmd/beacons/main.go`. Keep new config fields in the appropriate struct in `internal/config` and document them in `beacons.example.yaml`.
