---
title: Pi-hole
sidebar_position: 2
---

# Pi-hole

Syncs records to a Pi-hole instance. Targets Pi-hole v6 and later.

## Configuration

```yaml
upstreams:
  pihole-home:
    type: pihole
    url: ${PIHOLE_URL}
    password: ${PIHOLE_PASSWORD}
    verify_interval: 60
```

| Field | Required | Description |
|-------|----------|-------------|
| `url` | yes | Base URL of the Pi-hole instance. |
| `password` | yes | Admin password used to authenticate. |

## Supported record types

`A`, `AAAA`, `CNAME`, via the Pi-hole v6 config API. Other types are not supported.

Pi-hole is typically self-hosted, so a shorter [`verify_interval`](./index.md#verify-interval) is fine.

See also: [shared HTTP tuning](./index.md#http), [drift detection](./index.md#verify-interval), and [debug logging](./index.md#debug). With `debug_log_secrets` enabled, the Pi-hole authentication exchange is also logged.
