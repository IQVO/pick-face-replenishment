# pick-face-replenishment

Pick-Face Replenishment: a WMS-tier **Supporting** bounded context of the
warehouse-systems fleet. From min/max policies and event-fed bin levels it
raises replenishment requests that move stock from a reserve bin to the
forward pick slot, so pick faces do not run dry. It does not execute moves
itself and never calls a sibling over REST or MCP: it keeps event-fed local
copies of bin stock levels (inventory-storage), the approved forward map
(slotting-optimization), the layout (facility-layout) and pack levels
(product-master), and publishes `ReplenishmentRequested`,
`ReplenishmentCompleted` and `ReplenishmentCancelled` through a transactional
outbox. Hexagonal Go, Postgres, REST, Kafka (CloudEvents 1.0). Design and
pinned contracts: `docs/adr/`; scaffolded from `warehouse-harness-template`
(v3) — guides in `.claude/rules/`, sensors in `HARNESS.md`.

## Disclaimer

This project is a domain-driven design study. It is not associated with,
endorsed by or affiliated with any company, and it uses no company's code,
data, logos or trademarks. All names, figures and examples are illustrative
or synthetic.

---

## Documentation

The site is built from `docs/` (Docusaurus) and published at
**<https://iqvo.github.io/pick-face-replenishment/>**; the `Docs` workflow
builds it on every pull request and deploys it from `main`. Build locally
with `cd docs && npm ci && npm run build`.

| Area | Pages |
| --- | --- |
| Overview | [Introduction](docs/docs/overview/introduction.md), [Architecture](docs/docs/overview/architecture.md), [Quickstart](docs/docs/overview/quickstart.md) |
| Operations | [Configuration](docs/docs/operations/configuration.md), [Runbook](docs/docs/operations/runbook.md), [Observability](docs/docs/operations/observability.md), [Troubleshooting](docs/docs/operations/troubleshooting.md) |
| Development | [Testing](docs/docs/development/testing.md) |
| Domain-driven design | [Use cases](docs/docs/ddd/use-cases.md), [Subdomain classification](docs/docs/ddd/subdomain-classification.md), [Ubiquitous language](docs/docs/ddd/ubiquitous-language.md), [DDD artifacts index](docs/docs/ddd/ddd-artifacts.md) |
| DDD artifacts | [Core domain chart](docs/docs/ddd/core-domain-chart.md), [Bounded context canvas](docs/docs/ddd/bounded-context-canvas.md), [Context map](docs/docs/ddd/context-map.md), [Aggregate design canvas](docs/docs/ddd/aggregate-design-canvas.md), [Domain message flow](docs/docs/ddd/domain-message-flow.md), [EventStorming](docs/docs/ddd/eventstorming.md), [Class diagrams](docs/docs/ddd/class-diagram.md), [Entity-relationship](docs/docs/ddd/entity-relationship.md), [Sequence diagrams](docs/docs/ddd/sequence-diagrams.md), [Domain events](docs/docs/ddd/domain-events.md) |
| API reference | [API overview](docs/docs/api-reference/overview.md), [Event catalogue](docs/docs/api-reference/events.md), `apis/openapi.yaml`, `apis/asyncapi.yaml` |
| Ecosystem | [Integration](docs/docs/ecosystem/integration.md) |
| Decisions | [ADR index](docs/adr/index.md) — ADRs 0001 to 0010 |

---

## Architecture

Hexagonal / ports & adapters; dependencies point inward only.

```text
apis/                     openapi.yaml, asyncapi.yaml (the pinned contracts)
cmd/api/                  the composition root: REST router, the four Kafka consumers, the outbox relay
cmd/mcp/                  read-only MCP server (ADR 0007)
cmd/a2a/                  read-only A2A agent (ADR 0010)
cmd/pfr-projector/        analytics projector — the only analytical-DB writer (ADR 0008)
cmd/pfr-reports/          analytics reader (:8092)
internal/domain/          policy, replenishment, pickface and the shared value types (four packages)
internal/application/     usecases, ports, repository, outbox, idempotency
internal/adapters/        inbound/{http,kafka,mcp,a2a}, outbound/{postgres,memory,events,outbox,kafka,analyticsstore,system}
internal/architecture/    hexagonal and fleet fitness tests (incl. TestNoGoFilesAtRepoRoot)
features/                 godog scenarios (the repo root holds no .go file)
docs/                     Docusaurus site (docs/docs) and the ADRs (docs/adr)
charts/pick-face-replenishment/  Helm chart (values, templates, tests/)
web/                      the replenishment_mfe Module Federation remote
```

