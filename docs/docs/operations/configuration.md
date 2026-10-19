---
id: configuration
title: Configuration
sidebar_label: Configuration
sidebar_position: 2
---

# Configuration

`cmd/api` is the main binary; `cmd/mcp`, `cmd/pfr-projector` and `cmd/pfr-reports` are separate deployables with their own,
smaller environment (sections below). Everything is read from the environment at boot by `cmd/api/main.go`, `config.go`,
`consumers.go`, `relay.go`, `adapters.go` and `internal/adapters/outbound/postgres/migrate.go`; there are no flags and
no config file. Test-only variables do not exist in the non-test code. Variables marked **yes** in *Required* have no
default and no fallback.

## `cmd/api`

| Variable | Default | Required | Meaning | Source |
| --- | --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | Listen address of the HTTP server (`ReadHeaderTimeout` is a fixed 5 s). | `cmd/api/main.go` |
| `DATABASE_URL` | none | **yes** | Postgres DSN. Missing is a boot error (`DATABASE_URL is required: this context runs on real Postgres`). The embedded migrations run first (with boot retry), then a pgx pool opens. There is no in-memory fallback. | `cmd/api/config.go`, `adapters.go` |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-pooled) DSN used only for the boot migrations, because golang-migrate's advisory lock does not survive PgBouncer transaction pooling. | `cmd/api/adapters.go`, `internal/adapters/outbound/postgres/migrate.go` |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated broker list (blanks ignored). Missing is a boot error (`KAFKA_BROKERS is required: this context runs on real Kafka`). The outbox relay and the three consumers use it, and so does the `/readyz` check. | `cmd/api/config.go` |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Relay poll interval: a Go duration (`500ms`) or plain seconds (`2`). Unset, malformed or non-positive logs a WARN and uses `1s`. | `cmd/api/config.go`, `relay.go` |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failures a row may accumulate before the relay stops claiming it. `0` is unlimited; negative or non-numeric logs a WARN and uses `20`. | `cmd/api/config.go`, `relay.go` |
| `FORWARD_ZONE_CODES` | `FWD` | no | Comma-separated zone codes that are forward pick-face zones. A forward assignment whose bin is in a known zone outside this list is not a pick face. An empty or blank list falls back to the default. | `cmd/api/config.go` |
| `RESERVE_ZONE_CODES` | `RSV` | no | Comma-separated zone codes whose Storage locations are reserve (source) bins. Same fallback rule. | `cmd/api/config.go` |
| `INVENTORY_MODE` | `kafka` | no | The only supported value is `kafka` (or empty). `permissive` is a boot error (`INVENTORY_MODE=permissive is not supported: this context runs on real Kafka`); any other value is a boot error too. | `cmd/api/consumers.go` |
| `INVENTORY_CONSUMER_GROUP` | none | **yes** | Kafka consumer group id of the inventory consumer (never a literal in code). Missing is a boot error. | `cmd/api/consumers.go` |
| `SLOTTING_MODE` | `kafka` | no | Same, for the slotting consumer. | `cmd/api/consumers.go` |
| `SLOTTING_CONSUMER_GROUP` | none | **yes** | Group id of the slotting consumer. | `cmd/api/consumers.go` |
| `LAYOUT_MODE` | `kafka` | no | Same, for the layout consumer. | `cmd/api/consumers.go` |
| `LAYOUT_CONSUMER_GROUP` | none | **yes** | Group id of the layout consumer. | `cmd/api/consumers.go` |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | Go duration to wait after `/readyz` flips to 503 and before the listener closes. `0` disables the wait; negative or malformed logs a WARN and uses `5s`. | `cmd/api/config.go` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`), `error`, case-insensitive; anything else is `info`. Logs are JSON on stdout. | `cmd/api/config.go` |

### Mandatory configuration

Postgres and Kafka are the only runtime ([ADR 0006](/docs/adr/0006-postgres-and-kafka-are-the-only-runtime)).
`loadBootConfig` refuses to boot (the process exits 1, before it opens a connection or binds the listener) when
`DATABASE_URL`, `KAFKA_BROKERS` or one of the three `*_CONSUMER_GROUP` variables is empty. Every problem is reported at
once and each message names its variable.

### Consumer modes

The three `*_MODE` variables are kept for the deployment contract, but there is one mode: every consumer always runs.
`planConsumers` refuses to boot when:

- a `*_MODE` is `permissive` (`INVENTORY_MODE=permissive is not supported: this context runs on real Kafka`);
- a `*_MODE` is anything else but `kafka` (`unknown INVENTORY_MODE "x" (want kafka)`);
- a consumer's group variable is empty (`INVENTORY_CONSUMER_GROUP is required: this context runs on real Kafka`).

### Readiness

`GET /readyz` is ready only when the shutdown drain gate is open, Postgres answers a ping and a Kafka broker answers a
metadata request that names no topic (reachability only). The verdict is cached for 3 s and a round of checks is
bounded by 2 s. Topics are **not** part of readiness: the service never creates topics through an admin API, the cluster
broker auto-creates them on first write (the relay's writer sets `AllowAutoTopicCreation`; infrastructure declares none),
and a consumed topic that does not exist yet shows as consumer retry logs, never as `503`. An unreachable broker or
database answers `503` (`dependency-unavailable`, `not ready: kafka`). `GET /healthz` is liveness only.

## `cmd/mcp`

Read-only MCP server (ADR 0007). Reads no Kafka variable, builds no outbox, and has no auth.

| Variable | Default | Required | Meaning |
| --- | --- | --- | --- |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the OLTP database (same as `cmd/api`). Missing is a boot error. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations. |
| `MCP_ADDR` | `:8090` | no | Listen address; MCP is served at `/mcp` (and `/`), `GET /healthz` for probes. |
| `FORWARD_ZONE_CODES`, `RESERVE_ZONE_CODES` | `FWD`, `RSV` | no | Same meaning as for `cmd/api`. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`, `error`. |

