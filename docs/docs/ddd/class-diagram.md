---
id: class-diagram
title: Class diagrams
sidebar_label: Class diagrams
sidebar_position: 17
---

# Class diagrams

## Domain types

```mermaid
classDiagram
    class Policy {
        +string SiteID
        +string SKU
        +int64 MinQty
        +int64 MaxQty
        +int64 Multiple
        +int64 Version
        +WithBounds(min, max, multiple) Policy
        +RoundDown(n) int64
        +Declaration(at) Declaration
    }
    class Request {
        -string id
        -string siteID
        -string sku
        -string forwardBin
        -string sourceBin
        -int64 quantity
        -int64 movedQuantity
        -Priority priority
        -State state
        -int64 version
        +Plan(PlanInput) Request
        +Complete(Relocation, at) error
        +Cancel(CancelReason, at) error
        +DomainEvents() Event[]
        +Snapshot() Snapshot
    }
    class Event {
        <<interface>>
        +EventName() string
        +RequestID() string
        +OccurredAt() time
    }
    class ReplenishmentRequested
    class ReplenishmentCompleted
    class ReplenishmentCancelled
    class State {
        <<enumeration>>
        Requested
        Completed
        Cancelled
    }
    class Priority {
        <<enumeration>>
        NORMAL
        URGENT
    }
    class CancelReason {
        <<enumeration>>
        OPERATOR
        ASSIGNMENT_CHANGED
        POLICY_REMOVED
    }
    class SourceBinPolicy {
        <<interface>>
        +Name() string
        +Choose(forwardBin, reserve) ReserveBin
    }
    class FullestReserve
    class Detect {
        <<function>>
        Detect(Input, SourceBinPolicy) Outcome
    }
    Request --> State
    Request --> Priority
    Request --> CancelReason
    Request ..> Event : raises
    Event <|.. ReplenishmentRequested
    Event <|.. ReplenishmentCompleted
    Event <|.. ReplenishmentCancelled
    SourceBinPolicy <|.. FullestReserve
    Detect ..> SourceBinPolicy
    Detect ..> Policy
```

Source: `internal/domain/policy/policy.go`, `internal/domain/replenishment/{request,values,events}.go`,
`internal/domain/detection/{detector,source}.go`.

## Ports and adapters

```mermaid
classDiagram
    class PolicyRepo {
        <<interface>>
    }
    class RequestRepo {
        <<interface>>
    }
    class PickFaceReadModel {
        <<interface>>
    }
    class EventSink {
        <<interface>>
    }
    class ProcessedEvents {
        <<interface>>
    }
    class IdempotencyStore {
        <<interface>>
    }
    class UnitOfWork {
        <<interface>>
    }
    class OutboxStore {
        <<interface>>
    }
    class PostgresRepos["postgres: PolicyRepo, RequestRepo, ReadModel, ProcessedEventRepo, IdempotencyStore, UnitOfWork, OutboxRepo"]
    class MemoryRepos["memory: the same ports in memory"]
    class EventsSink["events.Sink (CloudEvents to outbox)"]
    class Relay["outbox.Relay"]
    class RelaySink["kafka.RelaySink"]
    class HTTPServer["inbound http: Server, Idempotency, Readiness"]
    class Consumer["inbound kafka: Consumer (inventory, slotting, layout)"]
    PolicyRepo <|.. PostgresRepos
    RequestRepo <|.. PostgresRepos
    PickFaceReadModel <|.. PostgresRepos
    ProcessedEvents <|.. PostgresRepos
    IdempotencyStore <|.. PostgresRepos
    UnitOfWork <|.. PostgresRepos
    PolicyRepo <|.. MemoryRepos
    RequestRepo <|.. MemoryRepos
    PickFaceReadModel <|.. MemoryRepos
    EventSink <|.. EventsSink
    EventsSink ..> PostgresRepos : Inserter (OutboxRepo)
    OutboxStore <|.. PostgresRepos
    Relay ..> OutboxStore
    Relay ..> RelaySink
    HTTPServer ..> PolicyRepo : through use cases
    Consumer ..> ProcessedEvents : through Intake
```

Source: `internal/application/ports/*.go`, `internal/adapters/outbound/{postgres,memory,events,outbox,kafka}`,
`internal/adapters/inbound/{http,kafka}`, `cmd/api/adapters.go`. The inbound adapters reach the ports only through the use
cases; `cmd/api` is the only place that connects adapters, which `TestHexagonalArchitecture` enforces.