The layering is enforced by the executable fitness tests in
`internal/architecture` (`make arch-test`, CI job `arch-test`).

What the context does, functionally:

- Holds a `MIN_MAX` policy per site and SKU (`PUT /policies/{siteId}/{sku}`,
  versioned with `If-Match`).
- Raises a replenishment request when a pick face's projected level reaches
  its minimum: from a consumed event, from the sweep
  `POST /sites/{siteId}/evaluate`, or by hand with `POST /replenishments`.
  The source is the fullest reserve bin (pack-aware: a request can carry
  multiple pack-derived units, ADR 0009).
- Completes the request from inventory-storage's `StockRelocated` (reason
  `REPLENISHMENT`).

The execution chain after `ReplenishmentRequested` is built end to end on
`develop`: wes-work-planning plans one `REPLENISH` work unit per request (its
ADR 0042, consumer opt-in with `REPLENISHMENT_MODE=kafka`),
process-path-management declares the `REPLENISH` path (its ADR 0036, seeded
per cluster), fulfillment-execution runs the `REPLENISH` task (its ADR 0050),
and inventory-storage applies the completed task as a `StockRelocated` with
reason `REPLENISHMENT` (its ADR 0041). In `warehouse-infra`
(`origin/develop`) the API and its consumers are registered, and
wes-work-planning's `helm-values` set `REPLENISHMENT_MODE=kafka` (group
`wes-work-planning-replenishment`); the `REPLENISH` path is still an
operator seed step (process-path-management `seed_catalogue.py`, post-deploy
seed 1) and this repository's MCP server, analytics pair and web remote are
not wired there. Cluster state was not checked; see
[Integration](docs/docs/ecosystem/integration.md).

---

## Domain events

All messages are **CloudEvents 1.0**; the type convention is
`com.warehouse.wms.pick-face-replenishment.<entity>.<EventName>` (the `wms`
subdomain segment).

**Publishes** (through the transactional outbox):

| Topic | Messages | Notes |
|---|---|---|
| `warehouse.pick-face-replenishment.events` | `ReplenishmentRequested`, `ReplenishmentCompleted`, `ReplenishmentCancelled` (`com.warehouse.wms.pick-face-replenishment.replenishment.*`) | The integration topic. |
| `warehouse.pick-face-replenishment.analytics` | the same three (as `…Analytics`), plus `ReplenishmentPolicyDeclared` (`…policy.ReplenishmentPolicyDeclared`) — analytics-only | Consumed solely by this service's own `cmd/pfr-projector` (ADR 0008). |

**Consumes** — four always-on local-copy consumers (ADR 0003, ADR 0009;
every other type ignored, `permissive` is rejected at boot):

| Topic | Messages | Consumer group |
|---|---|---|
| `warehouse.inventory.events` | `BinStockLevelChanged`, `StockRelocated` (reason `REPLENISHMENT` completes a request) | `INVENTORY_CONSUMER_GROUP` |
| `warehouse.slotting-optimization.events` | `SlotPlanApproved` (the approved forward map) | `SLOTTING_CONSUMER_GROUP` |
| `warehouse.facility.events` | `ZoneRegistered`, `LocationSlotRegistered`, `LocationSlotDecommissioned` | `LAYOUT_CONSUMER_GROUP` |
| `warehouse.product-master.events` | `ProductPackLevelDeclared`, `ProductPackLevelRetired` (ADR 0009) | `PACK_LEVEL_CONSUMER_GROUP` |

Contracts are pinned in `apis/asyncapi.yaml`.

---

## Running it

Postgres and Kafka are the only runtime (ADR 0006): there is no in-memory
mode, no consumer that is off and no optional relay. `cmd/api` refuses to
boot without `DATABASE_URL`, `KAFKA_BROKERS` and all four `*_CONSUMER_GROUP`
variables. The service never creates topics through an admin API: the
cluster broker auto-creates them on first write. Migrations run at boot.

```bash
make run-local     # docker compose up Postgres + Kafka + topics, then go run ./cmd/api
make local-down    # stop them (the Postgres volume is kept)
```

Host ports 5432 and 9092 must be free. **The compose file is not verified
end to end** (the development host's shared kind cluster holds host port
9092); the proof that the service runs on real Postgres and Kafka is the
testcontainers end-to-end test in `make integration`, which does not use it.
Note: the compose `topics` job pre-creates five topics with the broker's
auto-creation off, and `warehouse.product-master.events` (consumed by the
pack-level consumer, ADR 0009) is not among them — locally that consumer
stays idle until the topic exists.

