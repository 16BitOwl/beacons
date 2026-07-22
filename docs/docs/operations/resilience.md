---
title: Resilience
sidebar_position: 3
---

# Resilience

Every upstream sends its requests through a shared middleware chain: circuit breaker → retry → per-attempt timeout → authentication → optional debug logging. Tuning lives in each upstream's [`http` block](../upstreams/index.md#http); zero values fall back to the defaults below.

## Retry

Transient failures are retried with exponential backoff and ±25% jitter.

- **Retried:** network errors and HTTP `429`, `500`, `502`, `503`, `504`.
- On `429`, the `Retry-After` header is honored when present, capped at the max delay.
- Rejected credentials and requests with a non-replayable body are never retried.

| Field | Default | Description |
|-------|---------|-------------|
| `retry_max_attempts` | `3` | Total attempts, including the first. |
| `retry_base_delay_ms` | `500` | Initial backoff delay. |
| `retry_max_delay_ms` | `30000` | Backoff cap; also caps a honored `Retry-After`. |

## Per-attempt timeout

Each attempt — including any authentication exchange and reading the response body — is bounded by a 15s timeout. Backoff sleeps are not counted against it, so total time is bounded by `retry_max_attempts × (timeout + retry_max_delay_ms)`.

## Circuit breaker

After too many consecutive authentication failures — HTTP `401`, `403`, or rejected credentials — the breaker opens and every further request fails fast until the process restarts. The counter resets on any non-auth response.

| Field | Default | Description |
|-------|---------|-------------|
| `auth_failure_threshold` | `5` | Consecutive auth failures that open the breaker. |

The breaker is an authentication killswitch, not outage protection: it never trips on `5xx` or network errors, which are handled by retry instead. When it opens, Beacons logs an error naming the upstream; fix the credentials and restart to recover.

## Session authentication

Providers that authenticate with a session token (Pi-hole) acquire one on first use and cache it until shortly before it expires. A request rejected with `401` triggers one re-authentication and a single retry. Concurrent requests that hit `401` on the same token share one re-authentication rather than each re-hitting the auth endpoint.

## Debug logging

Full request/response logging is available per upstream for local troubleshooting. See [debug logging](../upstreams/index.md#debug).
