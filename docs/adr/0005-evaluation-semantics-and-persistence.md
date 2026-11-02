# ADR 0005: evaluation semantics and persistence

## Status

Accepted (2026-10-17). The in-memory development mode and `INVENTORY_MODE=permissive`
mentioned below are superseded by
[ADR 0006](0006-postgres-and-kafka-are-the-only-runtime.md): the binary runs on
Postgres and Kafka only.

## Context

ADRs 0002 to 0004 fix the aggregates, the local copies and the event contract.
Building the service on them left four decisions open that every later step
depends on: when and how the detector runs over the event-fed copies, what an
unknown bin level means, what to do with a forward map that arrives out of
order, and how all of this is stored. They are recorded together because the
database schema is where each one becomes enforceable.

## Decision

### Evaluation semantics

- **One detector, many triggers.** Every caller goes through the same
  `Evaluator`, so the sweep, the consumers, the manual request and the
  pick-face view cannot disagree: `POST /sites/{siteId}/evaluations` (every
  forward assignment of the site), a bin level that was applied (that one pick
  face), a new or moved forward assignment (those pick faces), and a manual
  request. `GET /pick-faces` runs the same detector read-only.
- **Raise at or below the minimum.** With `projected = on_hand - reserved +
  open inbound`, a request is raised when `projected <= min_qty`. The open
  request's quantity counts as inbound, so a second evaluation raises nothing
  while a request is open. A pick face is a forward assignment whose location
  is not decommissioned and is not known to sit outside a forward zone; a
  location whose zone has not arrived yet counts as a pick face (permissive
  until the layout copy converges).
- **An unseen bin reads as level 0.** `BinLevel` returns an all-zero level at
  version 0, not an error and not "unknown". A slot the approved plan has just
  assigned is empty and nothing happens to an empty slot, so inventory-storage
  never publishes a `BinStockLevelChanged` for it. Treating "no row" as unknown
  would mean the emptiest pick faces are the only ones never replenished.
  Reading it as zero raises an URGENT request for such a face as soon as a
  policy is declared and a reserve bin holds stock. A face that really holds
  stock but whose level message has not arrived yet can therefore get a request
  that turns out to be unnecessary; this is accepted because the cost is one
  trip and the next level message, which carries absolute levels, corrects the
  copy. The consumers replay from the start of the topic in `kafka` mode, so the
  levels converge after any restart or rebuild.
- **Levels are absolute and ordered by `version`.** A level message replaces the
  stored one only when its `version` is greater (equal or lower is a no-op,
  outcome `stale`). The bin-level consumer ignores `reason`: the level is
  already the result of whatever happened.
- **Reserve bins come from the layout copy.** A reserve bin is a known,
  non-decommissioned Storage location (an absent role is Storage) of the same
  site whose zone code is in `RESERVE_ZONE_CODES` (default `RSV`); a pick face
  is in a forward zone (default `FWD`). Both sets are passed in as a config
  object, never read from a global.

### The forward map only moves forward in time

`SlotPlanApproved.assignments` is the FULL forward map of a site, applied
wholesale. Kafka redelivery, a consumer-group rebalance or a replay from the
start can deliver an older plan after a newer one, and applying it would bring
back assignments the newer plan dropped and cancel the requests raised for the
right bins. The service therefore stores, per site, the `plan_id` and
`approved_at` of the map it holds (`forward_assignment_maps`) in the same step
that replaces the assignments. `ApplyForwardAssignmentMap` ignores a message
whose `approved_at` is **not after** the recorded one: older is a late
delivery, equal is the same map again and an idempotent no-op. The outcome is
`stale`, the CloudEvents id is still claimed (so a redelivery is a `duplicate`)
and nothing else changes. The first map of a site is never stale. `plan_id` and
`approved_at` are required in the consumed contract, so a message without them
is invalid and skipped like any other malformed event.

### Persistence

