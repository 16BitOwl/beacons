---
title: Contributing
sidebar_position: 7
---

# Contributing

Open an issue before starting work you intend to merge — PRs without a relevant issue are ignored.

## Development commands

```sh
make fmt    # format code
make vet    # go vet
make lint   # golangci-lint (install separately)
make test   # tests with the race detector
make build  # compile the Linux binary
```

The binary is statically linked (`CGO_ENABLED=0`) and targets Linux only. Building requires Go 1.26+.

## Adding a source or upstream

New sources and upstreams implement the interfaces in `pkg/source` / `pkg/upstream` and register in `cmd/beacons/main.go`. New config fields go in the appropriate struct in `internal/config` (with a `validate` tag) and must be documented in `beacons.example.yaml`.

## HTTP endpoint collection

A [Bruno](https://www.usebruno.com/) collection for the HTTP endpoints lives in `./bruno`. Open it in Bruno, select the `Local` environment, and set `api_key_value` to match your `http.auth.api_key`.

## Docs tooling

This documentation site is built with [Docusaurus](https://docusaurus.io/) and lives under `docs/` as its own Node package (`docs/package.json`), separate from the Go project.

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

CI (`.github/workflows/docs.yml`) builds and deploys to GitHub Pages on pushes to `main` that touch `docs/`. A single current version is published; Docusaurus has built-in [versioning](https://docusaurus.io/docs/versioning) that can be enabled later if needed.
