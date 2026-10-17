# ADR 0003: event-fed local copies and the consumed contracts

## Status

Accepted (2026-10-17). The `permissive` mode and the optional consumers below
are superseded by [ADR 0006](0006-postgres-and-kafka-are-the-only-runtime.md):
`*_MODE` defaults to `kafka`, `permissive` is rejected at boot.

## Context

Detection needs bin stock levels, the forward-slot map and the location map.
Each belongs to another context. The fleet rule (context-boundaries) forbids a
live REST or MCP call between contexts, so pick-face-replenishment keeps durable
local copies fed by Kafka, the same stance as `slotting-optimization`. This ADR
lists what is consumed, verified against each producer's `origin/develop`
`apis/asyncapi.yaml` on 2026-10-17.

## Decision

### Consumed contracts

| producer | topic | full type | used for | status |
|---|---|---|---|---|
| inventory-storage | `warehouse.inventory.events` | `com.warehouse.wms.inventory-storage.stock.BinStockLevelChanged` | `bin_stock_levels` copy | **Pending producer change P1**, pinned consumer-first (below) |
| inventory-storage | `warehouse.inventory.events` | `com.warehouse.wms.inventory-storage.stock.StockRelocated` | completes a request | Verified (ADR 0039 of inventory-storage); reason `REPLENISHMENT` is a **new additive value** (P4) |
| slotting-optimization | `warehouse.slotting-optimization.events` | `com.warehouse.wms.slotting-optimization.slotplan.SlotPlanApproved` | `forward_assignments` copy | Verified |
| facility-layout | `warehouse.facility.events` | `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | zone code per zone | Verified |
| facility-layout | `warehouse.facility.events` | `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `locations` copy | Verified |
| facility-layout | `warehouse.facility.events` | `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | removes a location | Verified |

Payloads are in `apis/asyncapi.yaml`, restated from the producers and never
imported. Every other type on these topics is ignored.

### `BinStockLevelChanged` (producer change P1, pinned here first)

Today inventory-storage publishes no per-bin level: `StockPicked` is
analytics-only and carries no bin. Both sides agree on this contract before the
producer ships (consumer-first): key and subject `<bin>:<sku>`, `data`
`{site_code, bin, sku, on_hand, reserved, version, reason, occurred_at}`;
`on_hand` and `reserved` are ABSOLUTE levels after the change, not deltas;
`reserved` is the units of this bin bound by active reservations; `version` is
a per-(bin, sku) monotonically increasing integer (the consumer keeps the
highest and ignores lower ones); `reason` is `STOW`, `PICK`, `RELOCATE`,
`REPLENISHMENT`, `COUNT`, `RESERVE`, `REVOKE` or `UNLOCATED`, and unknown
values are ignored. It is raised from the same transaction as the ledger change
through inventory-storage's outbox.

### `StockRelocated` and the new reason

inventory-storage's contract today lists `reason` as `SLOTTING`,
`CONSOLIDATION` or `MANUAL`. This service acts only when `reason ==
REPLENISHMENT` (added by producer change P4, when a REPLENISH task completes)
and ignores every other reason. Completion further requires `source_ref` to be a
request id of this service and the SKU, from bin and to bin to match
(ADR 0002, invariant 4).

slotting-optimization's `StockRelocated` consumer (its ADR 0010) matches a
relocation to a slot-plan move by `source_ref`, else by SKU and bins on the
site's current Approved plan, and does **not** filter on `reason`. A
`REPLENISHMENT` relocation could therefore only be mistaken for a plan move if
its SKU and both bins coincide with a pending move, which is unlikely but not
impossible. The producer-side fix (ignore `reason != SLOTTING`) belongs to a
small slotting-optimization PR and is tracked with producer change P4; it is
not made in this repo.

### `SlotPlanApproved`

`assignments` is the FULL forward map of the site: the `forward_assignments`
copy of that site is replaced wholesale from it in one transaction. A request
whose forward bin no longer holds its SKU in the new map is cancelled with
reason `ASSIGNMENT_CHANGED`.

### Local copies

| table | fed by | rule |
|---|---|---|
| `bin_stock_levels` | `BinStockLevelChanged` | apply only when `version` is greater than the stored one for the (bin, SKU) |
| `forward_assignments` | `SlotPlanApproved` | replace the site's rows wholesale |
| `locations` and zone codes | `ZoneRegistered`, `LocationSlotRegistered`, `LocationSlotDecommissioned` | a reserve bin is a Storage location (absent `role` = Storage) in a zone listed in `RESERVE_ZONE_CODES` (default `RSV`); decommission removes the location |

### Consumer rules

- Dispatch on the FULL `type`; ignore unknown types; skip (WARN, commit past)
  anything that is not a valid CloudEvent.
- Dedupe on the CloudEvents `id` in the SAME database transaction as the effect;
  commit the Kafka offset only after success (`FetchMessage` +
  `CommitMessages`).
- Consumer group ids come from env: `INVENTORY_CONSUMER_GROUP`,
  `SLOTTING_CONSUMER_GROUP`, `LAYOUT_CONSUMER_GROUP`. Modes `INVENTORY_MODE`,
  `SLOTTING_MODE`, `LAYOUT_MODE` default to `permissive`; `kafka` without its
  group env is a boot error.
- Never dead-letter on a cancelled context; poison payloads only
  (`//dlq:deterministic`).

### No live lookup

pick-face-replenishment has no outbound HTTP or MCP client. An empty or stale
copy produces `NO_SOURCE` or `NO_POLICY`, never a guess.

## Consequences

- Until P1 ships, `bin_stock_levels` stays empty and detection finds nothing to
  replenish; the sweep endpoint and the pick-face view say so instead of
  failing. Until P4 ships, no relocation carries `REPLENISHMENT`, so requests
  can be raised and cancelled but not completed.
- The consumer-first order means this service accepts P1 and P4 before the
  producer changes; if inventory-storage's final contract differs, the contract
  is fixed here in a follow-up PR.
- Three consumer groups' worth of lag is the cost of keeping the service
  independent of its producers' availability.
