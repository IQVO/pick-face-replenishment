# ADR 0014: Every problem type has a replay class; a retryable refusal is not stored

## Status

Accepted (2026-11-20). Adopts the fleet rule decided by the owner on 2026-11-18 (standard: warehouse-harness-template
`docs/idempotency-replay-classes.md`, rule `.claude/rules/fleet/idempotency-and-outbox.md`). Supersedes the sentence of
[ADR 0011](./0011-adopt-the-fleet-rest-api-guideline.md) item 2 that says a 4xx answer is stored and replayed like a
success (and the "2xx and 4xx only" of the `idempotency_keys` row of [ADR 0005](./0005-evaluation-semantics-and-persistence.md)); the
rest of both stands.

## Context

`Idempotency.Middleware` (`internal/adapters/inbound/http/idempotency.go`) stored the first answer below 500 of every
key, errors included, and replayed it. A refusal that a retry could pass was therefore pinned: a `422 no-policy`
replayed to an operator who declared the policy and retried with the same key, a `409 concurrent-modification`
replayed to a client that did the right thing.

**The store design (read before this change, same lines before and after).** The key row is part of the business
transaction for a 2xx and a separate write for a refusal:

- `serveFresh` runs the handler inside ONE `UoW.Do` (`idempotency.go` 209 ff.). A 2xx is saved by `saveAndReread`
  in that same transaction, so the aggregate, its outbox rows and the stored answer commit or roll back together;
  `Save` is `INSERT ... ON CONFLICT (key) DO NOTHING`, so the first writer wins and a concurrent loser rolls back
  and replays the winner's answer.
- Any other answer returns `errRollback` (`idempotency.go` 217): the WHOLE unit of work is rolled back, then
  `finishRejected` stores a 4xx apart on a fresh statement (`Store.Save` outside any transaction) and a 5xx is
  never stored. Pinned by `cmd/api/idempotency_aborted_integration_test.go`.
- There is no in-flight lock: two requests with one new key both run, the unique index serialises them.
- Consequence for the classes: **releasing is not writing.** A RETRYABLE refusal never held a key row (the row of a
  2xx lives in the rolled-back transaction, the row of a refusal is a write that is simply skipped), so the key is
  free the instant the answer leaves. **OUTCOME cannot exist** (section 3).
- MCP and A2A are read-only (ADR 0007, ADR 0010, every tool `readOnlyHint`): no other surface stores answers.
  `cmd/pfr-reports` serves two GET routes and has no keyed write. No outbound caller derives a key.

## Decision

1. Every problem type declares exactly one replay class on its catalogue entry: the `class` field of `problem`
   (`internal/adapters/inbound/http/errors.go`). A problem cannot be built without one: `newProblem`
   (`replay_class.go`) is the only constructor, an AST test forbids a literal anywhere else, and the fleet
   template test `TestEveryProblemTypeDeclaresAReplayClass` fails on an unclassified entry. There is no default in
   code.
2. `writeProblem` (every problem, `writeError` included) marks the middleware's capturing writer with the class;
   `capture.storable` is the whole decision: a 2xx is stored, a 5xx never, any other answer only when FINAL. A
   RETRYABLE refusal, a refusal that reaches the middleware without a class, and (by construction, section 3) an
   OUTCOME are released: nothing is written, the unit of work was rolled back, a retry executes again. The
   middleware holds no slug list and no status range beyond 2xx and 5xx. 2xx, 5xx and FINAL answers are
   byte-identical to before; slugs, titles and statuses on the wire are unchanged.
3. **OUTCOME does not exist in this service.** It needs a refusal that commits state. Here a refusal rolls
   everything back: `serveFresh` returns `errRollback` for every answer that is not a 2xx, and `UoW.Do` discards the
   whole unit. The use cases that can refuse after writing do not exist either: `RaiseManualRequest.raise`
   (`raise_manual_request.go` 46-90) refuses (`ErrNoForwardAssignment`, `ErrNoPolicy`, `ErrOpenRequestExists`,
   `ErrNoSourceStock`, `ErrNothingToReplenish`) before any save, and `CancelRequest.Handle` (`cancel_request.go`
   57-63) returns `ErrNotOpen` from `Request.Cancel` before `Save`. `TestA4xxRollsBackWhatTheHandlerDid` and
   `TestPostgresRejectionLeavesNoEffectAndReleasesTheKey` prove no refusal leaves state, and
   `TestNoProblemTypeIsAnOutcomeBecauseARefusalRollsEverythingBack` fails the day a problem declares OUTCOME
   without a mechanism to commit its writes.