Or by hand against infrastructure you already have:

```bash
go build -o /tmp/pfr-api ./cmd/api
HTTP_ADDR=:18080 DATABASE_URL='postgres://...' KAFKA_BROKERS=broker:9092 \
  INVENTORY_CONSUMER_GROUP=pfr-inventory SLOTTING_CONSUMER_GROUP=pfr-slotting \
  LAYOUT_CONSUMER_GROUP=pfr-layout PACK_LEVEL_CONSUMER_GROUP=pfr-pack-levels \
  /tmp/pfr-api
```

### Configuration

`cmd/api`:

| Env var | Default | Meaning |
|---|---|---|
| `HTTP_ADDR` | `:8080` | REST listen address |
| `DATABASE_URL` | **required** | Postgres DSN; boot error when unset |
| `MIGRATIONS_DATABASE_URL` | *(unset = `DATABASE_URL`)* | Direct DSN for the boot migrations |
| `KAFKA_BROKERS` | **required** | Comma-separated brokers |
| `INVENTORY_CONSUMER_GROUP` | **required** | Stable group of the inventory consumer |
| `SLOTTING_CONSUMER_GROUP` | **required** | Stable group of the slotting consumer |
| `LAYOUT_CONSUMER_GROUP` | **required** | Stable group of the layout consumer |
| `PACK_LEVEL_CONSUMER_GROUP` | **required** | Stable group of the pack-level consumer (ADR 0009) |
| `INVENTORY_MODE` / `SLOTTING_MODE` / `LAYOUT_MODE` / `PACK_LEVEL_MODE` | *(unset = `kafka`)* | Only `kafka`; `permissive` is rejected at boot |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval (Go duration or seconds) |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row (`0` = unlimited) |
| `FORWARD_ZONE_CODES` | `FWD` | Comma-separated zone codes of forward pick faces |
| `RESERVE_ZONE_CODES` | `RSV` | Comma-separated zone codes of reserve bins |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait after flipping `/readyz` before closing |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |

The other binaries:

| Binary | Env vars |
|---|---|
| `cmd/mcp` | `MCP_ADDR` (`:8090`), `DATABASE_URL` (**required**), `FORWARD_ZONE_CODES`, `RESERVE_ZONE_CODES`, `LOG_LEVEL` |
| `cmd/a2a` | `A2A_ADDR` (`:8093`), `A2A_AGENT_URL` (**required**, absolute URL advertised on the Agent Card), `DATABASE_URL` (**required**), `FORWARD_ZONE_CODES`, `RESERVE_ZONE_CODES`, `LOG_LEVEL` |
| `cmd/pfr-projector` | `ANALYTICS_DATABASE_URL` (**required**), `KAFKA_BROKERS` (**required**), `ANALYTICS_CONSUMER_GROUP`, `ADMIN_ADDR` (`:8091`), `LOG_LEVEL` |
| `cmd/pfr-reports` | `ANALYTICS_DATABASE_URL` (**required**), `HTTP_ADDR` (`:8092`), `LOG_LEVEL` |

Every variable is on [Configuration](docs/docs/operations/configuration.md);
the first calls are in the [Quickstart](docs/docs/overview/quickstart.md).

---

## Observability

The service today has **structured logs and two health endpoints, and
nothing else** (see [Observability](docs/docs/operations/observability.md),
which proves the absence): no `/metrics` endpoint, no OpenTelemetry SDK, no
OTLP exporter and no tracing setup — the router registers only `/healthz`,
`/readyz` and the API operations. What exists:

- **Logs**: JSON (`log/slog`) on stdout with `time`, `level`, `msg` and
  structured fields; level from `LOG_LEVEL` (default `info`).
- **Health**: `/healthz` (liveness) and `/readyz` (the drain gate AND a
  Postgres ping AND a Kafka metadata check, cached for a few seconds).
- **Trace-context helper**: the outbox injects the W3C `traceparent` of the
  writing request into the row's headers so a relay copy carries it onto the
  Kafka message (no tracer provider is installed today).
- `warehouse-infra` ships a Grafana dashboard
  (`terraform/dashboards/contexts/pick-face-replenishment.json`) and
  Kong-based alert rules (5xx ratio, availability) for this context.

---

## API

REST on `HTTP_ADDR` (default `:8080`), specified in `apis/openapi.yaml`.
Errors are **RFC 7807** `application/problem+json`. No auth. Fourteen
operations over eleven paths:

