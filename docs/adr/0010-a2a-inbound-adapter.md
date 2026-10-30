# ADR 0010: A2A inbound adapter (read-only, unauthenticated, additive)

## Status

Accepted (2026-10-29). Follows [ADR 0007](./0007-mcp-server-adoption.md) (the MCP adapter this one is modelled on and
whose read-only charter it inherits), [ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md) (CloudEvents on every
Kafka message) and [ADR 0006](./0006-postgres-and-kafka-are-the-only-runtime.md) (Postgres and Kafka are the only
runtime). Mirrors the fleet's reference rollout (workforce-management ADR 0039, wes-work-planning ADR 0046).

## Context

The fleet is adopting the Agent2Agent (A2A) protocol so that one bounded context can **delegate a goal** to another
context's agent. pick-face-replenishment already exposes its use cases over REST and, read-only, over MCP (ADR 0007).
Three transports now carry three different things and must not overlap:

| Transport | Carries |
| --- | --- |
| Kafka + CloudEvents | **facts** between contexts (ADR 0004) |
| MCP | an agent's **tools** |
| A2A | **delegation** of a goal, never data sync |

The questions a delegating agent asks of this context are the two the MCP surface already answers: *which pick faces of
this site are low, and why?* and *what is happening to replenishment requests?*

## Decision

1. **A third driving adapter, `internal/adapters/inbound/a2a`,** over the SAME read use cases as REST and MCP
   (`ListPickFaces`, `ListRequests`, `GetRequest`), built on the official SDK `github.com/a2aproject/a2a-go/v2`
   (pinned v2.7.0). It is a **message-only** agent (no Task lifecycle: both skills are transactional request/response
   reads) speaking JSON-RPC 2.0 over HTTP. The Agent Card is served at `/.well-known/agent-card.json`, the endpoint is
   mounted at both `/` and `/a2a`, and `GET /healthz` answers the probes. The card declares no security schemes.
2. **The write gate: the agent is READ-ONLY.** An A2A skill may change state only if the context's MCP surface already
   offers that write. ADR 0007 decided MCP has no write tool (declaring or removing a policy, requesting and
   cancelling a replenishment and the site evaluation stay REST-only, because their events are consumed by other
   contexts and their idempotency and version guard are `Idempotency-Key` and `If-Match`), so A2A has none either.
   Delegation never widens what this context exposes; a write skill would need its own ADR that first widens MCP.
3. **Two curated, goal-shaped skills** (not one per endpoint), derived from the MCP tools and the REST read use cases:

   | Skill | Goal | Arguments | Use cases |
   | --- | --- | --- | --- |
   | `pick_face_status` | which forward pick faces of a site are `BELOW_MIN`, `NO_SOURCE`, `NO_POLICY` or `OK`, with stock and policy bounds | `siteId` (required), `status`, `limit`, `cursor` | `ListPickFaces` |
   | `replenishment_lookup` | one request by id, or a filtered newest-first list | `requestId` alone, or any of `siteId`, `sku`, `state`, `limit`, `cursor` | `GetRequest` / `ListRequests` |

   The policy reads (`list_policies`, `get_policy`) are not skills: their bounds already ride on every pick face. The
   by-id and list forms of `replenishment_lookup` are exclusive (`requestId` with a filter is refused) so a caller never
   believes a filter narrowed a by-id answer. Arguments are camelCase like the REST body (`siteId`), not the MCP
   snake_case. Answers are the REST bodies, never domain structs, and carry a one-line text summary.
4. **Strict input.** A skill call is `{"skill": "<id>", "arguments": {...}}` in a data part (or a text part holding the
   same JSON). Decoding rejects unknown fields and trailing data (`DisallowUnknownFields`), at the request and the
   argument level, so a misspelled optional name cannot silently drop a filter. `encoding/json` matches names
   case-insensitively, so only genuinely unknown names are refused.
5. **Rejections are answered in-band, never as JSON-RPC errors.** Every domain rejection and malformed request comes
   back as an RFC 7807-shaped `problem` (`type`, `title`, `detail`, `status`) inside the message envelope, with the SAME
   status and slug as the REST adapter's problem catalogue: `404 replenishment-not-found`; `400 invalid-site-id`,
   `invalid-sku` (also above the 64-character REST bound), `invalid-request-id`, `invalid-state`, `invalid-status`,
   `invalid-limit`, `invalid-cursor`; `500 internal-error` for anything untyped (cause logged, never sent). The A2A-only
   slugs are `malformed-skill-request`, `unknown-skill` and `invalid-argument` (all 400). The 409/422 entries of the REST
   catalogue are unreachable from reads and deliberately absent. Problem details are adapter-owned text: domain errors
   carry a package prefix (`policy: site id must not be blank`) and repository errors may wrap infrastructure text, so
   neither reaches an answer part. This context has no deprecated alias key (`siteId` is canonical on every surface),
   so there is no alias to keep out of the card.
