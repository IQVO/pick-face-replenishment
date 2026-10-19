# pick-face-replenishment

Pick-Face Replenishment: WMS-tier Supporting bounded context of the warehouse-systems fleet. From min/max policies and event-fed bin levels it raises replenishment requests that move stock from a reserve bin to the forward pick slot, so pick faces do not run dry. Hexagonal Go, Postgres, REST, Kafka (CloudEvents 1.0).

- Design and pinned contracts: `docs/adr/` and the fleet plan `.hermes/plans/2026-10-17_005500-pick-face-replenishment-and-task-interleaving.md`.
- Scaffolded from `warehouse-harness-template` (v3). Guides: `.claude/rules/`, sensors: `HARNESS.md`.
- Commands: `make check-fast`, `make check-all`, `make integration`.

## What it does

- Holds a `MIN_MAX` policy per site and SKU (`PUT /policies/{siteId}/{sku}`, versioned with `If-Match`).
- Keeps event-fed local copies of bin stock levels (inventory-storage), the approved forward map (slotting-optimization)
  and the layout (facility-layout). It never calls a sibling over REST or MCP.
- Raises a replenishment request when a pick face's projected level reaches its minimum: from a consumed event, from the
  sweep `POST /sites/{siteId}/evaluate`, or by hand with `POST /replenishments`. The source is the fullest reserve bin.
- Completes the request from inventory-storage's `StockRelocated` (reason `REPLENISHMENT`) and publishes
  `ReplenishmentRequested`, `ReplenishmentCompleted` and `ReplenishmentCancelled` through a transactional outbox.
- `cmd/api` (REST on `:8080`, the outbox relay and three Kafka consumers, all always on). It runs on real
  Postgres and real Kafka only: there is no in-memory mode ([ADR 0006](docs/adr/0006-postgres-and-kafka-are-the-only-runtime.md)).
  No auth.
- `cmd/mcp`: a read-only MCP server (Streamable HTTP, `:8090`) over the same read use cases ([ADR 0007](docs/adr/0007-mcp-server-adoption.md)).
- `cmd/pfr-projector` + `cmd/pfr-reports`: the analytics read side, a replenishment funnel over a separate analytical
  database ([ADR 0008](docs/adr/0008-analytics-read-side.md)).
- `web/`: the `replenishment_mfe` Module Federation remote for the warehouse console.

The execution chain after `ReplenishmentRequested` is built end to end on `develop`: wes-work-planning plans one
`REPLENISH` work unit per request (its ADR 0042, consumer opt-in with `REPLENISHMENT_MODE=kafka`),
process-path-management declares the `REPLENISH` path (its ADR 0036, seeded per cluster), fulfillment-execution runs the
`REPLENISH` task (its ADR 0050), and inventory-storage applies the completed task as a `StockRelocated` with reason
`REPLENISHMENT` (its ADR 0041). It is not live: nothing is deployed and `warehouse-infra` sets no `REPLENISHMENT_MODE`;
see [Integration](docs/docs/ecosystem/integration.md).

## Run it

The service needs Postgres and Kafka and refuses to boot without `DATABASE_URL`, `KAFKA_BROKERS` and the three
`*_CONSUMER_GROUP` variables. `make run-local` brings both up with `docker-compose.yml` (a job pre-creates the five topics
locally; on the cluster the broker auto-creates them on first write and the service never creates topics through an
admin API) and runs the service on them; host ports 5432 and 9092 must be free. **Not verified
end to end:** the compose file could not be exercised on the development host, where the shared kind cluster already
holds host port 9092. The proof that the service runs on real Postgres and Kafka is the testcontainers end-to-end test
(`make integration`), which does not use this file:

```bash
make run-local     # docker compose up Postgres + Kafka + topics, then go run ./cmd/api
make local-down    # stop them (the Postgres volume is kept)
```

Or by hand against infrastructure you already have:

```bash
go build -o /tmp/pfr-api ./cmd/api
HTTP_ADDR=:18080 DATABASE_URL='postgres://...' KAFKA_BROKERS=broker:9092 \
  INVENTORY_CONSUMER_GROUP=pfr-inventory SLOTTING_CONSUMER_GROUP=pfr-slotting LAYOUT_CONSUMER_GROUP=pfr-layout \
  /tmp/pfr-api        # migrations run at boot
```

Every variable is on [Configuration](docs/docs/operations/configuration.md); the first calls are in the
[Quickstart](docs/docs/overview/quickstart.md).

## Documentation

The site is built from `docs/` (Docusaurus) and published at <https://iqvo.github.io/pick-face-replenishment/>.

