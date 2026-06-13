# Beacons

Beacons watches Docker containers and static YAML files for DNS record definitions and syncs them to one or more upstream DNS providers. When a container starts or stops, its records are created or removed automatically.

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
```

## HTTP endpoints

| Path | Description |
|------|-------------|
| `GET /healthz` | Returns `{"status":"ok","records":<n>}` or 503 |
| `GET /metrics` | Prometheus metrics |

### Application metrics

| Metric | Type | Description |
|--------|------|-------------|
| `beacons_sync_operations_total` | counter | Sync operations by `upstream`, `operation`, `result` |
| `beacons_sync_duration_seconds` | histogram | Upstream call latency by `upstream`, `operation` |

## Running with Docker Compose

```sh
docker compose up -d
```

The `beacons` service mounts the Docker socket, `beacons.yaml`, a static config directory, and a data volume for persistent state.

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

When adding a new upstream or source, implement the relevant interface in `pkg/upstream` or `pkg/source` respectively and register it in `cmd/beacons/main.go`. Keep new config fields in the appropriate struct in `internal/config` and document them in `beacons.example.yaml`.