6. **`cmd/a2a` is a composition root cloned from `cmd/mcp`**: the same Postgres repositories and read use cases, a
   read-only `Evaluator` (no event sink, id generator or clock), the idempotent boot migrations
   (`MIGRATIONS_DATABASE_URL`) and the same real wiring. Like `cmd/mcp` it builds no outbox repository, unit of work or
   event publisher, never starts the outbox relay, never runs the local-copy consumers and never dials Kafka
   (no `KAFKA_BROKERS`, `EVENT_PUBLISHER`, `*_MODE` or consumer group). Real Postgres only, no in-memory mode: it
   refuses to boot without `DATABASE_URL`. It listens on `A2A_ADDR` (default `:8093`; 8080 is the API, 8090 MCP, 8091
   the projector admin port and 8092 the reports API) and refuses to boot without `A2A_AGENT_URL`, an absolute
   `http(s)` URL with a host: the card must advertise a URL clients can dial, so there is no default (in-cluster:
   `http://<release>-a2a.<ns>.svc.cluster.local:8093/`). Configuration errors surface before any connection and before
   the listener binds, each naming its variable.
7. **No authentication of any kind**, matching the fleet-wide decision (reverted 2026-09-11) that REST, MCP and A2A are
   unauthenticated; access control is the in-cluster `ClusterIP` boundary. `TestNoAuthMiddlewareReintroduced` already
   scans `internal/adapters/inbound`, which now includes this adapter.
8. **No outbound A2A client.** This context delegates to nobody and keeps its ban on live lookups of sibling contexts
   (REST or MCP). `TestA2ASDKStaysInTheInboundAdapter` fails if the SDK is imported outside the adapter and `cmd/a2a`,
   or if `a2aclient` is imported by any non-test file.
9. **Observability.** Each skill runs under an OTel span `a2a.skill <id>` with attribute `a2a.skill`, mirroring the MCP
   `mcp.tool <name>` span; a problem answer marks the span as an error.
10. **Packaging.** The Dockerfile already builds every `cmd/*` directory, so `/app/a2a` ships beside `/app/api` and
    `/app/mcp`. The chart renders an `a2a` Deployment and ClusterIP Service (component `a2a`, port 8093, command
    `/app/a2a`) only when `a2a.enabled=true` (default **false**), with `A2A_AGENT_URL` defaulting to the Service DNS
    and overridable through `a2a.agentUrl`; `a2a.route.enabled` adds an HTTPRoute `/api/a2a/pick-face-replenishment`
    that Kong rewrites to `/` (the agent card stays reachable under the same prefix). It gets no Kafka env. There is no
    HPA: one replica, like MCP.

## Sensors

- `internal/architecture/a2a_fitness_test.go`: `TestA2AAdapterDependencyRule` (the adapter depends only on application
  and domain; nothing depends on it), `TestA2ASurfaceIsReadOnly` (the adapter and `cmd/a2a` run the MCP read-only
  sensor: no write use case, no outbox, event sink, Kafka, ports package or memory double, no outbox repository, unit of
  work or idempotency store in `cmd/a2a`, no event sink on its `Evaluator`; `TestEveryUseCaseIsClassifiedReadOrWrite`
  keeps the classification from going stale) and `TestA2ASDKStaysInTheInboundAdapter` (no outbound client).
- Adapter tests drive the real JSON-RPC wire (`httptest` plus `a2aclient.NewFromCard`) with a test-local envelope
  struct: card shape and truthfulness (every advertised example parses under the strict decoder and is accepted by its
  skill), both skills, paging, argument validation, in-band problems with the REST classification, adapter-owned
  details, the 500 path that hides its cause, and reads that raise no event.
- `cmd/a2a` tests: boot-config refusals, fail-fast before the listener binds, both mounts, the card route, no auth.
  The `integration` test seeds Postgres through the write use cases, reads through the real router and proves the
  `outbox_events` table is unchanged by the reads.
- Chart: `render_test.sh` (Deployment, Service, route, `/app/a2a`, `:8093`, the card URL default and override, no Kafka
  env, the env literals and required variables derived from the Go source), `test_service_selectors.py` (every Service
  selects exactly one Deployment) and `test_kong_config.py` (the a2a route rewrites to `/`).

## Consequences

- A delegating agent can ask this context about pick faces and replenishment requests without a REST or MCP client; it
  cannot change anything. The REST contract stays the only write path, and every fact still leaves as a CloudEvent
  through the transactional outbox of `cmd/api`.
- The skill set is a reviewed contract: adding a skill changes the card test and this ADR. Adding a **write** skill
  additionally needs MCP to offer the same write first, the relaxation of `TestA2ASurfaceIsReadOnly` and a new ADR.
- The agent pod needs a Postgres DSN and nothing else; it can run when Kafka is down. Its answers inherit the
  event-fed local copies' lag (ADR 0003): a pick face level can trail the inventory ledger by a few seconds.
- Not in this change: the `warehouse-infra` flip (`helm-values/pick-face-replenishment.yaml` `a2a` block with
  `enabled`, the route path and the platform `parentRefs`), the image publication to the cluster, and any client in
  another context that calls this agent.
