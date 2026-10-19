# ADR 0008: Analytics read side: an analytics stream, a projector and a replenishment funnel report over a separate analytical database

## Status

Accepted (2026-10-18). This ADR fills the analytics read side that
[ADR 0004](0004-cloudevents-envelope-and-type-catalogue.md) reserved with the
topic `warehouse.pick-face-replenishment.analytics`. Additive: the OLTP API, the
integration topic `warehouse.pick-face-replenishment.events` and its payloads
are unchanged (the integration golden tests pass unchanged). No OLTP migration
is needed. Adds a second, separate database.

## Context

Every OLTP bounded context of the fleet ships an analytics read side: a
separate analytical Postgres fed by a **projector** that consumes the context's
own analytics topic, and a read-only **reports** binary over that database
(slotting-optimization ADR 0006, inbound-receiving ADR 0006,
warehouse-planning, product-master, ...). This service already writes the
analytics stream: the outbox sink turns every request event into two rows, the
integration one and the analytics twin, under one CloudEvents id, and the
analytics-only `ReplenishmentPolicyDeclared` fact (ADR 0004). Nothing consumed
it, so the stream was a topic without a reader. This ADR adds the consumer, the
analytical model and the report. It mirrors slotting-optimization ADR 0006 (the
reference implementation) and records where this service differs.

The question the data product answers is the **replenishment funnel**: how many
replenishments are requested per day and in how many units, how many complete
(and how many units that moves), how many are cancelled, how long a request
takes from raised to completed, and how often the min/max policies change.

## Decision

### 1. What is projected

All four published event types are read from
`warehouse.pick-face-replenishment.analytics` (CloudEvents 1.0, `dataschema`
`urn:warehouse:pick-face-replenishment:analytics:<EventName>:v1`):
`com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentRequested`,
`...replenishment.ReplenishmentCompleted`, `...replenishment.ReplenishmentCancelled`
and the analytics-only `...policy.ReplenishmentPolicyDeclared`. Every other type
on the topic is acknowledged and ignored. The dead-letter topic is
`warehouse.pick-face-replenishment.analytics.dlq`.

### 2. Payloads: equal to the integration payloads

The analytics payload of a request event is byte-for-byte the integration
payload (the sink builds one fact and encodes it for both streams); nothing is
added. The policy event is analytics-only and keeps the payload pinned in
`apis/asyncapi.yaml`. `apis/asyncapi.yaml` now declares the three twin messages
and the policy message on the analytics channel.

### 3. The publishing side already exists

One transaction, two topics, one id: `events.Sink` writes both outbox rows with
one CloudEvents id in the aggregate's unit of work (`UNIQUE (event_id, topic)`
since migration 0001), and `ReplenishmentPolicyDeclared` is written with the
analytics row only. This ADR changes nothing there; the end-to-end test of
`cmd/api` already asserts the analytics topic holds the policy, requested and
completed messages.

### 4. The analytical model

Facts, never aggregates, so the day buckets can be recomputed for any range
(`analytics/migrations/0001_replenishment_funnel`):

- `analytics_processed_events`: the dedupe set and the freshness source
  (`max(occurred_at)`).
- `replenishment_facts`, one row per request the projection has seen: `site_id`,
  `sku`, `priority`, `requested_quantity`, `completed_quantity`,
  `cancel_reason`, `requested_at`, `completed_at`, `cancelled_at`. Each
  milestone column is the CloudEvents `time` of the earliest such event seen,
  kept with `LEAST`; the descriptive columns keep the first non-null value, so
  arrival order never matters.
- `policy_declaration_facts`, one row per policy version
  (`site_id, sku, version`) with the bounds and the earliest `declared_at`.

### 5. The projector (`cmd/pfr-projector`)

- Consumes the analytics topic under a **fixed consumer group read from env**
  (`ANALYTICS_CONSUMER_GROUP`, required: no default, like the three OLTP
  consumers of ADR 0006). At-least-once; a brand-new group starts at the
  earliest offset so the model can be rebuilt from retained history.
- **Dedupe and effect in one transaction**: `Projection.Apply` validates the
  event, inserts the CloudEvents `id` into `analytics_processed_events`
  (`ON CONFLICT DO NOTHING`) and, only if it was inserted, folds the event into
  the fact tables in ONE analytical-database transaction. The offset is
  committed only after `Apply` returned.
- **Policy**: not a CloudEvents 1.0 message: skipped and committed past with a
  rate-limited WARN; a valid CloudEvent of another type: ignored; a known type
  whose payload contradicts its envelope (a `request_id` that does not match the
  subject, `<site_id>/<sku>` for the policy event) or fails validation, or one
  the store deterministically rejects (`report.ErrRejected`: validation,
  Postgres SQLSTATE class 22 or 23): **dead-lettered at once** (raw bytes plus
  the `x-dlq-*` headers), then committed past; a failed DLQ write retries the
  message so poison is never lost; a **transient** failure is retried with
  capped exponential backoff and never dead-lettered. Every dead-letter call
  carries the `//dlq:deterministic` reason the consumer-shutdown sensor requires.
- Boot: the embedded analytical migrations and the first ping run under
  `internal/bootretry`; Kafka is dialled lazily. `/healthz` and `/readyz` on
  `:8091`. A consumer that returns, or an admin server that fails, makes the
  process exit non-zero (`awaitStop`; the silent-projector incident of
  2026-10-12); a signal flips readiness, drains, lets the in-flight message
  finish and closes the pool.
