# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-17).

## Context

Every Kafka message in the fleet is a CloudEvents 1.0 event in structured mode
(fleet rule `.claude/rules/fleet/cloudevents.md`, warehouse-docs
`docs/strategic-design/event-standard-cloudevents.md`). This ADR is the
catalogue of every type pick-face-replenishment publishes or consumes;
`TestEventCatalogueMatchesContract` checks it against `apis/asyncapi.yaml`.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID persisted with the outbox row (stable across
  relay retries; consumers dedupe on it); `source`
  `/warehouse/pick-face-replenishment`; `subject` the request id
  (`repl-<uuid>`), or `<site_id>/<sku>` for the policy event; `time` the
  occurred-at instant in UTC; `datacontenttype` `application/json`;
  `dataschema`
  `urn:warehouse:pick-face-replenishment:<events|analytics>:<EventName>:v1`.
- Kafka value = the JSON event format; header
  `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key = the
  subject, so all events of one request stay ordered on one partition.
- `data` is snake_case; optional fields are omitted when unset; timestamps are
  RFC 3339 UTC.
- Built and decoded only through `internal/adapters/kafka/cloudevents`.

### Published

`<entity>` is the raising aggregate, lowercase, no separators: `replenishment`
or `policy`. The stream column says where the type goes:
`events` is `warehouse.pick-face-replenishment.events`, `analytics` is
`warehouse.pick-face-replenishment.analytics`.

| type | stream | raised when | `data` |
|---|---|---|---|
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentRequested` | (events + analytics) | detection (or a manual request) opens a request | `{request_id, site_id, sku, forward_bin, source_bin, quantity, priority, policy_min, policy_max, requested_at}` |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCompleted` | (events + analytics) | a matching `StockRelocated` closes the request | `{request_id, site_id, sku, forward_bin, source_bin, quantity, completed_at}` |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCancelled` | (events + analytics) | an open request is cancelled | `{request_id, site_id, sku, reason, cancelled_at}` |
| `com.warehouse.wms.pick-face-replenishment.policy.ReplenishmentPolicyDeclared` | (analytics) | a min/max policy is declared or changed | `{site_id, sku, min_qty, max_qty, multiple, version, declared_at}` |

Payload rules:

- `priority` is `NORMAL` or `URGENT`; `URGENT` when `on_hand - reserved <= 0`
  at detection. `policy_min` and `policy_max` are the policy values the request
  was sized with.
- `ReplenishmentCompleted.quantity` is what `StockRelocated` moved, which can be
  less than the requested quantity.
- `ReplenishmentCancelled.reason` is `OPERATOR`, `ASSIGNMENT_CHANGED` or
  `POLICY_REMOVED`.
- `ReplenishmentPolicyDeclared` is key and subject `<site_id>/<sku>`; removing
  a policy raises no event of its own (its open requests are cancelled with
  `POLICY_REMOVED`).
- Analytics copies carry the same `type` and the same `id` per occurrence, with
  `dataschema` `urn:warehouse:pick-face-replenishment:analytics:<EventName>:v1`.
- A breaking payload change is a new `.v2` type and dataschema, never a
  mutation of v1.

### Consumed (ADR 0003)

| type | topic | consumer group env |
|---|---|---|
| `com.warehouse.wms.inventory-storage.stock.BinStockLevelChanged` | `warehouse.inventory.events` | `INVENTORY_CONSUMER_GROUP` |
| `com.warehouse.wms.inventory-storage.stock.StockRelocated` | `warehouse.inventory.events` | `INVENTORY_CONSUMER_GROUP` |
| `com.warehouse.wms.slotting-optimization.slotplan.SlotPlanApproved` | `warehouse.slotting-optimization.events` | `SLOTTING_CONSUMER_GROUP` |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | `warehouse.facility.events` | `LAYOUT_CONSUMER_GROUP` |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `warehouse.facility.events` | `LAYOUT_CONSUMER_GROUP` |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | `warehouse.facility.events` | `LAYOUT_CONSUMER_GROUP` |

Every other type on those topics is ignored. A message that is not a valid
CloudEvent is logged and skipped, never retried forever.

## Consequences

- Consumers need nothing but this catalogue and `apis/asyncapi.yaml`; there is
  no shared Go code.
- `ReplenishmentRequested` is the only type that asks the floor to do
  something; wes-work-planning turns it into `REPLENISH` work (a planned edge,
  ADR 0001).
- `BinStockLevelChanged` is pinned consumer-first: inventory-storage does not
  publish it yet (producer change P1), and `REPLENISHMENT` as a `StockRelocated`
  reason arrives with producer change P4.

## Amendment 1: the analytics stream has a reader (ADR 0008)

The published catalogue above is unchanged. The analytics stream is consumed by
this service's own `cmd/pfr-projector` ([ADR 0008](0008-analytics-read-side.md)),
so the consumed catalogue gains these lines (the same four types as the
published table, read from the analytics stream; `dataschema`
`urn:warehouse:pick-face-replenishment:analytics:<EventName>:v1`):

| type | topic | consumer group env |
|---|---|---|
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentRequested` | `warehouse.pick-face-replenishment.analytics` | `ANALYTICS_CONSUMER_GROUP` |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCompleted` | `warehouse.pick-face-replenishment.analytics` | `ANALYTICS_CONSUMER_GROUP` |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCancelled` | `warehouse.pick-face-replenishment.analytics` | `ANALYTICS_CONSUMER_GROUP` |
| `com.warehouse.wms.pick-face-replenishment.policy.ReplenishmentPolicyDeclared` | `warehouse.pick-face-replenishment.analytics` | `ANALYTICS_CONSUMER_GROUP` |

Its dead-letter topic is `warehouse.pick-face-replenishment.analytics.dlq`. The
analytics stream is not an integration contract: no other context consumes it.

