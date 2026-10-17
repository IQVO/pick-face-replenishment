# ADR 0002: the ReplenishmentPolicy and ReplenishmentRequest aggregates, their invariants, and the detection policy ports

## Status

Accepted (2026-10-17).

## Context

ADR 0001 gives this context two jobs: hold the min/max rule of each pick face,
and track each decision to move stock until the ledger records it. Stock levels,
slot assignments and locations are owned by other contexts and arrive as events
(ADR 0003); they are read models here, not aggregate state.

## Decision

Domain code lives under `internal/domain/` and imports nothing outside
`internal/domain/`. Four packages: `policy`, `replenishment`, `detection`,
`shared` (the ECMA-262 blank-string rule that matches the contract's
`pattern: '\S'`).

### ReplenishmentPolicy (package `policy`)

A versioned value object, one per (site, SKU): `{SiteID, SKU, MinQty, MaxQty,
Multiple, Version}`.

- Invariants: site and SKU non-blank; `0 <= MinQty < MaxQty`; `Multiple >= 1`;
  `Version >= 1`. `New` declares version 1, `Restore` re-validates a persisted
  row, `WithBounds` returns a new value at `Version + 1` (the receiver is left
  untouched), which backs the REST `If-Match` optimistic concurrency.
- `RoundDown(n)` returns the largest multiple of `Multiple` not above `n`, and 0
  when `n` is below one multiple. A request quantity is rounded DOWN so it never
  overfills the face; with `Multiple == 1` the floor is 1 unit. A need smaller
  than one multiple raises no request.
- `Declaration(at)` is the fact behind the analytics-only
  `ReplenishmentPolicyDeclared` event.
- v1 policy kind is explicit MIN_MAX only. A demand-cover policy is a planned
  version behind the same shape.

### ReplenishmentRequest (package `replenishment`, the richest aggregate)

One decision to move `quantity` units of one SKU from one reserve bin to one
forward bin. States: `Requested -> Completed | Cancelled`; `Completed` and
`Cancelled` are terminal. `Version` is 1 when planned and +1 per transition.
`Plan`, `Complete`, `Cancel`, `Snapshot` and `Restore` are the only ways in;
`Restore` validates everything a snapshot can show, so a corrupt row never
becomes a live aggregate.

Invariants:

1. At most ONE open request per (site, forward bin, SKU). The rule spans
   requests, so the use case enforces it and a partial unique index backs it
   (wave 2).
2. `quantity >= 1` and `projected + quantity <= policy max`, where
   `projected = on_hand - reserved + open_inbound` at detection (checked by
   `Plan`).
3. `source != forward bin`, same site, the source is a reserve-zone bin, and
   `quantity <= source usable` at planning (checked by `Plan`, from the `Source`
   the application layer builds out of its copies).
4. Completion only by a `StockRelocated` whose `source_ref` equals the request
   id, whose `reason` is `REPLENISHMENT`, and whose SKU, from bin and to bin
   match the request; the relocated quantity (at least 1, possibly less than
   requested) is recorded as moved (checked by `Complete`).
5. A request is cancelled with reason `ASSIGNMENT_CHANGED` when a newer approved
   slot plan no longer assigns the SKU to that forward bin; the use case calls
   `Cancel`. The other reasons are `OPERATOR` and `POLICY_REMOVED`. `Cancel`
   rejects an unknown reason.

Priority is `URGENT` when the face had nothing available (`on_hand - reserved
<= 0`) at detection, else `NORMAL`.

The aggregate raises plain domain events (`ReplenishmentRequested`,
`ReplenishmentCompleted`, `ReplenishmentCancelled`) that carry the request id,
site, the version after the change and the caller's time. They carry no
CloudEvents or Kafka type; the outbound adapter maps them to the wire (ADR 0004).

### Levels are not aggregate state

`bin_stock_levels`, `forward_assignments` and `locations` are event-fed read
models (ADR 0003). The application layer reads them, builds a `detection.Input`
and, when the verdict is `Raise`, a `replenishment.PlanInput`.

### Detection (package `detection`)

A pure function `Detect(Input, SourceBinPolicy) Outcome`: no I/O, no clock, no
randomness; the same input in any slice order gives the same outcome. The input
is the pick face (forward bin, SKU, on hand, reserved), its policy (or none),
the open request for the face (or none) and the candidate reserve bins. The
caller filters reserve bins by site and reserve zone; the detector only ranks.

Rules, in order:

1. `Available = OnHand - Reserved`; `Projected = Available + open request
   quantity` (0 without one).
2. No policy: verdict `NoPolicy`.
3. A request is already open: `AlreadyOpen`.
4. `Projected > MinQty`: `Healthy`.
5. Otherwise `need = MaxQty - Projected`, rounded down to a multiple; the source
   bin is chosen by the `SourceBinPolicy`; the quantity is capped at the source's
   usable stock and rounded down again. No source, or a quantity below 1:
   `NoSource`.
6. Else `Raise` with the source bin, quantity, priority and the policy min and
   max the request was sized with.

`GET /pick-faces` derives its `status` from the same outcome: `NO_POLICY` for
`NoPolicy`, `NO_SOURCE` for `NoSource`, and otherwise `BELOW_MIN` when
`Projected <= MinQty` (a `Raise`, or an `AlreadyOpen` face still at or below
min) and `OK` when `Projected > MinQty`.

### Policy ports (versioned, replaceable)

- `SourceBinPolicy` (`Name()`, `Choose(forwardBin, reserve) (ReserveBin, bool)`).
  v1 is `fullest-reserve-bin-v1`: among reserve bins other than the forward bin
  with usable stock (`on_hand - reserved`) of at least 1, choose the largest
  usable stock; ties go to the lexically smallest bin code (then the largest on
  hand, so even a repeated bin code is order-independent). It allows a partial
  fill (`min(need, source usable)`), gives the fewest trips, and is
  deterministic and explainable.
- The MIN_MAX rule is the `Policy` value itself; a demand-cover version would be
  a second policy kind with its own ADR.

## Consequences

- Every decision a planner questions ("why this bin, why this quantity, why
  URGENT") is answered by the request's own fields and the detector rules above.
- Mutation testing covers every domain package (Makefile `mutation` target and
  the `mutation-fast` CI step loop over `policy`, `replenishment`, `detection`
  and `shared`); thresholds are in `.gremlins.yaml`.
- The one-open-request-per-face rule is not in the aggregate: wave 2 enforces it
  in the use case and with a partial unique index.
- A partial fill leaves the face below max; the next detection sweep raises a
  new request once the first one closes.
