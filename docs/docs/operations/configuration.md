---
id: configuration
title: Configuration
sidebar_label: Configuration
sidebar_position: 2
---

# Configuration

`cmd/api` is the only binary. Everything is read from the environment at boot by `cmd/api/main.go`, `config.go`,
`consumers.go`, `relay.go`, `adapters.go` and `internal/adapters/outbound/postgres/migrate.go`; there are no flags and
no config file. Test-only variables do not exist in the non-test code.

## `cmd/api`

| Variable | Default | Required | Meaning | Source |
| --- | --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | Listen address of the HTTP server (`ReadHeaderTimeout` is a fixed 5 s). | `cmd/api/main.go` |
| `DATABASE_URL` | unset | no | Postgres DSN. Unset selects the in-memory adapters (no outbox, no relay, `Idempotency-Key` not enforced, state lost on restart). Set: the embedded migrations run first, then a pgx pool opens. | `cmd/api/main.go`, `adapters.go` |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-pooled) DSN used only for the boot migrations, because golang-migrate's advisory lock does not survive PgBouncer transaction pooling. | `cmd/api/adapters.go`, `internal/adapters/outbound/postgres/migrate.go` |
| `KAFKA_BROKERS` | unset | no, but see below | Comma-separated broker list (blanks ignored). With `DATABASE_URL` it switches the outbox relay on; any consumer in `kafka` mode requires it. | `cmd/api/config.go` |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Relay poll interval: a Go duration (`500ms`) or plain seconds (`2`). Unset, malformed or non-positive logs a WARN and uses `1s`. | `cmd/api/config.go`, `relay.go` |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failures a row may accumulate before the relay stops claiming it. `0` is unlimited; negative or non-numeric logs a WARN and uses `20`. | `cmd/api/config.go`, `relay.go` |
| `FORWARD_ZONE_CODES` | `FWD` | no | Comma-separated zone codes that are forward pick-face zones. A forward assignment whose bin is in a known zone outside this list is not a pick face. An empty or blank list falls back to the default. | `cmd/api/config.go` |
| `RESERVE_ZONE_CODES` | `RSV` | no | Comma-separated zone codes whose Storage locations are reserve (source) bins. Same fallback rule. | `cmd/api/config.go` |
| `INVENTORY_MODE` | `permissive` | no | `kafka` starts the inventory consumer; `permissive` (or empty) does not. Any other value is a boot error. | `cmd/api/consumers.go` |
| `INVENTORY_CONSUMER_GROUP` | none | when `INVENTORY_MODE=kafka` | Kafka consumer group id of the inventory consumer (never a literal in code). Missing is a boot error. | `cmd/api/consumers.go` |
| `SLOTTING_MODE` | `permissive` | no | Same, for the slotting consumer. | `cmd/api/consumers.go` |
| `SLOTTING_CONSUMER_GROUP` | none | when `SLOTTING_MODE=kafka` | Group id of the slotting consumer. | `cmd/api/consumers.go` |
| `LAYOUT_MODE` | `permissive` | no | Same, for the layout consumer. | `cmd/api/consumers.go` |
| `LAYOUT_CONSUMER_GROUP` | none | when `LAYOUT_MODE=kafka` | Group id of the layout consumer. | `cmd/api/consumers.go` |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | Go duration to wait after `/readyz` flips to 503 and before the listener closes. `0` disables the wait; negative or malformed logs a WARN and uses `5s`. | `cmd/api/config.go` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`), `error`, case-insensitive; anything else is `info`. Logs are JSON on stdout. | `cmd/api/config.go` |

### Consumer modes

Each consumer is independent. `permissive` means "no consumer": the local copies are whatever the database holds, which
is how development and the BDD suite run. `kafka` means the consumer runs and its group variable is mandatory.
`planConsumers` refuses to boot (the process exits 1) when:

- a `*_MODE` is neither `kafka` nor `permissive` (`unknown INVENTORY_MODE "x" (want kafka or permissive)`);
- a mode is `kafka` without its group (`INVENTORY_MODE=kafka requires INVENTORY_CONSUMER_GROUP`);
- any consumer is `kafka` and `KAFKA_BROKERS` is empty (`a consumer in kafka mode requires KAFKA_BROKERS`).

A consumer in `kafka` mode over the in-memory adapters (no `DATABASE_URL`) starts with a WARN, because the copies are lost
on restart while the committed offsets are kept.

### Combinations at a glance

| `DATABASE_URL` | `KAFKA_BROKERS` | Result |
| --- | --- | --- |
| unset | unset | in-memory development mode: REST only |
| set | unset | Postgres, no relay: events pile up in `outbox_events` until brokers are configured (WARN at boot) |
| set | set | Postgres and the outbox relay publishing to Kafka |
| set or unset | set, plus `*_MODE=kafka` | the matching consumers also run |

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

## Not configurable here

There is no CORS, auth, TLS, tracing or metrics configuration: the service has none of those features
([Observability](/docs/operations/observability)). The Helm chart and its values file that the CI workflow refers to
are not in the repository on `develop` ([Runbook](/docs/operations/runbook#deployment-status)).
