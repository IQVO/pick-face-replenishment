---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
sidebar_position: 16
---

# EventStorming

A design-level EventStorming of `pick-face-replenishment`, using the sticky colours of the ddd-crew
[EventStorming glossary and cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet): **orange**
domain event, **blue** command, **yellow** aggregate, **lilac** policy, **green** read model, **pink** external system,
**red** hotspot, small yellow actor.

## Detection and request flow

```mermaid
flowchart LR
    classDef event fill:#ff9f43,stroke:#333,color:#000
    classDef command fill:#54a0ff,stroke:#333,color:#000
    classDef aggregate fill:#feca57,stroke:#333,color:#000
    classDef policy fill:#c8a2c8,stroke:#333,color:#000
    classDef readmodel fill:#1dd1a1,stroke:#333,color:#000
    classDef external fill:#ff9ff3,stroke:#333,color:#000
    classDef actor fill:#fffa65,stroke:#333,color:#000
    classDef hotspot fill:#ff6b6b,stroke:#333,color:#000

    planner(["Planner"]):::actor
    declare["Declare policy"]:::command
    pol["ReplenishmentPolicy"]:::aggregate
    declared["Policy declared (analytics)"]:::event
    levels["Bin levels, forward map, layout"]:::readmodel
    sweep["Evaluate site"]:::command
    manual["Request replenishment"]:::command
    detect["Detector: low when projected at or below min"]:::policy
    source["Fullest reserve bin v1"]:::policy
    one["One open request per pick face"]:::policy
    req["ReplenishmentRequest"]:::aggregate
    requested["Replenishment requested"]:::event
    work["wes-work-planning REPLENISH work"]:::external
    hs1["Nobody consumes ReplenishmentRequested yet"]:::hotspot

    planner --> declare --> pol --> declared
    levels --> detect
    pol --> detect
    planner --> sweep --> detect
    planner --> manual --> detect
    source --> detect
    detect --> req
    one --> req
    req --> requested --> work
    work -.-> hs1
```

Source: `internal/application/usecases/{declare_policy,evaluate_site,raise_manual_request,evaluator}.go`,
`internal/domain/detection/{detector,source}.go`, `internal/domain/replenishment/request.go`.

## Completion and cancellation flow

```mermaid
flowchart LR
    classDef event fill:#ff9f43,stroke:#333,color:#000
    classDef command fill:#54a0ff,stroke:#333,color:#000
    classDef aggregate fill:#feca57,stroke:#333,color:#000
    classDef policy fill:#c8a2c8,stroke:#333,color:#000
    classDef external fill:#ff9ff3,stroke:#333,color:#000
    classDef actor fill:#fffa65,stroke:#333,color:#000
    classDef hotspot fill:#ff6b6b,stroke:#333,color:#000

    inv["inventory-storage"]:::external
    slot["slotting-optimization"]:::external
    planner(["Planner"]):::actor
    relocated["Stock relocated (REPLENISHMENT)"]:::event
    match["Complete only on a matching relocation"]:::policy
    approved["Slot plan approved"]:::event
    drop["Cancel requests the new map drops"]:::policy
    cancel["Cancel request"]:::command
    remove["Remove policy"]:::command
    rm["Cancel the policy's open requests"]:::policy
    req["ReplenishmentRequest"]:::aggregate
    completed["Replenishment completed"]:::event
    cancelled["Replenishment cancelled"]:::event

    inv --> relocated --> match --> req --> completed
    slot --> approved --> drop --> req
    planner --> cancel --> req
    planner --> remove --> rm --> req
    req --> cancelled
```

Source: `internal/application/usecases/{apply_relocation,apply_forward_assignments,cancel_request,remove_policy}.go`.

## Local-copy flow

```mermaid
flowchart LR
    classDef event fill:#ff9f43,stroke:#333,color:#000
    classDef readmodel fill:#1dd1a1,stroke:#333,color:#000
    classDef external fill:#ff9ff3,stroke:#333,color:#000
    classDef policy fill:#c8a2c8,stroke:#333,color:#000
    classDef hotspot fill:#ff6b6b,stroke:#333,color:#000

    inv["inventory-storage"]:::external
    slot["slotting-optimization"]:::external
    lay["facility-layout"]:::external
    e1["Bin stock level changed"]:::event
    e2["Slot plan approved"]:::event
    e3["Zone registered"]:::event
    e4["Location slot registered or decommissioned"]:::event
    p1["Apply only a newer version"]:::policy
    p2["Apply only a newer approved_at"]:::policy
    p3["Claim the event id in the same transaction"]:::policy
    r1["bin_stock_levels"]:::readmodel
    r2["forward_assignments and stamp"]:::readmodel
    r3["zones and locations"]:::readmodel
    hs3["Unseen bin reads as level 0"]:::hotspot

    inv --> e1 --> p1 --> r1
    slot --> e2 --> p2 --> r2
    lay --> e3 --> r3
    lay --> e4 --> r3
    p3 --> r1
    p3 --> r2
    p3 --> r3
    r1 -.-> hs3
```

Source: `internal/application/usecases/{apply_bin_level,apply_forward_assignments,apply_layout,intake}.go`,
`internal/adapters/outbound/postgres/read_model.go`.

## Hotspots

| Hotspot | Where it comes from |
| --- | --- |
| Nobody consumes `ReplenishmentRequested` | wes-work-planning has no consumer of `warehouse.pick-face-replenishment.events` and no `REPLENISH` work path, so the loop cannot start ([Integration](/docs/ecosystem/integration)) |
| An unseen bin reads as level 0 | accepted in ADR 0005: a face whose level has not arrived yet can get an unnecessary, `URGENT` request |
| Partial fills leave the face below max | the next detection raises a new request once the first is closed (ADR 0002) |

Two earlier hotspots are closed on `develop`: inventory-storage now publishes `BinStockLevelChanged` (its ADR 0040), and
its `StockRelocated` carries reason `REPLENISHMENT` for a completed `REPLENISH` task (its ADR 0041).
