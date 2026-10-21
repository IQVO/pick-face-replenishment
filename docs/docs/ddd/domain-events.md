---
id: domain-events
title: Domain events
sidebar_label: Domain events
sidebar_position: 20
---

# Domain events

Every event this context publishes or consumes, with the full CloudEvents `type`. The contract is `apis/asyncapi.yaml`;
the envelope (`specversion` 1.0, `id`, `source`, `type`, `subject`, `time`, `datacontenttype` `application/json`,
`dataschema`) is built only by `internal/adapters/kafka/cloudevents`. Payload fields are snake_case and timestamps are
RFC 3339 UTC.

## Published

`dataschema` is `urn:warehouse:pick-face-replenishment:<events|analytics>:<EventName>:v1`; `source` is
`/warehouse/pick-face-replenishment`; the Kafka header `content-type` is
`application/cloudevents+json; charset=UTF-8`.

| Event | Full type | Topics | Key and subject | Raised by |
| --- | --- | --- | --- | --- |
| ReplenishmentRequested | `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentRequested` | `warehouse.pick-face-replenishment.events`, `warehouse.pick-face-replenishment.analytics` | request id | `Request.Plan` via the Evaluator, `RaiseManualRequest`, `EvaluateSite`, `ApplyBinLevel`, `ApplyForwardAssignmentMap` |
| ReplenishmentCompleted | `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCompleted` | events + analytics | request id | `Request.Complete` via `ApplyRelocation` |
| ReplenishmentCancelled | `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCancelled` | events + analytics | request id | `Request.Cancel` via `CancelRequest`, `RemovePolicy`, `ApplyForwardAssignmentMap` |
| ReplenishmentPolicyDeclared | `com.warehouse.wms.pick-face-replenishment.policy.ReplenishmentPolicyDeclared` | analytics only | `<site_id>/<sku>` | `DeclarePolicy` |

### Payloads

| Event | `data` fields |
| --- | --- |
| ReplenishmentRequested | `request_id`, `site_id`, `sku`, `forward_bin`, `source_bin`, `quantity` (at least 1), `priority` (`NORMAL` or `URGENT`), `policy_min`, `policy_max`, `requested_at` |
| ReplenishmentCompleted | `request_id`, `site_id`, `sku`, `forward_bin`, `source_bin`, `quantity` (what the relocation moved, possibly less than requested), `completed_at` |
| ReplenishmentCancelled | `request_id`, `site_id`, `sku`, `reason` (`OPERATOR`, `ASSIGNMENT_CHANGED`, `POLICY_REMOVED`), `cancelled_at` |
| ReplenishmentPolicyDeclared | `site_id`, `sku`, `min_qty`, `max_qty`, `multiple`, `version`, `declared_at` |

Both topics carry the same `type` and the same CloudEvents `id` per occurrence, written in one outbox transaction. A breaking
payload change would be a new `.v2` type and dataschema, never a mutation of v1. Golden files pin the exact bytes
(`internal/adapters/outbound/events/testdata/`).

Consumers of the published events: wes-work-planning reads `ReplenishmentRequested` and `ReplenishmentCancelled` from
`warehouse.pick-face-replenishment.events` when `REPLENISHMENT_MODE=kafka` (its ADR 0042; it ignores
`ReplenishmentCompleted`, and the default `permissive` consumes nothing); `warehouse-infra` sets it on wes-work-planning. The
analytics topic is read only by this repository's `cmd/pfr-projector` (ADR 0008), which `warehouse-infra` does not deploy - see [Integration](/docs/ecosystem/integration).

## Consumed

| Full type | Topic | Key | Fields used | Handler |
| --- | --- | --- | --- | --- |
| `com.warehouse.wms.inventory-storage.stock.BinStockLevelChanged` | `warehouse.inventory.events` | `<bin>:<sku>` | `site_code`, `bin`, `sku`, `on_hand`, `reserved`, `version` | inventory consumer, `ApplyBinLevel` |
| `com.warehouse.wms.inventory-storage.stock.StockRelocated` | `warehouse.inventory.events` | `relocation_id` | `relocation_id`, `site_code`, `sku`, `from_bin`, `to_bin`, `quantity`, `reason`, `source_ref` | inventory consumer, `ApplyRelocation` |
| `com.warehouse.wms.slotting-optimization.slotplan.SlotPlanApproved` | `warehouse.slotting-optimization.events` | plan id | `plan_id`, `site_id`, `approved_at`, `assignments[{sku, slot}]` | slotting consumer, `ApplyForwardAssignmentMap` |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | `warehouse.facility.events` | `zoneId` | `zoneId`, `siteCode`, `zoneCode` | layout consumer, `ApplyZone` |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `warehouse.facility.events` | `locationCode` | `locationCode`, `zoneId`, `role` | layout consumer, `ApplyLocationRegistered` |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | `warehouse.facility.events` | `locationCode` | `locationCode` | layout consumer, `ApplyLocationDecommissioned` |

The payloads of consumed events are restated here from the producers' contracts, never imported; fields not listed are
ignored. Producer status of each type is on [Integration](/docs/ecosystem/integration#upstream-and-downstream-edges).

## Example: ReplenishmentRequested

```json
{
  "specversion": "1.0",
  "id": "6f1d2c3b-4a5e-4b7c-8d9e-0a1b2c3d4e5f",
  "source": "/warehouse/pick-face-replenishment",
  "type": "com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentRequested",
  "subject": "repl-3b0e8d2a-7c41-4f6a-9d15-2a8b6c4e1f07",
  "time": "2026-10-17T09:30:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:pick-face-replenishment:events:ReplenishmentRequested:v1",
  "data": {
    "request_id": "repl-3b0e8d2a-7c41-4f6a-9d15-2a8b6c4e1f07",
    "site_id": "WH1",
    "sku": "SKU-1",
    "forward_bin": "WH1-FWD-A01-01-01-A",
    "source_bin": "WH1-RSV-A07-03-02-B",
    "quantity": 24,
    "priority": "NORMAL",
    "policy_min": 10,
    "policy_max": 60,
    "requested_at": "2026-10-17T09:30:00Z"
  }
}
```

This is the example from `apis/asyncapi.yaml`.
