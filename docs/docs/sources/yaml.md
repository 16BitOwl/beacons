---
title: YAML
sidebar_position: 2
---

# YAML

Reads DNS records from static YAML files matched by a glob pattern. Files are watched for changes; a debounced reload fires once a write settles, so edits are picked up without a restart.

## Configuration

```yaml
sources:
  static-records:
    type: yaml
    glob: /config/*.yaml
```

| Field | Description |
|-------|-------------|
| `glob` | Glob pattern matching the record files, e.g. `/config/*.yaml`. |

## File schema

```yaml
defaults:            # optional, file-level defaults
  ttl: 300
  comment: "managed by beacons"

records:
  web:
    ttl: 300         # optional, record-level base (all upstreams below)
    cloudflare:
      type: A
      name: svc.example.com
      value: 1.2.3.4
    pihole-home:
      type: A
      name: svc.example.com
      value: 10.0.0.5
  alias:
    cloudflare:
      type: CNAME
      name: alias.example.com
      value: svc.example.com
      ttl: 3600      # optional, overrides record + file defaults
```

- The top-level key under `records` is an arbitrary record `<id>`, unique within the file.
- Under each id, keys name the target `<upstream>` instance from `beacons.yaml`. One id can target several upstreams.
- `type` is upper-cased automatically. Supported types: `A`, `AAAA`, `CNAME`, `TXT`, `MX`, `SRV`, `NS`, `CAA`.

## Value precedence

`ttl`, `priority`, and `comment` resolve in this order, most specific wins:

1. Per-upstream field (deepest level).
2. Record-level base (sibling of the upstream keys).
3. File-level `defaults`.
4. Global [`defaults`](../configuration.md#defaults) in `beacons.yaml`.

Invalid records are skipped with a warning by default; set [`sync.strict_validation: true`](../configuration.md#sync) to make them fatal.
