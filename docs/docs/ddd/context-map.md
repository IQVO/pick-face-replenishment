---
id: context-map
title: Context map
sidebar_label: Context map
sidebar_position: 13
---

# Context map

This context's slice of the fleet map, following ddd-crew [Context Mapping](https://github.com/ddd-crew/context-mapping).
Solid edges exist as a producer and a consumer; dotted edges are planned or absent. Relationship patterns are the ones
ADR 0001 decides.

```mermaid
flowchart LR
    classDef self fill:#fff3bf,stroke:#e67700,stroke-width:2px,color:#000
    classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
    PFR["pick-face-replenishment<br/>Supporting, wms"]:::self
    INV["inventory-storage<br/>Core, wms"]:::ctx
    SLOT["slotting-optimization<br/>Supporting, wms"]:::ctx
    LAY["facility-layout<br/>Generic, wms"]:::ctx
    WP["wes-work-planning<br/>Core, wes"]:::ctx
    FE["fulfillment-execution<br/>Core, wes"]:::ctx
    OPS["warehouse-ops-agent, warehouse-console"]:::ctx
    INV -->|"PL: StockRelocated (reason REPLENISHMENT not yet produced)"| PFR
    INV -.->|"PL: BinStockLevelChanged (producer absent)"| PFR
    SLOT -->|"PL: SlotPlanApproved"| PFR
    LAY -->|"OHS and PL: Zone and LocationSlot events"| PFR
    PFR -.->|"PL: ReplenishmentRequested (planned)"| WP
    WP -.->|"WorkReleased with replenishment object (planned)"| FE
    FE -.->|"RelocateStock reason REPLENISHMENT (planned)"| INV
    PFR -.->|"OHS: REST (later phase)"| OPS
```

Source: `docs/adr/0001-pick-face-replenishment-bounded-context.md` (context map table),
`docs/adr/0003-local-copies-and-consumed-contracts.md`, producers' `apis/asyncapi.yaml` on `origin/develop`.
Omits: contexts with no edge to this one. Classifications are the fleet table's.

| Edge | Relationship | Technology | Status (producer side checked on `origin/develop`) |
| --- | --- | --- | --- |
| inventory-storage to this | Published Language; this context is a **Conformist** and keeps a local bin stock copy | Kafka `warehouse.inventory.events`, CloudEvents | `StockRelocated` published (reasons `SLOTTING`, `CONSOLIDATION`, `MANUAL`); `REPLENISHMENT` and `BinStockLevelChanged` absent (changes P4 and P1) |
| slotting-optimization to this | Published Language; **Conformist**, local forward-assignment copy | Kafka `warehouse.slotting-optimization.events` | live |
| facility-layout to this | Open Host Service and Published Language; **Conformist**, local location copy | Kafka `warehouse.facility.events` | live |
| this to wes-work-planning | Published Language (`ReplenishmentRequested`); wes-work-planning would be the Conformist and turn it into `REPLENISH` work | Kafka `warehouse.pick-face-replenishment.events` | planned, not built (P3b); wes-work-planning has only `RELOCATE` work |
| wes-work-planning to fulfillment-execution | `WorkReleased` carries an additive `replenishment` object and creates a `REPLENISH` task | Kafka | planned, not built (P3) |
| fulfillment-execution to inventory-storage | `TaskCompleted` of a `REPLENISH` task becomes `RelocateStock` with reason `REPLENISHMENT` and `source_ref` = the request id | Kafka | planned, not built (P4) |
| this to ops agent, console | Open Host Service | REST, `apis/openapi.yaml` | no caller yet; no Kong route in `warehouse-infra` |

Rule of the map: **no live REST or MCP call between contexts**. Every edge is an event, and this context's copies of
other contexts' data are rebuilt from the topics.

An anticorruption layer is not needed on any inbound edge: payloads are restated in `internal/adapters/inbound/kafka` and
read only for the fields this context uses; unknown fields and unknown types are ignored.
