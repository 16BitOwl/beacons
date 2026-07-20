---
title: Docker
sidebar_position: 1
---

# Docker

:::info

Scaffold placeholder. Full content to be migrated from `README.md`.

:::

Reads records from container labels under the `dns.` prefix. A container is
scanned only when `dns.enable=true`.

```yaml
dns.enable: "true"
dns.ttl: "300"
dns.<id>.<upstream>.type: "A"
dns.<id>.<upstream>.name: "host.example.com"
dns.<id>.<upstream>.value: "1.2.3.4"
dns.<id>.<upstream>.comment: "Managed by Beacons"
```
