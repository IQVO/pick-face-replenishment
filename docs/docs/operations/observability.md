---
id: observability
title: Observability
sidebar_label: Observability
sidebar_position: 3
---

# Observability

The service exports **metrics and traces over OTLP/gRPC** to the OpenTelemetry Collector and writes **trace-correlated
JSON logs**, following the fleet observability standard ([ADR 0013](/docs/adr/0013-adopt-the-fleet-observability-standard)).
It serves no `/metrics` endpoint: the Collector re-publishes the metrics for Prometheus. All five binaries
(`cmd/api`, `cmd/mcp`, `cmd/a2a`, `cmd/pfr-projector`, `cmd/pfr-reports`) start `telemetry.Setup` from their own
`telemetry.go`; export is lazy and non-blocking, so an unreachable Collector costs telemetry and never availability.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC endpoint, `host:port` or a URL. Chart: `otel.endpoint`, default `otel-collector.observability.svc.cluster.local:4317` |
| `OTEL_SERVICE_NAME` | `pick-face-replenishment` (api), `-mcp`, `-a2a`, `-projector`, `-reports` | `service.name` of the spans and metrics. Chart: `otel.serviceName` for the api, `<otel.serviceName>-<component>` for the others |
| `SERVICE_VERSION` | `dev` | `service.version`. Chart: `otel.serviceVersion`, empty falls back to `image.tag`, then the chart `appVersion` |
| `ENVIRONMENT` | `local` | `deployment.environment.name`. Chart: `environment` |

The chart renders these four into every Deployment that runs a Go binary while `otel.enabled` is true (the default) and
refuses them in any `extraEnv` list. Integration tests that boot a binary set `OTEL_SDK_DISABLED=true`.

## Metrics

| Instrument | Type, unit | Attributes | Emitted by |
| --- | --- | --- | --- |
| `http.server.request.duration` (`http_server_request_duration_seconds`) | histogram, `s` | `http.route` is the chi route pattern (asserted by the router tests); the other attributes are `otelchimetric`'s defaults | `cmd/api`, `cmd/pfr-reports`, `cmd/mcp`, `cmd/a2a` |
| Go runtime instruments of `go.opentelemetry.io/contrib/instrumentation/runtime` | gauges and counters | | all five binaries |
| `replenishment.request.raised` (`replenishment_request_raised_total`) | counter, `{request}` | `outcome` (`created`, `failed`); `reason` on failure: the problem slug, e.g. `replenishment-already-open`, `concurrent-modification`, `quantity-exceeds-max`, `no-source-stock`, `invalid-quantity`, `internal-error` | `cmd/api` |

`replenishment.request.raised` counts every attempt to plan a request, from the evaluation sweep, the event consumers and
the manual request alike (all three go through one function of the use-case layer). It does not count refusals that happen
before a request is planned (`no-policy`, `nothing-to-replenish`, `no-forward-assignment`) or detections that raise
nothing, and the count is taken inside the unit of work, so a failed final commit is not told apart from `created`.

`warehouse-infra` ships a Grafana dashboard (`terraform/dashboards/contexts/pick-face-replenishment.json`) and Kong-based
alert rules (5xx ratio, availability and latency SLO burn, container restarts). This change does not touch them; whether
their `http_server_request_duration_seconds` and Go runtime panels now fill was not checked in a cluster.

## Traces

`cmd/api`, `cmd/pfr-reports`, `cmd/mcp` and `cmd/a2a` create one server span per request (`otelchi`), named after the chi
route pattern, so there is one span name per route and not one per site id. `cmd/pfr-projector` has no HTTP API and
creates no spans of its own.

The outbox stores a `traceparent` (and `tracestate`, `baggage` when present) in the `headers` of each row that is written
while the request context carries a span (`traceHeaders` in `internal/adapters/outbound/postgres/outbox_repository.go`),
and the relay copies it onto the Kafka message. `telemetry.Setup` installs the W3C propagator that code reads, so the
headers are stored for writes that arrive over the REST API. Rows raised by an event consumer carry no `traceparent`: the
Kafka consumers do not extract a trace context from the incoming message (not evidenced in
`internal/adapters/inbound/kafka`).

## Logs

JSON, one object per line on stdout, via `log/slog` (`time`, `level`, `msg` plus the attributes below). `LOG_LEVEL`
selects the threshold. A record written with a request or span context also carries `trace_id` and `span_id`, so a log
line can be joined to its trace; records written without one (boot, shutdown, workers) are unchanged.

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
| INFO, WARN (4xx), ERROR (5xx) | `http request` | `method`, `route` (the chi pattern), `status`, `duration_ms`, `bytes`, `request_id`, plus `trace_id`, `span_id` | one record per request on `cmd/api` and `cmd/pfr-reports`; `/healthz` and `/readyz` are not logged |
| ERROR | `request failed` | `error`, `method`, `path` | an unmapped error became a 500 `internal-error` (the response never carries the internal message) |
| ERROR | `handler panicked` | `panic`, `method`, `path` | a recovered panic became a 500 |
| WARN | `idempotency: answer not stored` | `error` | a 4xx answer could not be stored for replay; it was still sent |

A domain rejection (4xx) is logged only by the `http request` record at WARN (status and route, not the reason); the
reason is in the response body.

## Health endpoints

| Endpoint | Healthy | Unhealthy |
| --- | --- | --- |
| `GET /healthz` | `200 {"status":"ok"}` | the process is gone |
| `GET /readyz` | `200 {"status":"ready"}` | `503 {"status":"not_ready"}` after shutdown began; `503` `dependency-unavailable` while Postgres or the Kafka broker is unreachable (topics are not checked) |

`/healthz` checks neither Postgres nor Kafka; `/readyz` checks Postgres and that a Kafka broker answers, never topics ([Runbook](/docs/operations/runbook#readiness-and-shutdown)).

## Suggested alerts

`warehouse-infra` has only the Kong-level alerts named above (5xx ratio, SLO burn, restarts). Beyond the instruments above, the backlog and lag signals
below are not exported as metrics, so they are checks against the data stores and logs rather than PromQL (the
`replenishment_request_raised_total{outcome="failed"}` ratio and the request-duration histogram are PromQL signals once a
Collector scrape exists):

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

An outbox backlog gauge and consumer outcome counters are not instrumented (not evidenced in `internal/`); adding them
would be a code change.
