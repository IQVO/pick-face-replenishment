---
id: observability
title: Observability
sidebar_label: Observability
sidebar_position: 3
---

# Observability

The service today has **structured logs and two health endpoints, and nothing else**. There is no metrics endpoint, no
OpenTelemetry SDK, no OTLP exporter and no tracing setup in `cmd/api`: `grep` for `otel`, `prometheus` and `/metrics`
over `cmd/` and `internal/` finds only the trace-context helper described below. This page says what exists and how to
watch the service with it.

## Metrics

| Item | State |
| --- | --- |
| `/metrics` endpoint | not served (the router has only `/healthz`, `/readyz` and the 10 API operations) |
| OTel metric instruments | none: the service creates no meter, counter or histogram |
| OTLP exporter variables (`OTEL_EXPORTER_OTLP_ENDPOINT`, ...) | not read: the SDK is not wired |
| Go runtime metrics, HTTP server duration | not emitted |

Consequence: there are no service metrics to scrape from `cmd/api`. `warehouse-infra` (`origin/develop`) does ship a Grafana
dashboard (`terraform/dashboards/contexts/pick-face-replenishment.json`) and Kong-based alert rules (5xx ratio, availability
and latency SLO burn, container restarts), but they read Kong's `kong_http_requests_total` and latency histograms and
kube restarts; the panels that query `http_server_request_duration_seconds` or the Go runtime metrics stay empty for `cmd/api`
(it emits neither) and fill only for the analytics binaries once they are deployed. The state of the service has to be read
from logs, the database and Kafka itself (see [Suggested alerts](#suggested-alerts)).

## Traces

No spans are created. The only OpenTelemetry code is `traceHeaders` in
`internal/adapters/outbound/postgres/outbox_repository.go`: it asks the global propagator for the W3C trace context of
the request and appends `traceparent` (and `tracestate`, `baggage` when present) to the headers stored with each outbox
row, so a consumer could continue the trace. The composition root installs no propagator, so the global one is the
no-op default and **no trace headers are stored today**; every outbox row carries only the CloudEvents `content-type`
header. The code comment that says "once the composition root installs it" describes a wiring that has not happened.

## Logs

JSON, one object per line on stdout, via `log/slog` (`time`, `level`, `msg` plus the attributes below). `LOG_LEVEL`
selects the threshold. There is **no access log**: successful requests are not logged; only failures are.

### Boot and lifecycle

| Level | `msg` | Attributes | Meaning |
| --- | --- | --- | --- |
| INFO | `kafka topics (informational; the broker auto-creates them on first write, the service never creates them through an admin API)` | topics consumed and produced | logged at boot; topics play no part in `/readyz` |
| INFO | `postgres adapters configured` | | migrations ran and the pool pinged |
| WARN | `retrying` | `op` (`run migrations`, `ping postgres`), `attempt`, `in`, `err` | boot retry, backoff 1 s doubling |
| INFO | `succeeded after retry` | `op`, `attempt` | a retried boot step worked |
| ERROR | `service exited with error` | `error` | boot refused (migration or ping failure, bad consumer configuration) or the listener failed |
| INFO | `http server listening` | `addr` | the listener is up (the pod turns ready) |
| INFO | `shutdown: readiness flipped to not-ready; waiting for traffic to drain` | `drain_delay` | SIGINT or SIGTERM received |
| WARN | `worker did not stop before the shutdown drain deadline` | `worker` (`INVENTORY_MODE`, `SLOTTING_MODE`, `LAYOUT_MODE`, `outbox-relay`) | a worker ignored the 10 s stop bound |
| WARN | `invalid OUTBOX_RELAY_INTERVAL; using the default`, `invalid OUTBOX_MAX_ATTEMPTS; using the default`, `ignoring invalid SHUTDOWN_DRAIN_DELAY` | `value`, `default` | a malformed setting was replaced by its default |

### Outbox relay

| Level | `msg` | Attributes | Meaning |
| --- | --- | --- | --- |
| INFO | `outbox relay running` | `interval`, `max_attempts`, `brokers` | relay started (it always runs; a missing `DATABASE_URL` or `KAFKA_BROKERS` is a boot error) |
| ERROR | `outbox relay pass failed` | `error` | a Kafka or database error; retried after the interval |
| ERROR | `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | `outbox_id`, `topic`, `event_type`, `key`, `attempts`, `max_attempts`, `error` | one row will not be retried until re-armed |
| ERROR | `outbox relay stopped`, `kafka relay sink close failed` | `error` | the relay ended abnormally |

### Consumers

| Level | `msg` | Attributes | Meaning |
| --- | --- | --- | --- |
| INFO | `consumer running` | `consumer` (`inventory consumer`, `slotting consumer`, `layout consumer`), `topic`, `group_id`, `brokers` | started (all three always run) |
| INFO | `event processed` | `type`, `event_id`, `subject`, `outcome` (`applied`, `duplicate`, `stale`, `ignored`) | one message handled |
| WARN | `skipping a message that is not a valid CloudEvent` | `consumer`, `topic`, `error` | committed past, never retried |
| WARN | `dead-lettering an event that can never be applied` | `consumer`, `type`, `event_id`, `error` | poison payload |
| ERROR | `kafka reader error; retrying` | `consumer`, `topic`, `detail` | kafka-go is retrying a group join: the broker is unreachable or the consumed topic does not exist yet (its producer has not written). Not a readiness failure; the consumer recovers by itself |
| ERROR | `<consumer> handling failed; retrying the same message` | `error`, `partition`, `offset`, `attempt`, `max_attempts`, `retry_in` | transient failure, logged on every attempt |
| ERROR | `<consumer> handling failed after the maximum attempts; dead-lettering the message` | `error`, `partition`, `offset`, `attempts` | moved to the `.dlq` topic |
| ERROR | `<consumer> message can never be applied; dead-lettering it without retry` | `error`, `partition`, `offset` | poison, immediate |
| ERROR | `<consumer> dead-letter publish failed; retrying the same message`, `<consumer> offset commit failed; retrying the same message` | `error`, `partition`, `offset`, `attempt`, `retry_in` | the partition is blocked until it succeeds |
| ERROR | `consumer stopped`, `consumer close failed` | `consumer`, `error` | the loop ended with an error |

### HTTP

| Level | `msg` | Attributes | Meaning |
| --- | --- | --- | --- |
| ERROR | `request failed` | `error`, `method`, `path` | an unmapped error became a 500 `internal-error` (the response never carries the internal message) |
| ERROR | `handler panicked` | `panic`, `method`, `path` | a recovered panic became a 500 |
| WARN | `idempotency: answer not stored` | `error` | a 4xx answer could not be stored for replay; it was still sent |

Domain rejections (4xx) are not logged at all; they are visible only in the response.

## Health endpoints

| Endpoint | Healthy | Unhealthy |
| --- | --- | --- |
| `GET /healthz` | `200 {"status":"ok"}` | the process is gone |
| `GET /readyz` | `200 {"status":"ready"}` | `503 {"status":"not_ready"}` after shutdown began; `503` `dependency-unavailable` while Postgres or the Kafka broker is unreachable (topics are not checked) |

`/healthz` checks neither Postgres nor Kafka; `/readyz` checks Postgres and that a Kafka broker answers, never topics ([Runbook](/docs/operations/runbook#readiness-and-shutdown)).

## Suggested alerts

`warehouse-infra` has only the Kong-level alerts named above (5xx ratio, SLO burn, restarts). Because `cmd/api` exports no metrics, these are checks against its data stores and
logs rather than PromQL:

| Signal | Query or source | Why |
| --- | --- | --- |
| Outbox backlog | `SELECT count(*), min(created_at) FROM outbox_events WHERE published_at IS NULL` older than a few minutes | the relay is off, the broker is down or a row is stuck |
| Stuck outbox row | `SELECT id, attempts, last_error FROM outbox_events WHERE published_at IS NULL AND attempts > 0` or the log line `row reached the attempt cap` | one event is blocked behind a persistent failure |
| Consumer lag | Kafka consumer-group lag of the three groups on their topics | a blocked partition or a stopped consumer |
| Dead letters | message count growth on the three `.dlq` topics, log lines `dead-lettering` | producers sending unusable payloads |
| Consumer stopped | log `consumer stopped` or `outbox relay stopped` | the loop ended and is not restarted |
| Server errors | log lines `request failed` and `handler panicked` | every 5xx is logged once |
| Boot loops | repeated `service exited with error` / `retrying` | database unreachable or a bad migration |
| Open-request age | `SELECT min(requested_at) FROM replenishment_requests WHERE state = 'Requested'` | requests that never complete (the chain is built end to end but not live; wes-work-planning plans nothing unless `REPLENISHMENT_MODE=kafka` is set there and the `REPLENISH` path is seeded, see [Integration](/docs/ecosystem/integration)) |

Adding real instruments (request duration, outbox backlog gauge, consumer outcome counters) would be a code change; this
page documents the service as it is.
