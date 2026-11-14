# ADR 0013: Adopt the fleet observability standard

## Status

Accepted (2026-11-14). Implements the fleet "standard metrics convention" (inventory-storage ADR 0016, wes-work-planning
ADR 0013) for this context and closes the open row of the fleet ecosystem-gaps tracker that says `cmd/api` of
pick-face-replenishment emits no metrics, no traces and no OpenTelemetry instruments. It changes no API contract, no
event payload, no schema and no domain code.

## Context

Until this ADR the service had structured logs and two health endpoints and nothing else: no `telemetry.Setup`, no HTTP
request metrics, no OTLP export, logs without a trace id, and no business instrument. Its Grafana panels that read
`http_server_request_duration_seconds` or the Go runtime metrics stayed empty for every binary, and a request could not
be followed from Kong into the service.

## Decision

The service adopts the two tiers of the fleet standard. Reference implementation copied from inventory-storage
(`telemetry.go`, `slogotel.go` and their tests, one repo throughout); the multi-binary layout follows node-topology.

### Tier 1: identical everywhere

1. **`telemetry.Setup`** (`internal/adapters/outbound/telemetry/telemetry.go`): OTLP/gRPC trace and metric exporters,
   W3C trace-context propagator, Go runtime metrics, `service.name`, `service.version` and
   `deployment.environment.name` (from `ENVIRONMENT`). An empty endpoint falls back to `DefaultEndpoint`
   (`localhost:4317`); export is lazy and non-blocking, so a missing Collector costs telemetry and never availability.
2. **One `cmd/<binary>/telemetry.go` per long-running binary**: `cmd/api`, `cmd/a2a`, `cmd/mcp`, `cmd/pfr-projector`
   and `cmd/pfr-reports`. Each reads `OTEL_SERVICE_NAME` (default `pick-face-replenishment`, `-a2a`, `-mcp`,
   `-projector`, `-reports`), `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION` (`ENVIRONMENT` is read by `Setup`), starts
   the SDK after the configuration check (a configuration error must still fail fast), and defers the flush so it runs
   after the server and the workers have stopped (5 s bound). The projector is not an HTTP API server (its admin
   mux only answers probes), so it gets `Setup` and trace-correlated logs only; the loggers of all five binaries wrap
   their JSON handler in `telemetry.WithTraceContext`, which adds `trace_id` and `span_id` to any record logged with a
   span in its context.
3. **HTTP middleware** on the REST router and on the reports router, in the order the standard fixes:
   `RequestID` -> `otelchi.Middleware` -> `otelchimetric.NewServerRequestDuration` -> `RequestLogger` -> `Recoverer`.
   The MCP and A2A routers (small chi routers with no request logger or recoverer of their own) get the two otel
   middlewares in the same order, as the reference's MCP router does.
   The span is named after the chi route pattern and `http.server.request.duration` carries `http.route` as that
   pattern, so a site id in the path never becomes a label. `/healthz` is unchanged. `RequestLogger` lives in its own
   package, `internal/adapters/inbound/requestlog`, because the MCP read-only fitness test forbids `cmd/mcp` importing
   any path containing `inbound/http`.

### Tier 2: one business counter

`replenishment.request.raised` (unit `{request}`, Prometheus series `replenishment_request_raised_total`), attributes
`outcome` (`created` or `failed`) and, only on failure, `reason` (the problem slug the API answers for the same error:
`replenishment-already-open`, `concurrent-modification`, `quantity-exceeds-max`, `no-source-stock`, `invalid-quantity`,
and `internal-error` for anything else).

- **Why this command.** The context exists to turn a below-minimum pick face into a replenishment request. Every
  creation path (the evaluation sweep, the three event consumers through `EvaluateSKU`, and the manual request, sized
  or explicit) goes through `Evaluator.plan`, so one call site counts them all. A rising `outcome=failed` share, or a
  single `reason`, means detection and the consumers fight over the same pick faces or the store is failing; a flat
  `created` series while stock levels move means the detector is not raising.
