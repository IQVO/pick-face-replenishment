---
id: bounded-context-canvas
title: Bounded context canvas
sidebar_label: Bounded context canvas
sidebar_position: 12
---

# Bounded context canvas

ddd-crew [Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas), filled from the code on
`develop` and ADR 0001.

## Canvas at a glance

```mermaid
flowchart LR
    classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
    classDef actor fill:#fff4e6,stroke:#e8590c,color:#000
    classDef planned fill:#f8f9fa,stroke:#adb5bd,stroke-dasharray:5 5,color:#495057
    classDef bc fill:#fff3bf,stroke:#e67700,stroke-width:1.5px,color:#000
    subgraph IN["Inbound"]
        planner(["Planner, console, agent"]):::actor
        inv["inventory-storage"]:::ctx
        slot["slotting-optimization"]:::ctx
        lay["facility-layout"]:::ctx
    end
    subgraph BC["pick-face-replenishment - Supporting - WMS tier"]
        core["policy, request, detector<br/>local copies of levels, forward map, layout"]:::bc
    end
    subgraph OUT["Outbound"]
        wp["wes-work-planning (planned)"]:::planned
        ana["analytics topic (no consumer)"]:::planned
        ops["ops agent, console (later phase)"]:::planned
    end
    planner -->|"REST: policies, requests, evaluate"| core
    inv -->|"Kafka: BinStockLevelChanged, StockRelocated"| core
    slot -->|"Kafka: SlotPlanApproved"| core
    lay -->|"Kafka: Zone and LocationSlot events"| core
    core -->|"Kafka: ReplenishmentRequested, Completed, Cancelled"| wp
    core -->|"Kafka: request events plus PolicyDeclared"| ana
    core -->|"REST reads"| ops
```

Source: `internal/adapters/inbound/http/server.go`, `internal/adapters/inbound/kafka/*_consumer.go`,
`internal/adapters/outbound/events/encoder.go`, ADR 0001 canvas. Dashed boxes are edges with no consumer or caller yet.

## Name

pick-face-replenishment

## Purpose

Keep every forward pick face stocked between its minimum and maximum by requesting a move from the best reserve bin, and
close the request when the ledger records the move.

## Strategic classification

Supporting subdomain, WMS tier ([reasoning](/docs/ddd/subdomain-classification)). Business model: cost reduction (no
empty forward pick face, fewer short picks). Evolution: custom-built (genesis).

## Domain roles

- **Policy holder**: owns the min/max rule per site and SKU.
- **Work generator**: raises replenishment requests from levels, from a new forward map, from the sweep, or by hand.
- **Closer**: completes a request from the ledger's `StockRelocated`.

## Inbound communication

| Message | Type | Sender | Channel | Collaborator behaviour |
| --- | --- | --- | --- | --- |
| `PUT /policies/{siteId}/{sku}` | command | planner, console | REST | declare or change a policy (`If-Match`) |
| `DELETE /policies/{siteId}/{sku}` | command | planner | REST | remove a policy, cancel its open requests |
| `POST /replenishments` | command | planner, agent | REST | raise a request by hand (`Idempotency-Key`) |
| `POST /replenishments/{requestId}/cancel` | command | planner | REST | cancel an open request (`Idempotency-Key`) |
| `POST /sites/{siteId}/evaluate` | command | planner, agent | REST | sweep a site (`Idempotency-Key`) |
| `GET /policies`, `/policies/{siteId}/{sku}`, `/pick-faces`, `/replenishments`, `/replenishments/{requestId}` | query | planner, console, agent | REST | read |
| `com.warehouse.wms.inventory-storage.stock.BinStockLevelChanged` | event | inventory-storage (producer absent today) | Kafka `warehouse.inventory.events` | absolute bin level, versioned |
| `com.warehouse.wms.inventory-storage.stock.StockRelocated` | event | inventory-storage | Kafka `warehouse.inventory.events` | completes a request when reason is `REPLENISHMENT` |
| `com.warehouse.wms.slotting-optimization.slotplan.SlotPlanApproved` | event | slotting-optimization | Kafka `warehouse.slotting-optimization.events` | full forward map |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | event | facility-layout | Kafka `warehouse.facility.events` | zone code per zone |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | event | facility-layout | Kafka `warehouse.facility.events` | location, its zone and role |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | event | facility-layout | Kafka `warehouse.facility.events` | location retired |

## Outbound communication

| Message | Type | Receiver | Channel |
| --- | --- | --- | --- |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentRequested` | event | wes-work-planning (planned) | Kafka `warehouse.pick-face-replenishment.events` and `.analytics` |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCompleted` | event | anyone | Kafka `...events` and `...analytics` |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCancelled` | event | anyone | Kafka `...events` and `...analytics` |
| `com.warehouse.wms.pick-face-replenishment.policy.ReplenishmentPolicyDeclared` | event | analytics | Kafka `...analytics` only |
| REST responses | query results | planner, console, agent | REST |
| `<topic>.dlq` dead letters | event | operators | Kafka |

## Ubiquitous language

See the [glossary](/docs/ddd/ubiquitous-language). Key terms: pick face, forward bin, reserve bin, policy (min, max,
multiple), request, projected level, priority, source bin policy, usable stock.

## Business decisions

- A face is low when `projected <= minQty`, with `projected = on_hand - reserved + open request quantity`.
- How much: up to `maxQty`, rounded down to a multiple, capped at the source's usable stock.
- Which reserve bin: the one with the most usable stock, ties to the smallest bin code (`fullest-reserve-bin-v1`).
- `URGENT` when nothing is available.
- At most one open request per pick face.
- When a request is cancelled: operator, a newer slot plan that drops the assignment, or policy removal.
- A request completes only on a matching `REPLENISHMENT` relocation.

## Assumptions

- Reserve bins are Storage locations in zones listed in `RESERVE_ZONE_CODES` (default `RSV`); forward faces are in
  `FORWARD_ZONE_CODES` (default `FWD`).
- One SKU per forward slot; the forward bin of a SKU is its slot in the last approved slot plan.
- A bin never seen reads as level 0 (ADR 0005).
- The execution chain (work planning, fulfillment, relocation with reason `REPLENISHMENT`) is planned, not built.

## Verification metrics

Domain coverage gate 90 %; mutation efficacy and mutant coverage thresholds 90 % (measured 100 %); 57 BDD scenarios;
golden bytes per published type; fitness tests ([Testing](/docs/development/testing)). The service exports no runtime
metrics ([Observability](/docs/operations/observability)).

## Open questions

- Producer changes P1 (`BinStockLevelChanged`), P3 and P3b (`REPLENISH` work) and P4 (reason `REPLENISHMENT` on
  `StockRelocated`) are not built, so the loop cannot close (ADR 0001, 0003).
- slotting-optimization's relocation consumer does not filter on `reason`; ADR 0003 records the (unlikely) clash and assigns the
  fix to a slotting-optimization PR.
- No data retention exists for `outbox_events`, `processed_events` and `idempotency_keys` ([Runbook](/docs/operations/runbook#housekeeping)).
