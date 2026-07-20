# Beacons

Beacons watches Docker containers and static YAML files for DNS record definitions and syncs them to one or more upstream DNS providers. When a container starts or stops, its records are created or removed automatically. A periodic reconcile loop re-checks the desired state against each upstream, and optional drift detection (per-upstream `verify_interval`) restores records that were hand-edited or deleted upstream.

**Full documentation: https://16bitowl.github.io/beacons/**

> [!CAUTION]
> Beacons is still in a heavy development phase and as such might come with breaking changes between versions, even minor. Be sure to read the changelogs carefully before updating and do not use for critical workloads for now.

## Supported sources

- **Docker** — reads records from container labels (`dns.*`)
- **YAML** — reads records from static files matched by a glob pattern

## Supported upstreams

- **Cloudflare**
- **Pi-hole** (v6+)
- **Technitium**

## Quick start

Images are available on both Docker Hub (`16bitowl/beacons:<tag>`) and the GitHub Container Registry (`ghcr.io/16bitowl/beacons:<tag>`).

1. Copy `beacons.example.yaml` to `beacons.yaml` and prepare your upstreams and config.
2. Copy `.env.example` to `.env` and fill in your API tokens/passwords.
3. Run:

```sh
docker compose up -d
```

> [!TIP]
> Set `dry_run: true` in `beacons.yaml` on first run to verify records are discovered correctly before anything is pushed to your DNS providers.

See the [Getting started guide](https://16bitowl.github.io/beacons/getting-started) for details.

## Documentation

Detailed and technical documentation lives on the [documentation site](https://16bitowl.github.io/beacons/):

- [Configuration](https://16bitowl.github.io/beacons/configuration) — the `beacons.yaml` file and `BEACONS_*` env overrides.
- [Sources](https://16bitowl.github.io/beacons/sources/) — Docker labels and static YAML.
- [Upstreams](https://16bitowl.github.io/beacons/upstreams/) — Cloudflare, Pi-hole, Technitium.
- [Operations](https://16bitowl.github.io/beacons/operations/) — HTTP endpoints, metrics, and drift detection.
- [Contributing](https://16bitowl.github.io/beacons/contributing) — building, dev commands, and adding adapters.

## Contributing

Create an issue on GitHub before starting any work that you wish to merge into this project. Any PRs without a relevant issue will be ignored.

Common commands:

```sh
make build        # compile binary for Linux
make test         # run tests with the race detector
make fmt          # format code
make vet          # run go vet
make lint         # run golangci-lint (install separately)
```

Requires Go 1.26+. See the [contributing guide](https://16bitowl.github.io/beacons/contributing) for the full workflow, adapter interfaces, and docs tooling.

## License

See [LICENSE](LICENSE).