- **Placement.** Port `ports.ReplenishmentMetrics` in `internal/application/ports`; implementation
  `telemetry.ReplenishmentMetrics` in `internal/adapters/outbound/telemetry`; wired in `cmd/api` only. `Evaluator.Metrics`
  is nil-safe (the MCP and A2A read-only evaluators and every unit test leave it nil), and `internal/domain` is never
  touched.
- **Reason cannot drift.** `usecases.FailureReason` repeats five slugs of the http problem catalogue; a test in the http
  adapter (`TestFailureReasonIsTheProblemSlug`) asserts each equals `problemFor(err).slug`.
- **What it does not count.** Refusals that happen before a request is planned (`no-policy`, `nothing-to-replenish`,
  `no-forward-assignment`, an open request already found by the manual path) and detections that raise nothing
  (`healthy`, `already_open`, `no_policy`, `no_source`) are not counted by this instrument. The count is taken inside
  the unit of work, so a failure of the final commit is not separated from `created`.

### Trace propagation across Kafka: no code needed, and where it stops

The outbox repository already stores `headers` on every row and `traceHeaders` already injects the `traceparent` (and
`tracestate`) of the writing request's context from the global propagator
(`internal/adapters/outbound/postgres/outbox_repository.go`), appended where the row is written so the CloudEvents
encoders stay byte-identical. Before this ADR nothing installed a propagator, so the headers were empty; `telemetry.Setup`
now installs one, which makes that code live with no change and no migration. Limit: the span only exists for a write
that arrives over HTTP (the REST API). The Kafka consumers do not extract a trace context from the incoming message
(not evidenced in `internal/adapters/inbound/kafka`), so rows raised by an event-driven evaluation carry no
`traceparent`. This is reported, not fixed here.

### Deviations from the reference

- `normalizeEndpoint` builds its `http://` prefix with a string literal exactly as the reference does; no URL-literal
  fitness sensor in this repository rejected it (the architecture tests pass), so the `net/url` workaround the task
  anticipated was not needed.
- `golang.org/x/net` was not a dependency before; the OTLP/gRPC modules bring it and `go.mod` selects `v0.62.0`
  (`go list -m golang.org/x/net`); older versions appear in `go mod graph` only as requirements of other modules.
  `make vuln` runs on the final tree.
- Instruments the reference adds beyond the standard (for example `otelchimetric.NewServerActiveRequests`) are not
  adopted; the standard requires the duration histogram only.

### Chart

`otel.enabled`, `otel.endpoint`, `otel.serviceName`, `otel.serviceVersion` (empty falls back to `image.tag`, then the
chart `appVersion`) and a top-level `environment`. While `otel.enabled`, the four variables `OTEL_EXPORTER_OTLP_ENDPOINT`,
`OTEL_SERVICE_NAME`, `SERVICE_VERSION` and `ENVIRONMENT` are rendered into every Deployment that runs a Go binary (api,
mcp, a2a, projector, reports; the nginx frontend gets none) and are refused in every `extraEnv` list.
`<otel.serviceName>-<component>` is the service name of the non-api components.

## Consequences

- A request to the REST, reports, MCP or A2A router produces `http.server.request.duration` (proved with an in-memory
  `ManualReader` in `internal/adapters/inbound/http/observability_test.go`, `cmd/mcp/router_otel_test.go` and
  `cmd/a2a/router_otel_test.go`); the REST and reports routers also produce a server span named after the route pattern.
- New metric names for the dashboard generator (Prometheus series in parentheses): `http.server.request.duration`
  (`http_server_request_duration_seconds`), the Go runtime instruments of `contrib/instrumentation/runtime`, and
  `replenishment.request.raised` (`replenishment_request_raised_total`).
- `warehouse-infra` and its dashboards are not touched by this change.
- The only new test-environment obligation: integration tests that boot `run()` set `OTEL_SDK_DISABLED=true` so shutdown
  does not wait for a final flush to a Collector that is not there.
