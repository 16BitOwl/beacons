---
title: YAML
sidebar_position: 2
---

# YAML

:::info

Scaffold placeholder. Full content to be migrated from `README.md`.

:::

Reads records from static files matched by a glob pattern. Each file may also
carry an optional file-level `defaults` block.

```yaml
records:
    <id>:
        <upstream>:
            type: "A"
            name: "host.example.com"
            value: "1.2.3.4"
            comment: "Managed by Beacons"
```
