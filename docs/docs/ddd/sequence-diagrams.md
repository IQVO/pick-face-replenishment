---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
sidebar_position: 19
---

# Sequence diagrams

UML sequence diagrams of the runtime paths, including the error branches.

## 1. Manual request with an Idempotency-Key

```mermaid
sequenceDiagram
    autonumber
    actor C as Client
    participant H as HTTP adapter (Idempotency middleware)
    participant U as RaiseManualRequest
    participant DB as PostgreSQL
    C->>H: POST /replenishments with Idempotency-Key and body
    alt no key or key over 128 characters
        H-->>C: 400 idempotency-key-required
    else key already stored
        alt same method, path and body
            H-->>C: replay the stored answer (2xx or 4xx)
        else different request
            H-->>C: 422 idempotency-key-reused
        end
    else new key
        H->>DB: begin unit of work
        H->>U: handle the request
        U->>DB: read forward bin, policy, open request, levels, reserve bins
        alt no forward bin
            U-->>H: 422 no-forward-assignment
        else no policy
            U-->>H: 422 no-policy
        else a request is open
            U-->>H: 409 replenishment-already-open
        else raised
            U->>DB: insert request and outbox rows
            U-->>H: 201 request
        end
        alt answer is 2xx
            H->>DB: save the answer and commit
            H-->>C: 201 with Location
        else answer is 4xx
            H->>DB: roll back the effects
            H->>DB: store the rejection on its own
            H-->>C: the rejection
        else answer is 5xx
            H->>DB: roll back, store nothing
            H-->>C: 500 internal-error
        end
    end
```

Source: `internal/adapters/inbound/http/idempotency.go`, `internal/application/usecases/raise_manual_request.go`.

## 2. A consumer handles one message

```mermaid
sequenceDiagram
    autonumber
    participant K as Kafka topic
    participant L as consume loop
    participant C as Consumer.HandleMessage
    participant UC as consumer use case
    participant DB as PostgreSQL
    participant D as topic.dlq
    K->>L: FetchMessage
    L->>C: handle value
    alt not a valid CloudEvent
        C-->>L: nil (WARN, skipped)
    else type has no handler
        C-->>L: nil (ignored)
    else handled type
        C->>UC: Handle(event id, payload)
        UC->>DB: begin, claim (consumer, event id)
        alt already claimed
            UC-->>C: outcome duplicate
        else new
            UC->>DB: apply the effect, commit
            UC-->>C: outcome applied, stale or ignored
        end
        alt payload can never be applied
            C-->>L: poison error
            L->>D: publish with x-dlq headers, no retry
        else transient failure
            UC->>DB: roll back, the claim goes with it
            C-->>L: error
            L->>L: retry the same message, backoff 200 ms doubling to 5 s
            L->>D: after the 5th failed attempt publish with x-dlq headers
        end
    end
    L->>K: CommitMessages only after success or dead-letter
```

Source: `internal/adapters/inbound/kafka/{consumer,kafka,deadletter}.go`, `internal/application/usecases/intake.go`.
A handler that failed because the context was cancelled is never dead-lettered and its offset stays uncommitted.

## 3. The outbox relay

```mermaid
sequenceDiagram
    autonumber
    participant UC as use case
    participant DB as PostgreSQL
    participant R as outbox.Relay
    participant S as kafka.RelaySink
    participant K as Kafka
    UC->>DB: aggregate save and outbox insert in one transaction
    loop every OUTBOX_RELAY_INTERVAL
        R->>DB: claim up to 100 unpublished rows, FOR UPDATE SKIP LOCKED
        loop each row in id order
            R->>S: send one message to its own topic
            S->>K: write with acks all
            alt ack
                R->>DB: mark published
            else error
                R->>DB: attempts + 1, last_error, commit what was sent
                R->>R: stop the pass, retry after the interval
                Note over R: at the attempt cap the row is logged once and no longer claimed
            end
        end
    end
```

Source: `internal/adapters/outbound/outbox/relay.go`, `internal/adapters/outbound/postgres/outbox_repository.go`,
`internal/adapters/outbound/kafka/relay_sink.go`.

## 4. Boot and shutdown

```mermaid
sequenceDiagram
    autonumber
    participant O as Orchestrator
    participant A as cmd/api
    participant DB as PostgreSQL
    O->>A: start
    A->>DB: run embedded migrations (retry 5 times over about 15 s)
    A->>DB: ping (same retry)
    alt database never reachable
        A-->>O: exit 1 with the last error, never listens
    else ready
        A->>A: start consumers and relay (Kafka is dialled lazily)
        A->>A: listen on HTTP_ADDR, /readyz is 200
    end
    O->>A: SIGTERM
    A->>A: /readyz flips to 503
    A->>A: wait SHUTDOWN_DRAIN_DELAY
    A->>A: drain HTTP (10 s), stop consumers, stop the relay last
    A->>DB: close the pool
    A-->>O: exit
```

Source: `cmd/api/main.go`, `cmd/api/adapters.go`, `internal/bootretry/bootretry.go`.