| Method | Path | Purpose |
|---|---|---|
| GET | `/policies` | List policies |
| PUT | `/policies/{siteId}/{sku}` | Declare the `MIN_MAX` policy (versioned, `If-Match`) |
| GET | `/policies/{siteId}/{sku}` | Get one policy |
| DELETE | `/policies/{siteId}/{sku}` | Remove the policy |
| GET | `/pick-faces` | The forward pick faces with stock, policy bounds, open request and status (`BELOW_MIN`, `OK`, `NO_SOURCE`, `NO_POLICY`) |
| POST | `/replenishments` | Raise a request by hand. **All three POSTs are wrapped by the fleet `Idempotency-Key` middleware** (same key + body replays the original response) |
| GET | `/replenishments` | List requests (newest first) |
| GET | `/replenishments/{requestId}` | One request |
| POST | `/replenishments/{requestId}/cancel` | Cancel a request |
| POST | `/sites/{siteId}/evaluate` | Sweep: evaluate every pick face of a site |
| GET | `/healthz`, `/readyz` | liveness; readiness (drain gate + Postgres + Kafka) |
| GET | `/reports/replenishment-funnel`, `/reports/freshness` | analytics — served by `cmd/pfr-reports`, not `cmd/api` |

### Analytics data product (Replenishment Funnel)

ADR 0008. `cmd/pfr-projector` consumes
`warehouse.pick-face-replenishment.analytics` (group
`ANALYTICS_CONSUMER_GROUP`) and is the **only writer** of the separate
analytical database (`ANALYTICS_DATABASE_URL`); `cmd/pfr-reports` serves
`GET /reports/replenishment-funnel` and `GET /reports/freshness` read-only
on `:8092`.

---

## Helm chart

| Artifact | What it is |
| --- | --- |
| `Dockerfile` | digest-pinned multi-stage build of every `cmd/*` binary, non-root uid 1000, migrations embedded in the binary; `docker build -t pick-face-replenishment .` |
| `charts/pick-face-replenishment` | the Helm chart that deploys what [ADR 0006](docs/adr/0006-postgres-and-kafka-are-the-only-runtime.md) requires: real Postgres and real Kafka, the four consumers always on, no `*_MODE` ever rendered |
| `charts/pick-face-replenishment/tests` | `render_test.sh`, `test_service_selectors.py`, `test_kong_config.py` (CI job `chart-tests`) |

The chart refuses to render, with a message naming the value, unless these
are set:

| Value | Env var | Note |
| --- | --- | --- |
| `database.url` or `database.existingSecret` | `DATABASE_URL` (a Secret, never the ConfigMap) | `database.migrationsExistingSecretKey` adds the optional `MIGRATIONS_DATABASE_URL` |
| `kafka.brokers` | `KAFKA_BROKERS` | no default broker |
| `config.inventoryConsumerGroup`, `config.slottingConsumerGroup`, `config.layoutConsumerGroup`, `config.packLevelConsumerGroup` | `INVENTORY_CONSUMER_GROUP`, `SLOTTING_CONSUMER_GROUP`, `LAYOUT_CONSUMER_GROUP`, `PACK_LEVEL_CONSUMER_GROUP` | stable names (defaults `pick-face-replenishment-inventory`, `-slotting`, `-layout`, `-pack-levels`) |

```bash
helm lint charts/pick-face-replenishment -f charts/pick-face-replenishment/ci/default-values.yaml
helm template pick-face-replenishment charts/pick-face-replenishment \
  --set database.existingSecret=pick-face-replenishment-db --set kafka.brokers=kafka:9092
bash charts/pick-face-replenishment/tests/render_test.sh
python3 charts/pick-face-replenishment/tests/test_service_selectors.py
python3 charts/pick-face-replenishment/tests/test_kong_config.py
```

