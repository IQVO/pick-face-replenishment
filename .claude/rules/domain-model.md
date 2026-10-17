# Domain model: ubiquitous language, aggregates, events, use cases

pick-face-replenishment is the WMS-tier owner of forward pick-face
replenishment (ADR 0001): it holds a min/max policy per forward pick face,
raises a request to move stock from a reserve bin when the face runs low, and
closes the request when the ledger records the move. It never writes the stock
ledger and never creates floor tasks. Source of truth: `docs/adr/0002-aggregates-and-detection-policy.md`.

## Ubiquitous Language (use these exact names)

- **Pick face / forward bin** — a forward bin holding one SKU, assigned by the
  site's latest approved slot plan. Its level (`OnHand`, `Reserved`) is a read
  model, not aggregate state.
- **Reserve bin** — a Storage location in a zone listed in `RESERVE_ZONE_CODES`
  (default `RSV`); the only legal source of a request.
- **Policy** (`ReplenishmentPolicy`) — explicit MIN_MAX rule per (site, SKU):
  `MinQty`, `MaxQty`, `Multiple`, `Version`.
- **Request** (`ReplenishmentRequest`) — one decision to move `quantity` units of
  one SKU from one reserve bin to one forward bin, and how it ended.
- **Available / Projected** — `Available = OnHand - Reserved`;
  `Projected = Available + the open request's quantity` (inbound not arrived yet).
- **Priority** — `URGENT` when `Available <= 0` at detection, else `NORMAL`.
- **Usable stock** — a reserve bin's `OnHand - Reserved`.
- **SourceBinPolicy** — the port that picks the reserve bin; v1 is
  `fullest-reserve-bin-v1`.
- **Relocation** — the part of inventory-storage's `StockRelocated` that can
  complete a request (reason `REPLENISHMENT`, `source_ref` = request id).

## Aggregates

- **Request** (`internal/domain/replenishment`, type `Request`, the richest
  aggregate): states `Requested -> Completed | Cancelled` (both terminal);
  `Version` is 1 when planned, +1 per transition. Only `Plan`, `Complete`,
  `Cancel`, `Snapshot` and `Restore` build or change it; `Restore` validates
  everything so a corrupt row never becomes a live aggregate. Invariants:
  1. at most ONE open request per (site, forward bin, SKU) — spans requests, so
     the use case enforces it and a partial unique index backs it (not in the aggregate);
  2. `quantity >= 1` and `projected + quantity <= policy max` (`Plan`,
     `ErrQuantityBelowOne`, `ErrExceedsMax`);
  3. source differs from the forward bin, same site, reserve zone, and
     `quantity <= source usable` (`Plan`, `Source`, `ErrSource*`);
  4. completion only by a `Relocation` with reason `REPLENISHMENT`
     (`RelocationReason`), `SourceRef` = request id and matching SKU, from bin
     and to bin (`Complete`); the moved quantity (>= 1, may be less than
     requested) is recorded;
  5. cancel reasons are `OPERATOR`, `ASSIGNMENT_CHANGED`, `POLICY_REMOVED`
     (`CancelReason`); `Cancel` rejects an unknown reason.
- **Policy** (`internal/domain/policy`, type `Policy`): versioned value object,
  one per (site, SKU). Site and SKU non-blank, `0 <= MinQty < MaxQty`,
  `Multiple >= 1`, `Version >= 1`. `New` starts at version 1, `Restore`
  re-validates a row, `WithBounds` returns a new value at `Version + 1`
  (backs REST `If-Match`). `RoundDown(n)` rounds a quantity DOWN to a multiple
  so a request never overfills the face. `Declaration(at)` is the fact behind
  `ReplenishmentPolicyDeclared`.
- **Detector** (`internal/domain/detection`): not an aggregate; the pure
  function `Detect(Input, SourceBinPolicy) Outcome` (no I/O, no clock, same
  input in any slice order gives the same outcome). Verdicts (`Kind`):
  `KindRaise`, `KindHealthy`, `KindAlreadyOpen`, `KindNoPolicy`, `KindNoSource`.
  Order: no policy, open request, `Projected > MinQty` healthy, else
  `need = MaxQty - Projected` rounded down, source chosen and capped at its
  usable stock, rounded down again.
- `internal/domain/shared`: `IsBlank`, the ECMA-262 blank-string rule matching
  the contract's `pattern: '\S'`.

## Domain events

Plain facts raised by `Request` (`internal/domain/replenishment/events.go`),
carrying request id, site, version after the change and the caller's time. They
know nothing of CloudEvents or Kafka; the outbound adapter maps them to the
wire (ADR 0004).

- `ReplenishmentRequested` — raised by `Plan` when a request opens.
- `ReplenishmentCompleted` — raised by `Complete`; carries the moved quantity.
- `ReplenishmentCancelled` — raised by `Cancel`; carries the reason.
- `ReplenishmentPolicyDeclared` — analytics-only; built from
  `Policy.Declaration`, not raised by `Request`.

## Policy ports (versioned, replaceable)

- `SourceBinPolicy` (`Name()`, `Choose(forwardBin, reserve) (ReserveBin, bool)`)
  in `internal/domain/detection/source.go`. `FullestReserve` is
  `fullest-reserve-bin-v1`: largest usable stock (at least 1), ties to the
  lexically smallest bin code; allows a partial fill. A new policy is a new
  type with a new name and an ADR.
- The MIN_MAX rule is the `Policy` value itself; a demand-cover policy would be
  a second policy kind behind the same shape.

## Key use cases

The application layer lives in `internal/application/{ports,repository,usecases}`
(ports are interfaces only; `repository` holds their errors, filters and value
types). Every write runs in ONE `ports.UnitOfWork` and hands the aggregates'
facts to the `EventSink` (outbox) in that same unit. A shared `Evaluator` reads
the local copies of one pick face, runs `detection.Detect` and plans the
request, so the sweep, the consumers, the manual request and the pick-face view
cannot disagree. Use cases (one file each):

- Policy: `DeclarePolicy` (`If-Match` version; analytics fact), `RemovePolicy`
  (cancels the SKU's open requests with `POLICY_REMOVED` in the same unit),
  `GetPolicy`, `ListPolicies`.
- Requests: `RaiseManualRequest`, `CancelRequest` (reason `OPERATOR`),
  `EvaluateSite` (idempotent sweep, one unit per pick face), `GetRequest`,
  `ListRequests`, `ListPickFaces` (status via the read-only detector).
- Consumer effects, each behind `Intake` (claims the CloudEvents id in the same
  unit as the effect): `ApplyBinLevel` (version-guarded, then evaluates the
  SKU's pick face), `ApplyForwardAssignmentMap` (replaces the site's map,
  cancels with `ASSIGNMENT_CHANGED`, evaluates new or moved faces),
  `ApplyZone`, `ApplyLocationRegistered`, `ApplyLocationDecommissioned`,
  `ApplyRelocation` (acts only on reason `REPLENISHMENT` with a matching
  `source_ref`; everything else is ignored without error).
- Forward vs reserve is `usecases.ZoneConfig` (defaults `FWD` / `RSV`, env
  `RESERVE_ZONE_CODES`), passed in; no globals.
- In-memory adapters for every port are in `internal/adapters/outbound/memory`
  (BDD and the unit tests only; the runtime is Postgres + Kafka, ADR 0006).
  Acceptance scenarios are `features/*.feature`, run as `TestFeatures` from
  `features/features_test.go` (package `features_test`; no Go file lives at the
  repo root, `TestNoGoFilesAtRepoRoot`).
- Every use case reads event-fed local copies and calls no sibling context.