PostgreSQL through `pgx/v5`, migrations by golang-migrate. The SQL files live in
the top-level `migrations/` directory, embedded in the binary by package
`migrations`, so the image needs no files on disk. They are additive: a change
is a new numbered pair, an existing migration is never edited, and each `down`
recreates the previous schema exactly. The binary reads `MIGRATIONS_DATABASE_URL`
and falls back to `DATABASE_URL`, so a deployment behind PgBouncer can run DDL
over a direct connection.

| table | key | notes |
|---|---|---|
| `policies` | `(site_id, sku)` | `min_qty < max_qty`, `multiple >= 1`, `version` guards updates and deletes |
| `replenishment_requests` | `request_id` | the whole snapshot, `version` guards updates; **partial unique index** `(site_id, forward_bin, sku) WHERE state = 'Requested'` is invariant 1 |
| `bin_stock_levels` | `(site_code, bin, sku)` | absolute `on_hand`, `reserved`, and the `version` of the last message |
| `forward_assignments` | `(site_id, sku)` | the site's map, replaced wholesale |
| `forward_assignment_maps` | `site_id` | `plan_id`, `approved_at`: the stale-map guard |
| `zones`, `locations` | `zone_id`, `location_code` | layout copy; a location may arrive before its zone and is joined at read time; `decommissioned` is irreversible |
| `processed_events` | `(consumer, event_id)` | the consumer idempotency claim |
| `outbox_events` | `id` | the transactional outbox, same shape as the fleet's |
| `idempotency_keys` | `key` | answers of POSTs made with an `Idempotency-Key` (2xx and 4xx only) |

- **One unit of work, one transaction.** `UnitOfWork.Do` begins a pgx
  transaction and carries it in the context; every repository joins it, and a
  nested `Do` joins the outer one. Aggregate save, outbox rows and the
  `processed_events` claim commit or roll back together, which is what makes a
  consumer effect and its claim atomic: a rolled-back effect un-claims the id,
  so the redelivery is processed, not skipped.
- **Optimistic concurrency returns the domain error.** A version-guarded write
  that matches no row is `ErrConcurrentModification`; a second `Requested`
  request for the same pick face hits the partial unique index and is
  `ErrOpenRequestExists`; an insert over an existing policy is
  `ErrPolicyAlreadyExists`. The database is the final arbiter, so two racing
  sweeps cannot both raise a request.
- **Byte order.** Keys that Go orders or compares (`request_id`, SKUs, bins,
  site ids) use the `"C"` collation, so `ORDER BY` and keyset pages follow the
  same order as the in-memory adapters. Timestamps are `timestamptz`
  (microsecond precision) read back in UTC.
- **Outbox.** `outbox_events` stores the already-encoded CloudEvents message
  (id, topic, type, subject, key, dataschema, value, headers) with `attempts`,
  `last_error`, `created_at` and `published_at`. Rows are claimed with `FOR
  UPDATE SKIP LOCKED` in id order; a row at the attempt cap
  (`OUTBOX_MAX_ATTEMPTS`) is excluded from claims but kept for inspection. The
  relay and the mapping from domain events to CloudEvents arrive with the events
  adapter and follow ADR 0004.

## Consequences

- A newly assigned empty pick face is replenished without waiting for an event
  that will never come; the price is the possible spurious request described
  above, and a deployment that runs with `INVENTORY_MODE=permissive` before
  producer change P1 ships sees every policy-backed pick face as level 0.
- The stale-map guard compares only `approved_at`. Two plans of one site
  approved in the same instant are indistinguishable, and the first one
  delivered wins; slotting-optimization allows one Approved plan per site, so
  this needs a clock tie, not a race.
- Replaying a topic is safe: levels converge through their versions, maps
  through `approved_at`, everything else through `processed_events`.
- The schema is plain relational tables with no ORM, and the in-memory
  adapters and the Postgres adapters run the same behavioural contract tests,
  so `go run ./cmd/api` without `DATABASE_URL` behaves like production for
  everything but durability.
