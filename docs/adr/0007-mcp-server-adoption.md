# ADR 0007: MCP server adoption (read-only, unauthenticated, additive)

## Status

Accepted (2026-10-18).

## Context

The fleet exposes every bounded context to `warehouse-ops-agent` (and any other
MCP client) through a second, independent deployable binary that serves the
Model Context Protocol over Streamable HTTP, alongside the REST API and never
bundled into the same process (the same decision is recorded in
slotting-optimization ADR 0005, product-master ADR 0005 and inbound-receiving
ADR 0005). pick-face-replenishment owns the min/max replenishment policies and
the replenishment requests that move stock from a reserve bin to a forward pick
face (ADR 0002). Agents ask about them constantly ("which pick faces are below
minimum?", "why was `repl-...` cancelled?"), while every change must leave as a
versioned CloudEvent through the transactional outbox (ADR 0004), guarded by
`If-Match` and `Idempotency-Key` (rule 8 of the service guide).

## Decision

1. `cmd/mcp` is a **second composition root**, independent of `cmd/api`: it
   wires the SAME read use cases (`ListPolicies`, `GetPolicy`, `ListPickFaces`,
   `ListRequests`, `GetRequest`) to the SAME Postgres repositories through a
   dedicated inbound adapter (`internal/adapters/inbound/mcp`) instead of the
   REST router. It is built on the official SDK
   (`github.com/modelcontextprotocol/go-sdk`), Streamable HTTP only (no stdio,
   no SSE), listens on `MCP_ADDR` (default `:8090`), serves the MCP endpoint at
   both `/` and `/mcp`, and answers `GET /healthz` for the probes.
2. The surface is **read-only: 5 tools**, each backed by an existing read use
   case and returning the REST body with snake_case names: `list_policies`
   (`site_id`, `sku`, `limit`, `cursor`), `get_policy` (`site_id`, `sku`),
   `list_pick_faces` (`site_id` required; `status` BELOW_MIN, OK, NO_SOURCE or
   NO_POLICY; `limit`, `cursor`), `list_replenishments` (`site_id`, `sku`,
   `state` Requested, Completed or Cancelled; `limit`, `cursor`) and
   `get_replenishment` (`request_id`). Arguments are snake_case like every
   sibling MCP server, where REST uses camelCase (`siteId`). Lists page with
   the opaque cursor of the REST API (`limit` 1..500, default 100,
   `next_cursor` absent on the last page). Errors are tool results
   (`isError: true`) whose text starts with the REST problem slug
   (`policy-not-found`, `replenishment-not-found`, `invalid-site-id`,
   `invalid-sku`, `invalid-request-id`, `invalid-state`, `invalid-status`,
   `invalid-limit`, `invalid-cursor`); anything untyped is logged and reported
   as a generic `internal-error`.
3. **No write tool.** Declaring or removing a policy, requesting and cancelling
   a replenishment and the site evaluation stay REST-only: those are the
   operations whose events other contexts consume, whose idempotency is keyed
   by `Idempotency-Key` and whose version guard is `If-Match`; an agent write
   path would be a second front door to the outbox. Read-only is pinned three
   ways: `internal/adapters/inbound/mcp/governance_test.go` fails the build on a
   write-verb tool name, a tool that is not annotated read-only or a surface
   above the 5-tool budget, and pins the registry golden byte for byte;
   `internal/architecture`'s `TestMCPSurfaceIsReadOnly` fails when the adapter
   or `cmd/mcp` references any write use case, imports the outbox, the event
   sink, Kafka, the ports package or the in-memory doubles, builds an outbox
   repository, unit of work or idempotency store, or gives its detector an event
   sink, id generator or clock (`TestEveryUseCaseIsClassifiedReadOrWrite` keeps
   that list from going stale); and the integration test proves a series of
   reads leaves the `outbox_events` table unchanged.
4. **No authentication of any kind**, matching the fleet-wide decision
   (reverted 2026-09-11) that REST and MCP are both unauthenticated; access
   control is the in-cluster `ClusterIP` boundary.
   `TestNoAuthMiddlewareReintroduced` fails CI if auth middleware appears.
5. **Postgres only, no Kafka.** Like `cmd/api` (ADR 0006) the binary has no
   in-memory mode: without `DATABASE_URL` it refuses to boot, naming the
   variable. It never starts the outbox relay, never runs the local-copy
   consumers and never dials Kafka (no `KAFKA_BROKERS`, `*_MODE` or consumer
   group). It reads the same database `cmd/api` writes, so `list_pick_faces`
   answers from the event-fed local copies (ADR 0003), never from a live call to
   a sibling. It runs the idempotent embedded migrations on boot (through
   `MIGRATIONS_DATABASE_URL` when set), so it can start against a fresh
   database; golang-migrate's advisory lock makes concurrent starts safe. It
   reads `FORWARD_ZONE_CODES` and `RESERVE_ZONE_CODES` exactly as `cmd/api`
   does.
6. `TestMCPAdapterDependencyRule` keeps the adapter additive: it depends only on
   the application and domain layers, and nothing else imports it.
7. Packaging: the image (`Dockerfile`) builds every `cmd/*` binary, so
   `/app/mcp` ships next to `/app/api`; the chart renders an `mcp` Deployment
   and ClusterIP Service (component `mcp`) only when `mcp.enabled=true`
   (default false), and the chart selector test proves each Service selects
   exactly one Deployment. No HPA for mcp: the SDK keeps per-process session
   state.

## Consequences

- An agent can read policies, pick-face status and requests without any REST
  client; it cannot change anything. The REST contract stays the only write
  path.
- The tool registry is a reviewed contract: adding or changing a tool changes
  the golden and, for a new tool, the budget in `governance_test.go` and this
  ADR.
- The MCP pod needs a Postgres DSN and nothing else; it can run when Kafka is
  down.