| Section | Pages |
| --- | --- |
| Overview | [Introduction](docs/docs/overview/introduction.md), [Architecture](docs/docs/overview/architecture.md), [Quickstart](docs/docs/overview/quickstart.md) |
| Operations | [Configuration](docs/docs/operations/configuration.md), [Runbook](docs/docs/operations/runbook.md), [Observability](docs/docs/operations/observability.md), [Troubleshooting](docs/docs/operations/troubleshooting.md) |
| Development | [Testing](docs/docs/development/testing.md) |
| Domain-driven design | [Use cases](docs/docs/ddd/use-cases.md), [Subdomain classification](docs/docs/ddd/subdomain-classification.md), [Ubiquitous language](docs/docs/ddd/ubiquitous-language.md), [DDD artifacts index](docs/docs/ddd/ddd-artifacts.md) |
| DDD artifacts | [Core domain chart](docs/docs/ddd/core-domain-chart.md), [Bounded context canvas](docs/docs/ddd/bounded-context-canvas.md), [Context map](docs/docs/ddd/context-map.md), [Aggregate design canvas](docs/docs/ddd/aggregate-design-canvas.md), [Domain message flow](docs/docs/ddd/domain-message-flow.md), [EventStorming](docs/docs/ddd/eventstorming.md), [Class diagrams](docs/docs/ddd/class-diagram.md), [Entity-relationship](docs/docs/ddd/entity-relationship.md), [Sequence diagrams](docs/docs/ddd/sequence-diagrams.md), [Domain events](docs/docs/ddd/domain-events.md) |
| API reference | [API overview](docs/docs/api-reference/overview.md), [Event catalogue](docs/docs/api-reference/events.md), `apis/openapi.yaml`, `apis/asyncapi.yaml` |
| Ecosystem | [Integration](docs/docs/ecosystem/integration.md) |
| Architecture decisions | [ADR index](docs/adr/index.md): ADRs 0001 to 0006 in `docs/adr/` |

Build the site locally with `cd docs && npm ci && npm run build`. The `Docs` workflow builds it on every pull request and
deploys it from `main`.

## Make targets

| Target | What it runs |
| --- | --- |
| `make check` / `make check-all` | fast gate / fast gate plus coverage (90 %), architecture tests and BDD |
| `make test`, `make bdd`, `make arch-test` | unit, godog features, hexagonal and fleet fitness tests |
| `make integration` | testcontainers suites (needs Docker), including the end-to-end test that boots `run()` on real Postgres and Kafka |
| `make run-local` / `make local-down` | the service on a docker-compose Postgres + Kafka / stop them |
| `make mutation` | gremlins over the four domain packages |

See [Testing](docs/docs/development/testing.md) for the CI jobs.

## Build and deploy

| Artifact | What it is |
| --- | --- |
| `Dockerfile` | digest-pinned multi-stage build of every `cmd/*` binary (only `api` today), non-root uid 1000, migrations embedded in the binary; `docker build -t pick-face-replenishment .` |
| `charts/pick-face-replenishment` | the Helm chart that deploys what [ADR 0006](docs/adr/0006-postgres-and-kafka-are-the-only-runtime.md) requires: real Postgres and real Kafka, the three consumers always on, no `*_MODE` ever rendered |
| `charts/pick-face-replenishment/tests` | `render_test.sh`, `test_service_selectors.py`, `test_kong_config.py` (CI job `chart-tests`) |

The chart refuses to render, with a message naming the value, unless these are set:

| Value | Env var | Note |
| --- | --- | --- |
| `database.url` or `database.existingSecret` | `DATABASE_URL` (a Secret, never the ConfigMap) | `database.migrationsExistingSecretKey` adds the optional `MIGRATIONS_DATABASE_URL` |
| `kafka.brokers` | `KAFKA_BROKERS` | no default broker |
| `config.inventoryConsumerGroup`, `config.slottingConsumerGroup`, `config.layoutConsumerGroup` | `INVENTORY_CONSUMER_GROUP`, `SLOTTING_CONSUMER_GROUP`, `LAYOUT_CONSUMER_GROUP` | stable names (defaults `pick-face-replenishment-inventory`, `-slotting`, `-layout`) |

```bash
helm lint charts/pick-face-replenishment -f charts/pick-face-replenishment/ci/default-values.yaml
helm template pick-face-replenishment charts/pick-face-replenishment \
  --set database.existingSecret=pick-face-replenishment-db --set kafka.brokers=kafka:9092
bash charts/pick-face-replenishment/tests/render_test.sh
python3 charts/pick-face-replenishment/tests/test_service_selectors.py
python3 charts/pick-face-replenishment/tests/test_kong_config.py
```

Full list of values and probes: [Configuration](docs/docs/operations/configuration.md#helm-chart) and the
[Runbook](docs/docs/operations/runbook.md#deployment-status). Go test files never sit at the repository root: the godog
suite lives in `features/` and `TestNoGoFilesAtRepoRoot` keeps it that way.
