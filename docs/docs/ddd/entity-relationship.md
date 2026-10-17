---
id: entity-relationship
title: Entity-relationship diagram
sidebar_label: Entity-relationship
sidebar_position: 18
---

# Entity-relationship diagram

The OLTP schema created by `migrations/0001_replenishment_schema.up.sql` and
`migrations/0002_forward_assignment_maps.up.sql`. The schema declares **no foreign keys**: the relations drawn below are
logical, joined at read time, so rows of the event-fed copies may arrive in any order.

```mermaid
erDiagram
    policies {
        text site_id PK
        text sku PK
        bigint min_qty
        bigint max_qty
        bigint multiple
        bigint version
        timestamptz declared_at
    }
    replenishment_requests {
        text request_id PK
        text site_id
        text sku
        text forward_bin
        text source_bin
        bigint quantity
        bigint moved_quantity
        text priority
        text state
        bigint policy_min
        bigint policy_max
        timestamptz requested_at
        timestamptz completed_at
        timestamptz cancelled_at
        text cancel_reason
        bigint version
    }
    bin_stock_levels {
        text site_code PK
        text bin PK
        text sku PK
        bigint on_hand
        bigint reserved
        bigint version
    }
    forward_assignments {
        text site_id PK
        text sku PK
        text bin
    }
    forward_assignment_maps {
        text site_id PK
        text plan_id
        timestamptz approved_at
        timestamptz applied_at
    }
    zones {
        text zone_id PK
        text site_code
        text zone_code
    }
    locations {
        text location_code PK
        text zone_id
        text role
        boolean decommissioned
    }
    processed_events {
        text consumer PK
        text event_id PK
        timestamptz processed_at
    }
    outbox_events {
        bigserial id PK
        text event_id
        text topic
        text event_type
        text subject
        bytea key
        text dataschema
        bytea value
        jsonb headers
        timestamptz created_at
        timestamptz published_at
        int attempts
        text last_error
    }
    idempotency_keys {
        text key PK
        text fingerprint
        int status
        text content_type
        bytea body
        timestamptz created_at
    }
    policies ||--o{ replenishment_requests : "sized with (site, sku)"
    forward_assignments ||--o{ replenishment_requests : "forward bin (site, sku, bin)"
    forward_assignment_maps ||--o{ forward_assignments : "stamps the site's map"
    locations ||--o{ bin_stock_levels : "bin"
    locations ||--o{ forward_assignments : "bin"
    zones ||--o{ locations : "zone_id"
```

Source: `migrations/0001_replenishment_schema.up.sql`, `migrations/0002_forward_assignment_maps.up.sql`.
`processed_events`, `outbox_events` and `idempotency_keys` are plumbing and relate to nothing by key.

## What backs what

| Table | Backs | Written by | Notes |
| --- | --- | --- | --- |
| `policies` | `ReplenishmentPolicy` | `DeclarePolicy`, `RemovePolicy` | `version` guards UPDATE and DELETE; checks `min_qty >= 0`, `min_qty < max_qty`, `multiple >= 1` |
| `replenishment_requests` | `ReplenishmentRequest` snapshot | `Evaluator`, `RaiseManualRequest`, `CancelRequest`, `ApplyRelocation`, `ApplyForwardAssignmentMap`, `RemovePolicy` | partial unique index `uq_requests_one_open_per_pick_face` on `(site_id, forward_bin, sku) WHERE state = 'Requested'` is invariant 1; `idx_requests_listing` serves the newest-first listing |
| `bin_stock_levels` | bin stock copy | `ApplyBinLevel` | absolute level guarded by the per-(bin, SKU) `version`; a bin never seen has no row and reads 0 |
| `forward_assignments` | the site's forward map | `ApplyForwardAssignmentMap` | replaced wholesale per site; one forward bin per (site, SKU) |
| `forward_assignment_maps` | stale-map guard | `ApplyForwardAssignmentMap` | one row per site; `approved_at` of the map currently held |
| `zones`, `locations` | layout copy | `ApplyZone`, `ApplyLocationRegistered`, `ApplyLocationDecommissioned` | a location joins its zone at read time; `decommissioned` is irreversible; empty `role` means Storage |
| `processed_events` | consumer idempotency | every consumer use case | `(consumer, event_id)` claimed in the effect's transaction |
| `outbox_events` | transactional outbox | the events sink, drained by the relay | unique `(event_id, topic)`; partial index on unpublished rows; `attempts` and `last_error` |
| `idempotency_keys` | `Idempotency-Key` answers | the HTTP middleware | only 2xx and 4xx answers; first writer wins |

Text keys compared or ordered in Go use the `"C"` collation so database order equals the in-memory adapters' byte order.
