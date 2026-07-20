---
title: Technitium
sidebar_position: 3
---

# Technitium

Syncs records to a zone on a Technitium DNS server.

## Configuration

```yaml
upstreams:
  technitium-home:
    type: technitium
    url: ${TECHNITIUM_URL}       # e.g. https://dns.example.com:53443
    api_token: ${TECHNITIUM_TOKEN}
    zone: example.lan
    verify_interval: 60
```

| Field | Required | Description |
|-------|----------|-------------|
| `url` | yes | Base URL of the Technitium server. |
| `api_token` | yes | API token for the server. |
| `zone` | yes | Zone the records belong to. |

## Supported record types

`A`, `AAAA`, `CNAME`, `TXT`, `MX`, `NS`. `SRV` and `CAA` are not supported.

Technitium is typically self-hosted, so a shorter [`verify_interval`](./index.md#verify-interval) is fine.

See also: [shared HTTP tuning](./index.md#http), [drift detection](./index.md#verify-interval), and [debug logging](./index.md#debug).
