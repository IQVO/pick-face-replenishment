---
name: how-to-add-a-rest-endpoint
description: Add or change a REST endpoint in pick-face-replenishment in hexagonal order (domain invariant, use case, port, HTTP adapter, apis/openapi.yaml, godog scenario). Use when touching apis/openapi.yaml, the HTTP adapter, or exposing a use case over HTTP.
---

# How to add a REST endpoint

Use when asked to add a new REST use case or endpoint to this service. Follow
this order, domain first and adapter last, never the reverse: writing the HTTP
handler before the domain invariant it enforces produces handlers that
validate nothing and use cases that get bypassed.

Layer status: the domain (`internal/domain/...`) and the contract
(`apis/openapi.yaml`) are committed. The application layer, the HTTP adapter
and `cmd/` are built in wave 2, so steps 2 and 3 describe the shape to follow
and name no file yet. Once they exist, read an existing endpoint of the same
kind alongside this guide.

Worked example, `PUT /policies/{siteId}/{sku}` (`declarePolicy`): the
invariants are in `internal/domain/policy/policy.go` (`New`, `Restore`,
`WithBounds`, `RoundDown`), the contract is the `/policies/{siteId}/{sku}`
entry of `apis/openapi.yaml`, and the route to use case table is
`.claude/rules/rest-api.md`.

## 1. Domain first: does an invariant already exist, or do you need one?

Check `internal/domain/policy`, `internal/domain/replenishment` and
`internal/domain/detection` for the rule this endpoint enforces. An endpoint
should almost never contain business logic: it decodes a request, calls a use
case, encodes the result. If the operation needs a new rule (for example a new
validation on `policy.Policy`, or a new `replenishment.CancelReason`), add it to
the aggregate or value object with its own table-driven test BEFORE touching
the application or adapter layers. Return a sentinel error
(`replenishment.ErrExceedsMax` style) so the adapter can map it with
`errors.Is`. Write decisions as `if` chains with early returns (the mutation
gate counts every `case` as a mutant), and add the package to the mutation gate
if it is a new one under `internal/domain/`.

## 2. Application: define the use case

In the application layer (one package, one file per
use case), define the use case as a struct that depends on driven ports only,
never a concrete adapter, with an `Execute` method:

```go
type DeclarePolicy struct {
    Policies ports.PolicyRepository // interface only
    Outbox   ports.Outbox           // events leave via the transactional outbox
    Clock    ports.Clock            // never call time.Now() directly
}

func (uc *DeclarePolicy) Execute(ctx context.Context, in DeclarePolicyInput) (policy.Policy, error) {
    // 1. load through the port
    // 2. call the aggregate/value-object method (WithBounds); never inline the invariant
    // 3. persist and write the outbox row in ONE transaction
    // 4. return the domain type, not a DTO
}
```

Ports are interfaces ONLY (a fitness test in `internal/architecture/` enforces
it). Unit-test the use case against an in-memory implementation of the ports,
never a real Postgres or HTTP call, and cover the success path AND each
domain-rule failure path (stale `If-Match`, exceeds max, request not open).
Rules that span aggregates, such as one open request per (site, forward bin,
SKU), live in the use case and a partial unique index. The use case reads the
event-fed local copies and never calls a sibling context.

## 3. Adapter: wire the HTTP handler

In the inbound HTTP adapter (an `inbound/http` package under the adapters, built in wave 2):

1. DTO structs (camelCase JSON tags) live ONLY in the adapter; domain types
   never carry JSON tags.
2. The handler: decode with unknown fields rejected (the contract sets
   `additionalProperties: false`), convert to domain value objects at once so a
   bad value fails as an RFC 7807 problem and never reaches the use case, call
   `Execute`, encode the result.
3. Map errors in ONE table (`errors.Is` on domain sentinels) to the problem
   slugs of `apis/openapi.yaml`; read `.claude/rules/rest-api.md` before adding
   a slug. `If-Match` carries the policy version, `Idempotency-Key` is required
   on the mutating `POST` routes.
4. Wire the use case in `cmd/` (the only place that wires layers).

Write at least one `httptest` per endpoint: a success path and the error paths
it can produce. No auth middleware (`TestNoAuthMiddlewareReintroduced`).

## 4. Contract: update OpenAPI first or in the same change

Add the path to `apis/openapi.yaml` with request and response schemas, the
problem response for each error case (examples with
`https://errors.pick-face-replenishment.warehouse-systems.dev/<slug>`), and an
`operationId`. Then add the route and use case to the table in
`.claude/rules/rest-api.md`. Lint the contract the way CI does:

```bash
spectral lint apis/openapi.yaml --ruleset .spectral.yaml --fail-severity=warn
```

## 5. Behaviour: add a godog scenario

If the endpoint is user-facing behaviour, add a `.feature` file under
`features/` (steps live next to it in `features/features_*_test.go`; `make bdd`
runs `TestFeatures`; never add a Go file at the repo root), Given/When/Then.
Scenarios drive the use cases over the in-memory adapters; the HTTP surface is
covered by `internal/adapters/inbound/http` tests.

## 6. Verify before opening the PR

```bash
make check        # fmt-check vet build lint test
make check-all    # + coverage (90% gate) + arch-test + bdd
```

`make coverage` gates the domain and application packages at 90%; a new use
case with no test on its failure path is the most common way to miss it.
