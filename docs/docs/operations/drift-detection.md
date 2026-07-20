---
title: Drift detection
sidebar_position: 2
---

# Drift detection

:::info

Scaffold placeholder. Full content to be migrated from the `notes/` design docs.

:::

The reconciler periodically re-checks desired state against each upstream
(`sync.reconcile_interval`). With per-upstream `verify_interval` set, it also
reads current records back from the upstream to detect drift — records that were
hand-edited or deleted — and restores them. Corrections are counted by
`beacons_drift_corrections_total` (`reason`: `missing`, `changed`).
