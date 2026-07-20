---
title: Contributing
sidebar_position: 7
---

# Contributing

:::info

Scaffold placeholder. Full content to be migrated from `README.md`.

:::

```sh
make fmt    # format code
make vet    # go vet
make lint   # golangci-lint (install separately)
make test   # tests with the race detector
```

Open an issue before starting work you intend to merge — PRs without a relevant
issue are ignored. New sources/upstreams implement the interfaces in
`pkg/source` / `pkg/upstream` and register in `cmd/beacons/main.go`; new config
fields go in `internal/config` and `beacons.example.yaml`.

## Docs tooling

The docs site is built with [Docusaurus](https://docusaurus.io/) and lives under
`docs/` as its own Node package (`docs/package.json`), separate from the Go
project.

```sh
cd docs
pnpm install      # once
pnpm start        # live preview at http://localhost:3000/beacons/
pnpm build        # production build into docs/build
```

Equivalent Make targets from the repo root:

```sh
make docs-serve   # live preview
make docs-build   # production build
```

CI (`.github/workflows/docs.yml`) builds and deploys to GitHub Pages on pushes
to `main` that touch `docs/`. A single current version is published; Docusaurus
has built-in [versioning](https://docusaurus.io/docs/versioning) that can be
enabled later if needed.