## `cmd/pfr-projector`

The only writer of the analytical database (ADR 0008). It never opens the OLTP database.

| Variable | Default | Required | Meaning |
| --- | --- | --- | --- |
| `ANALYTICS_DATABASE_URL` | none | **yes** | DSN of the analytical Postgres (a database separate from `DATABASE_URL`). The embedded analytical migrations run at boot. |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated brokers. |
| `ANALYTICS_CONSUMER_GROUP` | none | **yes** | Stable consumer group of the projector; a brand-new group re-reads the topic from the earliest offset (the backfill). |
| `ADMIN_ADDR` | `:8091` | no | `/healthz` and `/readyz` (503 once shutdown begins). |
| `LOG_LEVEL` | `info` | no | As above. |

A missing required variable is a boot error naming all of them.

## `cmd/pfr-reports`

Read-only reports (`GET /reports/replenishment-funnel`, `GET /reports/freshness`), no auth, never dials Kafka.

| Variable | Default | Required | Meaning |
| --- | --- | --- | --- |
| `ANALYTICS_DATABASE_URL` | none | **yes** | DSN of the analytical Postgres; opened through a read-only pool. |
| `HTTP_ADDR` | `:8092` | no | Listen address; `GET /healthz` for probes. |
| `LOG_LEVEL` | `info` | no | As above. |

## `web/`