The optional MCP (`mcp.enabled`) and A2A (`a2a.enabled`) components render
only when enabled. Full list of values and probes:
[Configuration](docs/docs/operations/configuration.md#helm-chart) and the
[Runbook](docs/docs/operations/runbook.md#deployment-status).

### Running the MCP server in Kubernetes

`mcp.enabled=true` renders the MCP Deployment + Service on port `8090`
(ADR 0007). A read-only MCP server (Streamable HTTP, served at `/` and
`/mcp`, health at `/healthz`, no auth) over the same read use cases as REST,
with five tools: `list_policies`, `get_policy`, `list_pick_faces`,
`list_replenishments`, `get_replenishment`. No write tool; the build fails
if one appears.

### Running the A2A agent in Kubernetes

`a2a.enabled=true` renders the agent (Service `<release>-a2a:8093`, optional
Kong route `/api/a2a/pick-face-replenishment`). `cmd/a2a` delegates nothing
and changes nothing: it answers two goal-shaped reads over the SAME use
cases as REST and MCP (A2A carries delegation of a goal; facts still leave
as CloudEvents through the outbox of `cmd/api`). It is read-only because the
MCP charter is (ADR 0010), unauthenticated like every fleet endpoint, and
refuses to boot without `DATABASE_URL` and `A2A_AGENT_URL`.

| Skill | Arguments | Answers |
| --- | --- | --- |
| `pick_face_status` | `siteId` (required), `status`, `limit`, `cursor` | the forward pick faces of the site with stock, policy bounds, open request and status (`BELOW_MIN`, `OK`, `NO_SOURCE`, `NO_POLICY`) |
| `replenishment_lookup` | `requestId`, or any of `siteId`, `sku`, `state`, `limit`, `cursor` | one replenishment request, or a newest-first list |

```bash
A2A_AGENT_URL=http://localhost:8093/ DATABASE_URL='postgres://...' go run ./cmd/a2a
curl -s localhost:8093/.well-known/agent-card.json
```

Rejections come back in-band as RFC 7807-shaped `problem`s with the REST
statuses and slugs.

---

## Operator micro-frontend (`web/`)

`web/` is `replenishment_mfe`, this context's Module Federation remote for
the warehouse console (production asset base
`/mfes/pick-face-replenishment/`), talking only to this service's own REST
API and never part of `make check`.

```bash
cd web && npm install && npm run dev     # http://localhost:5196
```

In the cluster the chart's optional `frontend` component (off by default)
serves the built bundle behind warehouse-infra's Nginx web gateway; the
remote is not yet registered in `warehouse-infra`. CI job `web` lints, tests
and builds it.

---

## Local development / quality gate

| Target | What it runs |
| --- | --- |
| `make check` / `make check-all` | fast gate / fast gate plus coverage (90%), architecture tests and BDD |
| `make check-fast` | quickest gate |
| `make test`, `make bdd`, `make arch-test` | unit, godog features, hexagonal and fleet fitness tests |
| `make integration` | testcontainers suites (needs Docker), including the end-to-end test that boots `run()` on real Postgres and Kafka |
| `make run-local` / `make local-down` | the service on a docker-compose Postgres + Kafka / stop them |
| `make mutation` | gremlins over the four domain packages |
| `make vuln` | `govulncheck ./...` |

lefthook runs `fmt-check`, `vet` and `lint` on pre-commit and `make check`
on pre-push (`lefthook install` once per clone). Go test files never sit at
the repository root: the godog suite lives in `features/` and
`TestNoGoFilesAtRepoRoot` keeps it that way.

## Tests

- **Unit, handler and BDD** (`make test`, `make bdd`): no database; godog
  features in `features/`.
- **Integration** (`make integration`, CI job `integration`):
  testcontainers Postgres/Kafka — 18 `*_integration_test.go` suites covering
  the repository contract, use-case wiring, the outbox relay, the CloudEvents
  publisher, every local-copy consumer (including the pack-level consumer),
  the projector's consumer, the analytical store, the REST router
  (RFC 7807, Idempotency-Key replay), MCP and A2A wiring, and the
  end-to-end analytics path (ADR 0008).
- **Architecture fitness**: `internal/architecture` (`make arch-test`, CI
  `arch-test`).
- **Mutation**: gremlins over the domain packages — `mutation-fast` on every
  push/PR, the exhaustive `mutation` run on schedule.
- **Web**: the remote has its own vitest suite (CI job `web`).

CI (`.github/workflows/ci.yml`) runs on every push and PR: `lint`,
`guide-lint`, `complexity`, `test` (with the 90% coverage gate), `bdd`,
`integration`, `mutation-fast`, `api-lint`, `vuln` (govulncheck),
`chart-tests`, `arch-test`, `web`, plus the scheduled `mutation` and
advisory `drift`; `docker-publish` pushes the image and `release` tags the
next version and publishes the chart.

## Architecture Decision Records

The ADR index lives at [docs/adr/index.md](docs/adr/index.md) — 10 decisions
(0001–0010), from the bounded context to the A2A inbound adapter.

## License

MIT — see [LICENSE](LICENSE).