4. **A concurrent duplicate when the first ends RETRYABLE.** The first request is rolled back and stores nothing,
   so there is nothing to wait on or replay: the duplicate (which ran in parallel anyway) answers from its own
   execution. After a refusal that is not stored the middleware looks the key up once (`releaseRefusal`): if the
   duplicate that won the race stored a 2xx meanwhile (for example the loser of `replenishment-already-open`),
   that answer is replayed with `Idempotency-Replayed: true`, exactly as before; otherwise the refusal goes out as
   a first execution and the key stays free. A different body under a released key is a new request, not
   `idempotency-key-reused`.
5. When unsure between FINAL and RETRYABLE the class is RETRYABLE: a released refusal costs one more execution,
   a wrongly FINAL one pins a stale refusal for the retention window.

### The classes (held equal to the catalogue by `TestTheADRClassTableMatchesTheCatalogue`)

Routes with an `Idempotency-Key`: `POST /replenishments`, `POST /replenishments/{requestId}/cancellations`,
`POST /sites/{siteId}/evaluations`. The table classifies every problem type of the service; the GET and PUT
problems cannot reach the middleware and are classified because every problem type has a class.

| Problem type | Status | Class | Reason (code) |
| --- | --- | --- | --- |
| `malformed-request` | 400 | FINAL | the request alone: `decodeJSON` (`response.go`) and the body bound (`idempotency.go` `bufferBody`) |
| `invalid-if-match` | 400 | FINAL | the header alone (`params.go`); PUT and DELETE |
| `invalid-site-id` | 400 | FINAL | the path or body field alone: the 1-64 bound and blank checks (`params.go`, `usecases.go` `requireSiteSKU`) |
| `invalid-sku` | 400 | FINAL | as `invalid-site-id` |
| `invalid-policy` | 400 | FINAL | the PUT body alone: bounds, multiple, source (`policy.ErrInvalidBounds`, `ErrInvalidMultiple`, `ErrInvalidSource`, `ErrMultipleNotAllowed`, `declare_policy.go` 118-121); PUT |
| `invalid-quantity` | 400 | FINAL | `quantity` below 1 (`replenishment_handlers.go` `replenishmentInput`) |
| `invalid-request-id` | 400 | FINAL | the `{requestId}` alone: prefix, whitespace, `/` (`usecases.go` `validateRequestID`) |
| `invalid-state` | 400 | FINAL | the query alone; GET |
| `invalid-status` | 400 | FINAL | the query alone; GET |
| `unknown-query-parameter` | 400 | FINAL | the query alone; GET |
| `invalid-limit` | 400 | FINAL | the query alone; GET |
| `invalid-cursor` | 400 | FINAL | the cursor alone (`usecases.go` 114, 132); GET |
| `idempotency-key-required` | 400 | FINAL | the header alone; answered before the store, never stored |
| `idempotency-key-reused` | 409 | FINAL | the request and the stored fingerprint; it is the answer of a hit, never stored |
| `invalid-report-granularity` | 400 | FINAL | the query alone (`report.ParseGranularity`); GET on `cmd/pfr-reports` |
| `invalid-report-range` | 400 | RETRYABLE | an omitted `from` or `to` is anchored on the server clock (`report/range.go` 63-85), so an empty range depends on `now`; GET on `cmd/pfr-reports`, never keyed. J5 |
| `replenishment-not-found` | 404 | FINAL | the target of cancel and get: ids are minted here (`system.RandomIDs`, `repl-<uuid>`), no request row is ever deleted and no event can deliver one. J2 |
| `policy-not-found` | 404 | RETRYABLE | a policy that another PUT or a pack-level event declares later (`declare_policy.go` 90, `remove_policy.go` 23); PUT, DELETE and GET |
| `policy-already-exists` | 409 | RETRYABLE | the policy may be removed and the PUT retried (`declare_policy.go` 42); PUT |
| `replenishment-already-open` | 409 | RETRYABLE | the open request for the pick face closes later (`raise_manual_request.go` 64, `request_repository.go` 87); nothing committed |
| `replenishment-not-open` | 409 | FINAL | Completed and Cancelled are terminal: `Request.Cancel` and `Request.Complete` refuse them (`request.go` 93-95, 120-122) and nothing re-opens a request. J3 |
| `concurrent-modification` | 409 | RETRYABLE | the version-guarded save lost (`request_repository.go` 71, 84; `policy_repository.go` 72, 94); nothing committed |
| `no-forward-assignment` | 422 | RETRYABLE | the forward map is an event-fed local copy replaced per site on `SlotPlanApproved` (`raise_manual_request.go` 54) |
| `no-active-case-level` | 422 | RETRYABLE | the pack-level view is fed by a product-master event (`declare_policy.go` 131); PUT |
| `no-policy` | 422 | RETRYABLE | the policy may be declared next (`raise_manual_request.go` 61) |
| `no-source-stock` | 422 | RETRYABLE | one slug for two causes, both stock levels fed by inventory-storage events (`raise_manual_request.go` 75, 88; `request.go` 275) |
| `nothing-to-replenish` | 422 | RETRYABLE | the pick-face level changes (`raise_manual_request.go` 78) |
| `quantity-exceeds-max` | 422 | RETRYABLE | projected level plus quantity against a policy and a level that both change (`request.go` 255) |
| `internal-error` | 500 | RETRYABLE | a 5xx is never stored |
| `report-store-error` | 500 | RETRYABLE | a 5xx is never stored; GET on `cmd/pfr-reports` |
| `dependency-unavailable` | 503 | RETRYABLE | the `/readyz` answer, no keyed route; a 5xx is never stored |