`replenishment_mfe` has no build-time API configuration: it reads `window.__WAREHOUSE_CONFIG__.apiOrigin` from the
console's runtime `/config.json` and calls `${apiOrigin}/api/pick-face-replenishment` (the only base it ever uses). A
production build without an `apiOrigin` throws instead of falling back to a developer port. The dev server runs on
`:5195` and falls back to `http://localhost:8080` (`cmd/api`'s own default).

## Fixed values (not configurable)

These are constants in code; changing one needs a code change.

| Value | Where |
| --- | --- |
| pgx pool `MaxConns` 10, `statement_timeout` 5 s on every connection | `internal/adapters/outbound/postgres/pool.go` |
| boot retry: 5 attempts, backoff 1 s, 2 s, 4 s, 8 s (15 s in all) for the migrations and the first ping | `internal/bootretry/bootretry.go` |
| HTTP graceful drain 10 s; background workers get 10 s each to stop | `cmd/api/main.go` |
| outbox batch size 100 rows per pass | `internal/adapters/outbound/outbox/relay.go` |
| Kafka writer: acks=all, hash balancer on the key, `BatchTimeout` 10 ms | `internal/adapters/outbound/kafka/writer.go` |
| consumer retry: 200 ms doubling to a 5 s cap, 5 handling attempts before the dead-letter | `internal/adapters/inbound/kafka/kafka.go`, `deadletter.go` |
| request body limit 64 KiB; `Idempotency-Key` at most 128 characters | `internal/adapters/inbound/http/response.go`, `idempotency.go` |
| list `limit` 1 to 500, default 100 | `internal/application/usecases/usecases.go` |
| consumer claim names in `processed_events`: `pick-face-replenishment-inventory`, `-slotting`, `-layout` (not Kafka group ids) | `internal/application/usecases/intake.go` |

## Helm chart

`charts/pick-face-replenishment` deploys what [ADR 0006](/docs/adr/0006-postgres-and-kafka-are-the-only-runtime)
requires: real Postgres and real Kafka, with the three consumers always on. Every chart value maps 1:1 to a variable
above; the `*_MODE` variables are never rendered (and cannot be injected through `extraEnv`). `tests/render_test.sh`
fails when `cmd/api` reads a variable the chart does not render.

| Chart value | Env var | Required | Default |
| --- | --- | --- | --- |
| `database.url` (chart Secret) or `database.existingSecret` + `database.existingSecretKey` | `DATABASE_URL` | **yes** | none; the render fails naming `database.url or database.existingSecret` |
| `database.migrationsExistingSecretKey` | `MIGRATIONS_DATABASE_URL` | no | `MIGRATIONS_DATABASE_URL`, an `optional` key of the same Secret |
| `kafka.brokers` | `KAFKA_BROKERS` | **yes** | none; the render fails naming `kafka.brokers` |
| `config.inventoryConsumerGroup` | `INVENTORY_CONSUMER_GROUP` | **yes** | `pick-face-replenishment-inventory` (clearing it fails the render) |
| `config.slottingConsumerGroup` | `SLOTTING_CONSUMER_GROUP` | **yes** | `pick-face-replenishment-slotting` |
| `config.layoutConsumerGroup` | `LAYOUT_CONSUMER_GROUP` | **yes** | `pick-face-replenishment-layout` |
| `config.httpAddr`, `config.logLevel` | `HTTP_ADDR`, `LOG_LEVEL` | no | `:8080`, `info` |
| `config.outboxRelayInterval`, `config.outboxMaxAttempts` | `OUTBOX_RELAY_INTERVAL`, `OUTBOX_MAX_ATTEMPTS` | no | not rendered (binary: `1s`), `20` |
| `config.forwardZoneCodes`, `config.reserveZoneCodes` | `FORWARD_ZONE_CODES`, `RESERVE_ZONE_CODES` | no | not rendered (binary: `FWD`, `RSV`) |
| `config.shutdownDrainDelay` | `SHUTDOWN_DRAIN_DELAY` | no | not rendered (binary: `5s`) |

The DSNs live in a Secret only; everything else is also mounted from the chart ConfigMap with `envFrom`. Probes:
`startupProbe` and `livenessProbe` on `/healthz`, `readinessProbe` on `/readyz` (which checks Postgres and that a Kafka broker answers, not topics, so a
broker outage takes the pod out of the Service by design; period 10 s, timeout 5 s, 3 failures). The pod runs as uid
1000, non-root, with a read-only root filesystem and every capability dropped.
`terminationGracePeriodSeconds` is 40: `SHUTDOWN_DRAIN_DELAY` (5 s) + 10 s HTTP drain + 10 s consumers + 10 s relay, and
the chart refuses a value below 35. Gateway (`gatewayApi`), Ingress, HPA (`autoscaling.api`) and the three Kong plugins
(`kongPlugins.cors`, `.prometheus`, `.opentelemetry`; no auth plugin) are off by default. The CORS plugin allows the
`Idempotency-Key` and `If-Match` request headers the web remote sends.

Three optional components, each its own workload with its own `app.kubernetes.io/component` label and, where it has an
HTTP surface, its own Service (`tests/test_service_selectors.py` pins that every Service selects exactly one Deployment):

| Values block | Renders | Default |
| --- | --- | --- |
| `mcp` | Deployment + Service `<release>-mcp:8090` running `/app/mcp`; `mcp.route.enabled` adds an HTTPRoute `/api/mcp/pick-face-replenishment` rewritten to `/mcp` | off |
| `analytics` | Deployment `<release>-projector` (`/app/pfr-projector`, no Service), Deployment + Service `<release>-reports:80` (`/app/pfr-reports`), and a Secret with `ANALYTICS_DATABASE_URL` from `analytics.url` (or `analytics.existingSecret`); the chart refuses to render it without a DSN source or `analytics.consumerGroup` | off |
| `frontend` | nginx Deployment + ClusterIP Service `<release>-frontend:80` for the `replenishment_mfe` remote (image `frontend.image.*`); no route, the Nginx web gateway owns it | off |

## Not configurable here

There is no CORS, auth, TLS, tracing or metrics configuration in the service itself: it has none of those features
([Observability](/docs/operations/observability)); CORS and gateway metrics are Kong plugins rendered by the chart.
