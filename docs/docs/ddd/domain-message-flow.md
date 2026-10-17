---
id: domain-message-flow
title: Domain message flow
sidebar_label: Domain message flow
sidebar_position: 15
---

# Domain message flow

The key business scenarios as numbered command, event and query flows, following ddd-crew
[Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling). Edges that exist on `develop`
are solid arrows; a dashed arrow marks a step whose producer or consumer is not built. Built does not mean deployed:
nothing runs in `warehouse-infra` for this service.

## 1. Keep the local copies current

```mermaid
sequenceDiagram
    autonumber
    participant FL as facility-layout
    participant SO as slotting-optimization
    participant IS as inventory-storage
    participant PFR as pick-face-replenishment
    FL->>PFR: ZoneRegistered (event) on warehouse.facility.events
    PFR->>PFR: ApplyZone upserts zones
    FL->>PFR: LocationSlotRegistered (event)
    PFR->>PFR: ApplyLocationRegistered upserts locations
    SO->>PFR: SlotPlanApproved (event) on warehouse.slotting-optimization.events
    PFR->>PFR: ApplyForwardAssignmentMap replaces the map if approved_at is newer
    IS->>PFR: BinStockLevelChanged (event) on warehouse.inventory.events
    PFR->>PFR: ApplyBinLevel upserts the level if version is newer, then evaluates the face
```

Source: `internal/adapters/inbound/kafka/{layout,slotting,inventory}_consumer.go`,
`internal/application/usecases/{apply_layout,apply_forward_assignments,apply_bin_level}.go`.

## 2. Declare a policy and sweep a site

```mermaid
sequenceDiagram
    autonumber
    actor P as Planner
    participant PFR as pick-face-replenishment
    participant K as warehouse.pick-face-replenishment.events
    P->>PFR: PUT /policies/SITE-1/SKU-1 (command DeclarePolicy)
    PFR-->>P: 200 policy, ETag 1
    PFR->>PFR: outbox row ReplenishmentPolicyDeclared on the analytics topic
    P->>PFR: GET /pick-faces?siteId=SITE-1 (query)
    PFR-->>P: faces with status BELOW_MIN, OK, NO_SOURCE or NO_POLICY
    P->>PFR: POST /sites/SITE-1/evaluate with Idempotency-Key (command EvaluateSite)
    PFR->>PFR: detect per face, plan a request for each Raise
    PFR-->>P: 200 summary with requestIds
    PFR->>K: ReplenishmentRequested (event, via the outbox relay)
```

Source: `internal/application/usecases/{declare_policy,list_pick_faces,evaluate_site}.go`,
`internal/adapters/outbound/events/sink.go`.

## 3. Execute and close a request

```mermaid
sequenceDiagram
    autonumber
    participant PFR as pick-face-replenishment
    participant WP as wes-work-planning
    participant FE as fulfillment-execution
    participant IS as inventory-storage
    participant PPM as process-path-management
    participant K as warehouse.pick-face-replenishment.events
    PPM->>WP: ProcessPathCreated REPLENISH (event, catalogue replay) - seeding it is an operator step
    PFR->>WP: ReplenishmentRequested (event), consumed only with REPLENISHMENT_MODE=kafka
    WP->>WP: plan one REPLENISH work unit per request id (ReplenishmentCancelled withdraws it only while Pending)
    WP->>FE: WorkReleased with a replenishment object on warehouse.work-planning.events
    FE->>FE: create the REPLENISH task from WorkReleased.data.replenishment, announce it on TaskAvailable
    FE->>IS: TaskCompleted (event) with request_ref on warehouse.fulfillment.events
    IS->>IS: ApplyReplenishTask relocates the stock with reason REPLENISHMENT, source_ref = request_ref
    IS->>PFR: StockRelocated (event, reason REPLENISHMENT) on warehouse.inventory.events
    IS->>PFR: BinStockLevelChanged (event, reason REPLENISHMENT) for the source bin, then the destination bin
    PFR->>PFR: ApplyRelocation completes the matching open request
    PFR->>K: ReplenishmentCompleted (event, via the outbox relay)
```

Source: ADR 0001 context map, `internal/application/usecases/apply_relocation.go`; wes-work-planning ADR 0042 and
`internal/adapters/inbound/kafka/replenishment_consumer.go`; process-path-management ADR 0036 and `catalogue/paths.json`;
fulfillment-execution ADR 0050 and `apis/asyncapi.yaml` (`TaskAvailable`, `TaskCompleted.request_ref`);
inventory-storage ADR 0040 and 0041 and `apis/asyncapi.yaml`, all on `origin/develop`. Every step exists on `develop`, but
the flow is not live: nothing is deployed, `warehouse-infra` sets no `REPLENISHMENT_MODE` (so wes-work-planning's
consumer, off by default, plans nothing), and until the `REPLENISH` path is seeded in a cluster wes-work-planning
dead-letters the requests.

## 4. A new slot plan changes the forward map

```mermaid
sequenceDiagram
    autonumber
    participant SO as slotting-optimization
    participant PFR as pick-face-replenishment
    participant K as warehouse.pick-face-replenishment.events
    SO->>PFR: SlotPlanApproved (event) with the full map
    PFR->>PFR: stale guard compares approved_at with the recorded stamp
    PFR->>PFR: replace forward_assignments and the stamp in one transaction
    PFR->>PFR: cancel open requests whose bin no longer holds the SKU (ASSIGNMENT_CHANGED)
    PFR->>PFR: evaluate the faces that are new or moved
    PFR->>K: ReplenishmentCancelled and ReplenishmentRequested (events, via the outbox relay)
```

Source: `internal/application/usecases/apply_forward_assignments.go`.

## 5. Cancel and remove

```mermaid
sequenceDiagram
    autonumber
    actor P as Planner
    participant PFR as pick-face-replenishment
    P->>PFR: POST /replenishments/repl-1/cancel with Idempotency-Key (command CancelRequest)
    PFR->>PFR: Requested to Cancelled (OPERATOR) and outbox row
    PFR-->>P: 200 Cancelled request
    P->>PFR: DELETE /policies/SITE-1/SKU-1 with If-Match (command RemovePolicy)
    PFR->>PFR: delete the policy and cancel its open requests (POLICY_REMOVED)
    PFR-->>P: 204
```

Source: `internal/application/usecases/{cancel_request,remove_policy}.go`.