- Missing `ANALYTICS_DATABASE_URL`, `KAFKA_BROKERS` or
  `ANALYTICS_CONSUMER_GROUP` is a boot error naming all of them. It writes only
  to the analytical database and never opens the OLTP database, and it makes no
  call to a sibling context.

### 6. The report (`cmd/pfr-reports`, `:8092`)

Read-only HTTP over the analytical database through a read-only pool
(`default_transaction_read_only=on`); `/healthz`; RFC 7807 errors with the
service's problem base URI; no auth (fleet rule); additive paths in
`apis/openapi.yaml` (tag `Reports`, server `http://localhost:8092`). The router
test and `TestReportsRouterServesTheDocumentedReports` keep the contract and the
routes in step; `cmd/api` serves no `/reports/*` route.

| endpoint | question | shape |
| --- | --- | --- |
| `GET /reports/replenishment-funnel` | the per-day replenishment funnel | `from`, `to`, `site_id?`, `granularity` (`day`, the only bucket), `days[]`: `day`, `requests_raised`, `units_requested`, `requests_completed`, `units_replenished`, `requests_cancelled`, `completion_latency{count,p50_seconds,p95_seconds}`, `policies_declared` |
| `GET /reports/freshness` | how far the projection is behind | `as_of` (newest applied CloudEvents time), `lag_seconds` (never negative); both `null` until the first event |

Rules:

- `from` / `to` are optional RFC 3339 instants; both omitted = the 30 days
  ending now; at most 366 days; `from` inclusive, `to` exclusive; an empty or
  inverted range is a 400 (`invalid-report-range`); any `granularity` but `day`
  is a 400 (`invalid-report-granularity`).
- `siteId` is optional and narrows every counter to one site.
- `days[]` is dense: one row per UTC calendar day that intersects `[from, to)`,
  zeros included. A request counts in the day its milestone `time` falls in:
  raised by `requested_at`, completed by `completed_at`, cancelled by
  `cancelled_at`, a policy version by `declared_at`.
- `completion_latency` is the requested-to-completed time of the requests
  completed in the window whose request the projection also saw; a completion
  without a seen request counts in `requests_completed` but has no latency.
  p50/p95 are linear-interpolated percentiles in seconds (PostgreSQL
  `percentile_cont`), `null` when `count` is 0.
- SQL only counts, sums and takes percentiles; the range rules, day windows,
  granularity and the freshness lag live in `internal/analytics/report` (pure,
  imports nothing internal). The in-memory store and the Postgres store run the
  same contract test.

### 7. What is deliberately NOT reported

- **Per-bin and per-SKU history** (which forward bin or SKU is replenished most):
  the events carry them, but the funnel's question is the operational cadence; a
  later report can add them from the same facts without touching this one.
- **Stock levels and the pick-face status**: these are the OLTP read model's
  job (`GET /pick-faces`); the analytics stream carries no level.
- **Cancellation breakdown by reason**: `cancel_reason` is stored on the fact, so
  a later report can split `requests_cancelled` without a migration.
- **Consumed contexts' events** (`StockRelocated`, `SlotPlanApproved`): they are
  on other contexts' topics; pulling them in would couple the analytical
  database to their contracts (ADR 0003).

### 8. Why a separate analytical database

The projection is a derived copy that can be dropped and rebuilt from the topic;
keeping it out of the OLTP database means a heavy report cannot take OLTP
connections, the two schemas evolve independently, the reports role can be
read-only on a database that holds nothing transactional, and the projector has
no credentials on OLTP data. The cost is a second role/database to provision in
`warehouse-infra`.

### 9. Packaging

The one image builds every `cmd/*` (the Dockerfile's build loop), so
`/app/pfr-projector` and `/app/pfr-reports` ship in it; the analytical
migrations are embedded. The chart gains two components, `analytics-projector`
(Deployment) and `analytics-reports` (Deployment and Service
`<release>-reports:80`), plus a Secret with `ANALYTICS_DATABASE_URL` (a single
DSN: the projector writes with it, the reports binary opens it through a
read-only pool), behind `analytics.enabled` (default `false`: the default render
is unchanged). The chart refuses to render analytics without a DSN source, a
consumer group or Kafka brokers. External routing to the reports Service is
warehouse-infra's job, not the chart's.

## Consequences

- The analytics stream has a reader; the funnel is queryable and its freshness is
  published.
- The analytics stream is not an integration contract: only `pfr-projector`
  consumes it. Other contexts keep reading `warehouse.pick-face-replenishment.events`.
- Requests raised before the stream was projected are only partly visible (a
  completion without a seen request has no latency). Replaying from the earliest
  retained offset under a new group is the backfill.
- The three hexagonal isolation rules for an `internal/analytics` region are in
  `internal/architecture`: the region depends on nothing internal except itself,
  and the OLTP domain and application layers never import it or the analytics
  store.

## Alternatives considered and rejected

- **Project from the integration topic**: the fleet convention is a dedicated
  analytics topic owned by the producer, and the analytics-only policy event
  could not be added without touching the integration contract.
- **Pre-aggregated daily counters**: cannot be re-bucketed for a partial first or
  last day, and a late or replayed event would need decrement logic. Facts are
  cheap at this volume.
- **Compute the report from the OLTP tables**: requests and their states are
  there, but not the milestone history or the policy versions, and analytical
  queries would run on the OLTP database.
- **Hour granularity**: the cadence is daily; the `granularity` parameter exists
  so that can change without a new endpoint.
