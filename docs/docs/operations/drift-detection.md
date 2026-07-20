---
title: Drift detection
sidebar_position: 2
---

# Drift detection

Beacons keeps upstreams converged on the desired state in two layers: periodic reconciliation and optional per-upstream verification.

## Periodic reconcile

On the interval set by [`sync.reconcile_interval`](../configuration.md#sync), the reconciler runs a full pass: it re-diffs the desired records against the persisted store and re-applies anything that is missing or out of sync. This self-heals transient upstream failures without waiting for a source event. Set the interval to `0` to disable the ticker.

## Upstream verification

Periodic reconcile trusts the store as the record of what exists upstream. That misses changes made *out of band* — records hand-edited or deleted directly on the provider. Verification closes that gap.

With [`verify_interval`](../upstreams/index.md#verify-interval) set on an upstream, the reconciler periodically reads the upstream's actual records back and compares them against the desired state:

- **missing** — a managed record no longer exists upstream; Beacons recreates it.
- **changed** — a managed record's value drifted upstream; Beacons restores it.

Each correction increments `beacons_drift_corrections_total`, labelled by `upstream` and `reason` (`missing` or `changed`). See [HTTP & metrics](./http-metrics.md).

## Choosing an interval

Verification reads from the provider on every pass, so match the interval to the provider's rate limits:

- API-metered providers (e.g. Cloudflare) — keep it in the minutes range.
- Self-hosted providers (e.g. Pi-hole, Technitium) — shorter intervals are fine.

Leave `verify_interval` unset (or `0`) to disable verification for an upstream and rely on periodic reconcile alone.
