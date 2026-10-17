---
id: events
title: Event catalogue
sidebar_label: Event catalogue
sidebar_position: 2
---

# Event catalogue

Every Kafka message this service produces or consumes is a CloudEvents 1.0 event in **structured mode**: the Kafka
value is the whole JSON envelope and the header `content-type` is
`application/cloudevents+json; charset=UTF-8`. Envelopes are built and decoded only in
`internal/adapters/kafka/cloudevents` (ADR 0004). The contract is `apis/asyncapi.yaml`; field-level detail is on
[Domain events](/docs/ddd/domain-events).

## Published

Integration topic `warehouse.pick-face-replenishment.events` and analytics topic
`warehouse.pick-face-replenishment.analytics`, `source` `/warehouse/pick-face-replenishment`, `subject` and Kafka key =
the request id (`<site_id>/<sku>` for the policy event), `dataschema`
`urn:warehouse:pick-face-replenishment:<events|analytics>:<EventName>:v1`. One CloudEvents `id` is minted per
occurrence and shared by both copies.

| Type | Raised by | Topics | `data` |
| --- | --- | --- | --- |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentRequested` | detection (sweep, consumers) or `POST /replenishments` | events + analytics | `request_id`, `site_id`, `sku`, `forward_bin`, `source_bin`, `quantity`, `priority`, `policy_min`, `policy_max`, `requested_at` |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCompleted` | a matching `StockRelocated` | events + analytics | `request_id`, `site_id`, `sku`, `forward_bin`, `source_bin`, `quantity`, `completed_at` |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCancelled` | cancel (operator, slot plan, policy removal) | events + analytics | `request_id`, `site_id`, `sku`, `reason`, `cancelled_at` |
| `com.warehouse.wms.pick-face-replenishment.policy.ReplenishmentPolicyDeclared` | `PUT /policies/{siteId}/{sku}` | analytics only | `site_id`, `sku`, `min_qty`, `max_qty`, `multiple`, `version`, `declared_at` |

Removing a policy raises no event of its own: its open requests are cancelled with reason `POLICY_REMOVED`.

## Consumed

| Type | Topic | Key | Consumer (mode / group variable) |
| --- | --- | --- | --- |
| `com.warehouse.wms.inventory-storage.stock.BinStockLevelChanged` | `warehouse.inventory.events` | `<bin>:<sku>` | inventory consumer (`INVENTORY_MODE` / `INVENTORY_CONSUMER_GROUP`) |
| `com.warehouse.wms.inventory-storage.stock.StockRelocated` | `warehouse.inventory.events` | `relocation_id` | inventory consumer |
| `com.warehouse.wms.slotting-optimization.slotplan.SlotPlanApproved` | `warehouse.slotting-optimization.events` | plan id | slotting consumer (`SLOTTING_MODE` / `SLOTTING_CONSUMER_GROUP`) |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | `warehouse.facility.events` | `zoneId` | layout consumer (`LAYOUT_MODE` / `LAYOUT_CONSUMER_GROUP`) |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `warehouse.facility.events` | `locationCode` | layout consumer |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | `warehouse.facility.events` | `locationCode` | layout consumer |

Dead-letter topics are `<consumed topic>.dlq` for each of the three consumed topics
([Runbook](/docs/operations/runbook#dead-letter-topics)). Every other type on a consumed topic is ignored.

Producer status of the consumed types today (checked on each sibling's `origin/develop`): see
[Integration](/docs/ecosystem/integration#upstream-and-downstream-edges). In particular inventory-storage publishes
`BinStockLevelChanged` (its ADR 0040), and its `StockRelocated` carries the `REPLENISHMENT` reason for a completed
`REPLENISH` task (its ADR 0041).
