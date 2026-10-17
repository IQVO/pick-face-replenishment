---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

The contract is `apis/openapi.yaml` (OpenAPI 3.0.3, "Pick-Face Replenishment
API"). This file maps each route to the use case that serves it. Keep it
manually accurate: it is documentation for an agent, not generated, and the
`docs-api-drift` CI job only checks the generated docs against the spec. The
HTTP adapter and the application layer are built in wave 2; the use case names
below are the intended ones (see `.claude/rules/domain-model.md`).

| Route | `operationId` | Use case |
| --- | --- | --- |
| `GET /policies` | `listPolicies` | `ListPolicies` (filters `siteId`, `sku`; cursor page) |
| `PUT /policies/{siteId}/{sku}` | `declarePolicy` | `DeclarePolicy` (no `If-Match` creates at v1; `If-Match: <version>` replaces) |
| `GET /policies/{siteId}/{sku}` | `getPolicy` | `GetPolicy` (version also as `ETag`) |
| `DELETE /policies/{siteId}/{sku}` | `removePolicy` | `RemovePolicy` (needs `If-Match`; cancels open requests with `POLICY_REMOVED`) |
| `GET /pick-faces` | `listPickFaces` | `ListPickFaces` (`siteId` required; `status` filter; derived from `detection.Detect`) |
| `POST /replenishments` | `requestReplenishment` | `RequestReplenishment` (needs `Idempotency-Key`; 201 + `Location`) |
| `GET /replenishments` | `listReplenishments` | `ListReplenishments` (newest first; filters `siteId`, `sku`, `state`) |
| `GET /replenishments/{requestId}` | `getReplenishment` | `GetReplenishment` |
| `POST /replenishments/{requestId}/cancel` | `cancelReplenishment` | `CancelReplenishment` (needs `Idempotency-Key`; reason `OPERATOR`) |
| `POST /sites/{siteId}/evaluate` | `evaluateSite` | `EvaluateSite` (needs `Idempotency-Key`; sweep that converges) |
| `GET /healthz`, `GET /readyz` | `healthz`, `readyz` | probes, no use case (`readyz` is 503 while starting or draining) |

## Conventions

- Error shape: RFC 7807 `application/problem+json` with `type`, `title`,
  `status`, `detail`, `instance`. `type` is
  `https://errors.pick-face-replenishment.warehouse-systems.dev/<slug>`.
  Every non-2xx response uses it, including 500 `internal-error`.
- JSON bodies are camelCase and strict (`additionalProperties: false`): an
  unknown field is 400 `malformed-request`. Lists are cursor pages:
  `limit` 1 to 500 (default 100), opaque `cursor` = the previous `nextCursor`,
  no `nextCursor` on the last page (400 `invalid-limit` / `invalid-cursor`).
- `Idempotency-Key`: required on `POST /replenishments`,
  `POST /replenishments/{requestId}/cancel` and `POST /sites/{siteId}/evaluate`
  whenever the service runs (it always runs on Postgres, ADR 0006); absent is 400
  `idempotency-key-required`. Same key and same body replays the original
  response; same key with a different body is 422 `idempotency-key-reused`.
- `If-Match` carries the policy version (the `ETag` of a previous response,
  quoted or not): omitted creates, present replaces or removes. A stale value
  is 409 `concurrent-modification`; a non-integer is 400 `invalid-if-match`.
  Backed by `policy.Policy.WithBounds` (version + 1).
- 400 (bad input): `invalid-site-id`, `invalid-sku`, `invalid-request-id`
  (`repl-` prefix, no whitespace or `/`), `invalid-policy`, `invalid-quantity`,
  `invalid-state`, `invalid-status`, `invalid-limit`, `invalid-cursor`,
  `invalid-if-match`, `idempotency-key-required`, `malformed-request`.
- 404: `policy-not-found`, `replenishment-not-found`.
- 409 (wrong state or lost race): `policy-already-exists`,
  `replenishment-already-open` (one open request per forward bin and SKU),
  `replenishment-not-open` (cancelling a Completed or Cancelled request),
  `concurrent-modification`.
- 422 (well-formed but state forbids it): `no-forward-assignment`, `no-policy`,
  `no-source-stock`, `nothing-to-replenish` (no `quantity` on a pick face above
  its minimum), `quantity-exceeds-max`, `idempotency-key-reused`. These map the
  application errors `ErrNoForwardAssignment`, `ErrNoPolicy`,
  `ErrNoSourceStock` and `ErrNothingToReplenish` of
  `internal/application/usecases/usecases.go` and the domain errors
  `ErrExceedsMax` and `ErrExceedsSourceStock` (`no-source-stock`) of
  `internal/domain/replenishment/values.go`. `ErrNotOpen` is the 409
  `replenishment-not-open`, not a 422.
- Path ids longer than 64 characters are 400 `invalid-site-id` / `invalid-sku`
  (the OpenAPI `maxLength`).
- A domain error never leaks as a raw message; the adapter maps it to a slug
  above and adding a slug means adding its example to `apis/openapi.yaml`.
- Request ids are `repl-<uuid>`, at most 64 characters.
- Auth: none. Fleet-wide REST and MCP auth was deliberately reverted on
  2026-09-11 and this API is unauthenticated; do not add auth middleware
  (`TestNoAuthMiddlewareReintroduced` in `internal/architecture/fitness_test.go`
  enforces it). See `.claude/rules/fleet/no-auth-and-mcp.md`.
