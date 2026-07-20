---
title: Upstreams
sidebar_position: 0
---

# Upstreams

Upstreams define where DNS records are pushed. Each is a named instance, referenced by name from source records. You can define multiple instances of the same type — e.g. two Cloudflare zones, or a Cloudflare zone alongside a Pi-hole.

- [Cloudflare](./cloudflare.md)
- [Pi-hole](./pihole.md) — v6+
- [Technitium](./technitium.md)

## Shared HTTP tuning {#http}

Every upstream accepts an `http` block for retry and resilience tuning. Zero values fall back to the transport defaults.

```yaml
upstreams:
  cf-zone-a:
    type: cloudflare
    # ...
    http:
      retry_max_attempts: 3
      retry_base_delay_ms: 500
      retry_max_delay_ms: 30000
      auth_failure_threshold: 5
```

| Field | Description |
|-------|-------------|
| `retry_max_attempts` | Maximum retry attempts per request. |
| `retry_base_delay_ms` | Base backoff delay in ms. |
| `retry_max_delay_ms` | Backoff cap in ms. |
| `auth_failure_threshold` | Consecutive HTTP 401 responses that disable the upstream until restart (`0` uses the default of 5). |

## Drift detection {#verify-interval}

Any upstream can set `verify_interval` (seconds) to have the reconciler read its actual records back and restore ones that were hand-edited or deleted out of band. `0` (default) disables verification for that upstream. Respect provider rate limits when setting it — keep API-metered providers like Cloudflare in the minutes range; self-hosted providers can go lower. See [Drift detection](../operations/drift-detection.md).

## Debug logging {#debug}

Two development-only flags live under each upstream's `http` block. Both require `BEACONS_LOG_LEVEL=debug` to take effect.

| Field | Description |
|-------|-------------|
| `debug_log` | Log full request/response bodies for this upstream. Auth headers and tokens are redacted. |
| `debug_log_secrets` | Disable that redaction and, for Pi-hole, also log the authentication exchange. Only honored with `debug_log`. |

:::warning

`debug_log_secrets` writes auth headers — and therefore API tokens or passwords — to logs in plaintext. Use it only for local troubleshooting, never in production or anywhere logs are shipped or retained.

:::
