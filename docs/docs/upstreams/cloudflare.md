---
title: Cloudflare
sidebar_position: 1
---

# Cloudflare

Syncs records to a Cloudflare zone via the Cloudflare API.

## Configuration

```yaml
upstreams:
  cf-zone-a:
    type: cloudflare
    api_token: ${CF_API_TOKEN}
    zone_id: ${CF_ZONE_A_ID}
    # verify_interval: 300
    http:
      retry_max_attempts: 3
      retry_base_delay_ms: 500
      retry_max_delay_ms: 30000
```

| Field | Required | Description |
|-------|----------|-------------|
| `api_token` | yes | Cloudflare API token with edit access to the zone. |
| `zone_id` | yes | Target zone ID. |

Cloudflare is API-metered, so keep [`verify_interval`](./index.md#verify-interval) in the minutes range to stay within rate limits.

See also: [shared HTTP tuning](./index.md#http), [drift detection](./index.md#verify-interval), and [debug logging](./index.md#debug).
