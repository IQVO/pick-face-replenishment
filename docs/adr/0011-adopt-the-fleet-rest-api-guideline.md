# ADR 0011: Adopt the fleet REST API guideline

## Status

Accepted (2026-10-31). Follows [ADR 0005](./0005-evaluation-semantics-and-persistence.md) (the evaluation sweep whose
route is renamed here) and [ADR 0006](./0006-postgres-and-kafka-are-the-only-runtime.md) (`Idempotency-Key` is required
on every deployment). Implements the fleet standard "Warehouse REST API Guideline" (warehouse-docs); it is a breaking
change that ships with the whole REST cutover set.

## Context

The 2026-10-30 application portfolio assessment found three command-URL styles across the fleet (`:verb` suffixes, verb
sub-paths, sub-resources), two spellings of the replay header, key reuse answered `422` in most services and `409` in
one, and unpaginated lists. This service already keyed its three POSTs and already paged its three lists with
`limit`/`cursor` and `{items, nextCursor}`, but it had two verb routes, no replay marker, `422` for key reuse, and an
OpenAPI document that did not describe the replay header or the `409`.

## Decision

The service follows the guideline, with no alias, no toggle and no coexistence of old and new:

1. **URLs name things, never actions (R1, R2).** The two verb routes become transition resources. The operation ids do
   not change, so MCP and A2A mappings do not move; status codes and bodies do not change either.

   | Old | New | `operationId` |
   | --- | --- | --- |
   | `POST /replenishments/{requestId}/cancel` | `POST /replenishments/{requestId}/cancellations` | `cancelReplenishment` |
   | `POST /sites/{siteId}/evaluate` | `POST /sites/{siteId}/evaluations` | `evaluateSite` |

   The old routes answer `404`/`405` (`TestRetiredRoutesAreGone`, and the same assertion against the real router on
   Postgres).
2. **Every POST and PATCH is idempotent (R3 to R6, R8).** The three POSTs were already behind the route-scoped
   `Idempotency` middleware, so no route had to be newly wired. The middleware changed minimally:
   - the replay header is `Idempotency-Replayed: true`, set on every replay (a stored 2xx or 4xx, and the winner's answer
     handed to the loser of a race) and never on a first execution; the earlier, differently spelled header is retired
     everywhere (code, spec, tests, docs, clients);
   - the same key with a different method, path or body is `409 Conflict`, problem type `idempotency-key-reused` (it was
     `422`); the fingerprint stays method + path + SHA-256 of the body;
   - a missing key is `400 idempotency-key-required` (unchanged), a 5xx is rolled back and never stored (unchanged), the
     key row commits in the same transaction as the aggregate change and its outbox rows (unchanged).
3. **OpenAPI (R3 to R5, R8).** `IdempotencyKey` is a required header parameter (the service always runs on Postgres);
   every POST declares `400` and `409` (`evaluateSite` had no `409`) and documents `Idempotency-Replayed`
   (`components/headers/IdempotencyReplayed`) on its 2xx; the `422` response no longer describes key reuse.
4. **Collections (R7).** `GET /policies`, `GET /pick-faces` and `GET /replenishments` already page with `limit` (1 to 500,
   default 100) and an opaque `cursor`, answer `{items, nextCursor}` and reject bad values with `400 invalid-limit` /
   `invalid-cursor`; nothing changed.
5. **Enforcement.** `.spectral.yaml` runs the shared `rest-guideline` rule (`.spectral/functions/restGuideline.js`) in the
   existing `api-lint` job with `--fail-severity=warn`; the baseline was 9 findings (R2 x2, R4 x1, R5 x3, R8 x3) and is 0.

### Exemptions

No operation declares `x-pagination-exempt`. Two read-model routes served by the separate `pfr-reports` binary,
`GET /reports/replenishment-funnel` (a `from`/`to` window of at most 366 days, day granularity) and
`GET /reports/freshness` (a singleton), are skipped by the ruleset's structural `/reports/` rule rather than by a marker.

### Consumers changed in the same change

The only HTTP client in this repository is its own web remote: `web/src/api.ts` calls
`POST /replenishments/{requestId}/cancellations` (its `Idempotency-Key` per POST and its `items`/`nextCursor` handling
were already conformant) and its tests (`web/src/api.test.ts`, `web/src/screens/ReplenishmentDetailScreen.test.tsx`)
follow. The service calls no sibling over HTTP (rule 6) and the MCP and A2A adapters are read-only, so there is no
outbound adapter, seed script or recorded fixture to migrate.

## Consequences

- **Breaking.** Clients of the two renamed routes, and clients that treated `422` as the key-reuse answer, must change.
  This PR must deploy together with the REST cutover set of the fleet.
- A client may now distinguish a replay from a first execution by `Idempotency-Replayed: true`, and a key-reuse bug from
  a business `409` by the problem type `idempotency-key-reused`.
- The generated REST reference (`docs/docs/api-reference/rest`) is regenerated from the spec with the CI recipe.
