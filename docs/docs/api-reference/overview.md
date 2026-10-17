---
id: overview
title: API overview
sidebar_label: API overview
slug: /api-reference
sidebar_position: 1
---

# API overview

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` (OpenAPI 3.0.3, version 1.0.0) | The pages under *REST* are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. Servers declared in the spec: `http://localhost:8080` (local) and `http://localhost:8000/api/pick-face-replenishment` (Kong in the kind cluster). `warehouse-infra` on `develop` has no values file, route or dashboard for this service yet, so the Kong address is a declaration, not a live route. |
| Events | `apis/asyncapi.yaml` | Summarised on the [event catalogue](/docs/api-reference/events); every field on [Domain events](/docs/ddd/domain-events). |
| MCP | none | There is no `cmd/mcp`: the service exposes REST and Kafka only. |

## Endpoints

| Tag | Endpoint | Use case | Success |
| --- | --- | --- | --- |
| Policies | `GET /policies` | `ListPolicies` | `200 {items, nextCursor?}` ordered by site then SKU |
| Policies | `PUT /policies/{siteId}/{sku}` | `DeclarePolicy` | `200`, the policy, version also as `ETag` |
| Policies | `GET /policies/{siteId}/{sku}` | `GetPolicy` | `200`, the policy, `ETag` |
| Policies | `DELETE /policies/{siteId}/{sku}` | `RemovePolicy` | `204`; needs `If-Match` |
| Pick faces | `GET /pick-faces` | `ListPickFaces` | `200 {items, nextCursor?}` ordered by forward bin |
| Replenishments | `POST /replenishments` | `RaiseManualRequest` | `201`, `Location: /replenishments/<requestId>` |
| Replenishments | `GET /replenishments` | `ListRequests` | `200 {items, nextCursor?}`, newest first |
| Replenishments | `GET /replenishments/{requestId}` | `GetRequest` | `200`, the request |
| Replenishments | `POST /replenishments/{requestId}/cancel` | `CancelRequest` | `200`, the Cancelled request |
| Sites | `POST /sites/{siteId}/evaluate` | `EvaluateSite` | `200`, the sweep summary |
| Health | `GET /healthz`, `GET /readyz` | | `200` / `503` ([Runbook](/docs/operations/runbook#readiness-and-shutdown)) |

The router serves exactly these 12 operations: `TestRouterServesExactlyTheDocumentedOperations` in
`internal/adapters/inbound/http/contract_internal_test.go` fails when the router and the spec differ.
The service serves no `/metrics` endpoint (see [Observability](/docs/operations/observability)).

## Conventions

- **Bodies.** Strict JSON: unknown fields, trailing data, an empty required body and bodies over 64 KiB are
  `400 malformed-request`. JSON names are camelCase on REST and snake_case in event `data`.
- **`Idempotency-Key`** is required on the three POSTs (`/replenishments`, `/replenishments/{requestId}/cancel`,
  `/sites/{siteId}/evaluate`) when the service runs on Postgres: a missing or over-128-character key is
  `400 idempotency-key-required`, the same key and body replays the stored answer (4xx included), the same key with
  another body is `422 idempotency-key-reused`. A 5xx is never stored. The in-memory mode installs no middleware and
  ignores the header. The PUT and DELETE of a policy do not take it: they are guarded by `If-Match`.
- **Policy versions.** `PUT /policies/{siteId}/{sku}` without `If-Match` creates the policy at version 1
  (`409 policy-already-exists` when it exists); with `If-Match: <version>` (quoted or bare integer) it replaces the
  policy only when that is the current version (`409 concurrent-modification` otherwise). `DELETE` without
  `If-Match` is `400 invalid-if-match`.
- **Paging.** `limit` (1 to 500, default 100) and the opaque `cursor` (the previous page's `nextCursor`, base64url of
  the last key). A response without `nextCursor` is the last page. `GET /pick-faces` takes the required `siteId` and
  an optional `status` (`BELOW_MIN`, `OK`, `NO_SOURCE`, `NO_POLICY`); `GET /replenishments` takes `siteId`, `sku` and
  `state` (`Requested`, `Completed`, `Cancelled`); `GET /policies` takes `siteId` and `sku`.
- **Errors** are RFC 7807 `application/problem+json` with
  `type = https://errors.pick-face-replenishment.warehouse-systems.dev/<slug>` from one table (`problemCatalogue` in
  `internal/adapters/inbound/http/errors.go`). The slugs are `malformed-request`, `invalid-if-match`,
  `invalid-site-id`, `invalid-sku`, `invalid-policy`, `invalid-quantity`, `invalid-request-id`, `invalid-state`,
  `invalid-status`, `invalid-limit`, `invalid-cursor`, `policy-not-found`, `replenishment-not-found`,
  `policy-already-exists`, `replenishment-already-open`, `replenishment-not-open`, `concurrent-modification`,
  `no-forward-assignment`, `no-policy`, `no-source-stock`, `nothing-to-replenish`, `quantity-exceeds-max`,
  `idempotency-key-required`, `idempotency-key-reused` and `internal-error`. Causes and fixes are on
  [Troubleshooting](/docs/operations/troubleshooting#rest-problem-types).
- **Auth**: none. The fleet removed REST and MCP bearer auth on 2026-09-11 and
  `TestNoAuthMiddlewareReintroduced` fails CI if auth middleware returns.
- **CORS**: the router installs no CORS middleware.