No problem type is an OUTCOME (section 3). MCP and A2A map the same errors for their read-only tools and store nothing.

### Judgement calls (most consequential first)

- **J1 `no-policy`, `no-forward-assignment`, `no-source-stock`, `nothing-to-replenish`, `quantity-exceeds-max`
  RETRYABLE.** These were stored for the retention window until now. Each depends on state that events or other
  requests change (the declared policy, the slot plan, the bin levels); an operator who declares the policy and
  retries with the same key now gets the request raised instead of the stale `no-policy`. `no-source-stock` and
  `quantity-exceeds-max` also mix the request's own `quantity` with state; mixed slugs are RETRYABLE (splitting
  them is a recorded follow-up).
- **J2 `replenishment-not-found` FINAL.** The only 404 of a keyed route. The three conditions of the fleet default
  hold: this service mints the ids, nothing deletes a request, and no event can deliver the request later (the
  slug is not shared with a referenced fact). `policy-not-found` is a different slug and RETRYABLE. If a request
  row could ever be deleted, or an id be minted elsewhere, this entry becomes RETRYABLE.
- **J3 `replenishment-not-open` FINAL.** A cancel of a Completed or Cancelled request: the answer is a function of
  the request id and the aggregate's terminal state, which never moves back. A retry can never pass.
- **J4 `idempotency-key-*` and the request-only 400s FINAL** although the middleware answers the two key problems
  itself and never stores them: the class records that they are a function of the request.
- **J5 `invalid-report-range` RETRYABLE.** Request-shaped, but a range with one open end is evaluated against the
  clock. Only GET routes answer it, so no behaviour changes; the class is the safe one.

### What the proofs are

- Unclassified entry fails: `TestEveryProblemTypeDeclaresAReplayClass` (fleet template, red as a compile error
  before the field existed) and `TestEveryProblemTypeHasOneClass`.
- Per class through the production middleware: the six fleet-template tests in
  `idempotency_replay_classes_test.go`, wired in `idempotency_replay_classes_wiring_test.go` over the memory
  unit of work and store (Docker-free).
- Through the real router: `TestAFinalRefusalIsStoredAndReplayed`, `TestARetryableRefusalIsNotStoredAndItsKeyRetries`,
  `TestARefusalWithNoClassIsReleased`, `TestTheLoserOfARaceWithARetryableRefusalReplaysTheWinnersAnswer`.
- Over a real Postgres (CI `integration`): `TestPostgresConcurrentModificationReleasesTheKeySoTheRetryExecutes`
  (the loser's key row is absent, the retry under the same key executes, is stored and replayed),
  `TestPostgresNoPolicyReleasesTheKeySoTheRetryExecutes`, `TestPostgresAFinalRefusalIsStoredAndReplayed`.

## Consequences

- A refusal marked RETRYABLE is no longer replayed: a retry under the same key executes again, and a different body
  under that key is a new request. `apis/openapi.yaml` lists the RETRYABLE problem types so clients know to retry
  the same key.
- The `idempotency_keys` table loses the refusals it used to hold; the `status` check (`>= 200 AND < 500`) stays
  valid for FINAL refusals.
- **Callers.** Nothing in this service derives a key for a call to another context. A released refusal means the
  same key may be tried again by whoever sent it, nothing more: no consumer-side or client-side retry is added;
  a person-driven caller (the replenishment console) surfaces the refusal.
- **Retention is not enforced** (found, not fixed here): no code deletes expired `idempotency_keys` rows
  (only the `idx_idempotency_keys_created_at` index exists) and a replay does not check the row's age, so the
  documented 24 hours are not a rule today. A follow-up adds a purge and an age check on replay.
- Follow-ups: split `no-source-stock` and `quantity-exceeds-max` into request-only and state-dependent problem
  types; enforce the retention window.
