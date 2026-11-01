---
id: runbook
title: Runbook
sidebar_label: Runbook
sidebar_position: 1
---

# Runbook

How `cmd/api` is deployed, started, drained, migrated and operated. Everything here is derived from the code on
`develop`; where a deployment artifact does not exist yet, this page says so.

## Deployment status

| Artifact | State on `develop` |
| --- | --- |
| Dockerfile | `Dockerfile` at the repository root: digest-pinned multi-stage build of every `cmd/*` binary (`api`, `mcp`, `pfr-projector`, `pfr-reports`; the chart selects one with `command: ["/app/<name>"]`), non-root uid 1000, `ENTRYPOINT ["./api"]`, port 8080. Started without environment it exits 1 naming `DATABASE_URL`, `KAFKA_BROKERS` and the three `*_CONSUMER_GROUP` variables. Built by `docker-publish` on push to `main` |
| Web remote image | `web/Dockerfile` + `web/nginx.conf` + `web/.dockerignore`: the `replenishment_mfe` remote as a static nginx-unprivileged image on port 8080; see [Frontend image](#frontend-image-web-remote). CI job `web` builds it and smoke-tests it |
| Helm chart `charts/pick-face-replenishment` | present: it deploys what [ADR 0006](/docs/adr/0006-postgres-and-kafka-are-the-only-runtime) requires (Postgres, Kafka, three consumers, no `*_MODE`); see [Configuration](/docs/operations/configuration#helm-chart). CI runs `charts/pick-face-replenishment/tests/*` in `chart-tests`, and `release` packages it on push to `main` |
| `warehouse-infra` | **registered, API and consumers only** (`origin/develop`): `terraform/apps.yaml` has `pick-face-replenishment` (`kind: service`, `database: true`; no `analytics`, no `frontend_remote`), `helm-values/pick-face-replenishment.yaml` sets the three consumer groups (`pick-face-replenishment-inventory`, `-slotting`, `-layout`) and a Gateway API route `/api/pick-face-replenishment` (Kong strips the prefix), and `terraform/dashboards/contexts/pick-face-replenishment.json` plus alert rules exist. The values file has no `mcp`, `analytics` or `frontend` block, so the MCP server, `pfr-projector`, `pfr-reports` and the web remote are **not deployed** (off by default in the chart). Cluster state was not checked |
| Kafka topics | not pre-created by the repo and declared by no infrastructure: the cluster broker auto-creates every topic on first write (fleet convention, [ADR 0006 Amendment 1](/docs/adr/0006-postgres-and-kafka-are-the-only-runtime)); the relay writer and the dead-letter writer set `AllowAutoTopicCreation` |

The image, the chart and the `warehouse-infra` entry for `cmd/api` exist. What `warehouse-infra` does not yet wire
(MCP, analytics pair, web remote) is the subject of the infra PR in flight (`feat/wire-wms-gap-components`); until it
merges, those three components are built but unused. The sections below describe what any deployment must provide.

## What a deployment needs

| Item | Value |
| --- | --- |
| Deployment | one, running `cmd/api` (REST, outbox relay and consumers are goroutines of the same process) |
| Service port | HTTP `8080` (`HTTP_ADDR`) |
| Liveness probe | `GET /healthz` returns `200 {"status":"ok"}` while the process runs; shutdown never flips it |
| Readiness probe | `GET /readyz` returns `200 {"status":"ready"}`; `503` `dependency-unavailable` while Postgres or the Kafka broker is unreachable; `503 {"status":"not_ready"}` once shutdown has begun |
| Database | a PostgreSQL the `DATABASE_URL` user may run DDL on (the migrations run at boot) |
| Secrets | the DSNs only (`DATABASE_URL`, `MIGRATIONS_DATABASE_URL`); there is no auth, API key or TLS material |
| Kafka | `KAFKA_BROKERS` (one fleet broker) for the relay and the consumers |

### Readiness and shutdown

`/readyz` is ready only when the shutdown drain gate is open, Postgres answers a ping and a Kafka broker answers a
metadata request that names no topic (reachability only; the verdict is cached for 3 s and a round of checks is bounded
by 2 s). **Topics play no part in readiness**: the broker auto-creates the service's own topics on first write, so a
pod that waited for them could never become ready and so never publish, and a consumed topic exists only once its
producer ran. A missing consumed topic shows up as consumer retry logs (`kafka reader error; retrying`, with the
consumer and the topic, [Observability](/docs/operations/observability)), not as an unready pod. The listener also only
starts after `buildAdapters` has succeeded: the embedded migrations ran (retried 5 times over about 15 s, see
`internal/bootretry`) and the first `Ping` of the pool worked. A database that never becomes reachable therefore keeps
the process from listening (it exits with the last error after the retry budget). Kafka is dialled lazily inside the
consumer and relay goroutines, so a broker outage never blocks boot; it turns the pod unready (`503`
`dependency-unavailable`, `not ready: kafka`) and shows up in the logs and as consumer lag or unpublished outbox rows.

On `SIGINT` or `SIGTERM` the order is fixed in `serveUntilSignal`:

1. `/readyz` flips to 503;
2. wait `SHUTDOWN_DRAIN_DELAY` (default 5 s, `0` disables), log `shutdown: readiness flipped to not-ready; waiting for traffic to drain`;
3. drain the HTTP server (10 s bound);
4. stop the consumers and wait for them (10 s each);
5. stop the outbox relay last, so a row a consumer enqueued while draining is still published;
6. close the pool (deferred first, runs last).

The worst case is the drain delay plus the 10 s HTTP bound plus 10 s for each running consumer plus 10 s for the relay, so
set the pod's `terminationGracePeriodSeconds` above that sum (55 s with all three consumers and the default delay).

## Frontend image (web remote)

`web/` holds the operator UI as a Module Federation remote, not a standalone app. The files that package it:

| File | Role |
| --- | --- |
| `web/Dockerfile` | multi-stage build: `node:22-bookworm-slim` builds `@warehouse/ui-kit` (a sibling checkout, supplied as the named BuildKit context `uikit`), then `npm ci && npm run build` for the remote, then copies `dist/` into `nginxinc/nginx-unprivileged` |
| `web/nginx.conf` | `listen 8080`; `GET /healthz` answers `200 ok`; `remoteEntry.js` and `index.html` are `Cache-Control: no-cache`; `/assets/` is `public, max-age=31536000, immutable` (one header); unknown paths fall back to `index.html` |
| `web/.dockerignore` | keeps the host `node_modules` and `dist` out of the build context (a macOS `node_modules` would hide the linux native bindings) |

Facts a deployer needs (from `web/vite.config.ts`):

| Item | Value |
| --- | --- |
| Federation container name | `replenishment_mfe` |
| Exposed module | `./App` |
| Entry file | `remoteEntry.js` |
| Public base path (build) | `/mfes/pick-face-replenishment/` |
| Container port | `8080` |
| Dev / preview port | `5196` (slotting-optimization holds 5194, task-interleaving 5197) |
| Image build context | `web/` with `--build-context uikit=<path to warehouse-ui-kit>` |
| Chart component | `frontend` (`frontend.enabled`, default `false`; image `warehouse/pick-face-replenishment-frontend`) |

Build and check it locally from the repository root:

```bash
docker build --build-context uikit=../warehouse-ui-kit \
  -t warehouse/pick-face-replenishment-frontend:local web
docker run --rm -p 18196:8080 warehouse/pick-face-replenishment-frontend:local
curl -s localhost:18196/healthz                                           # ok
curl -s -o /dev/null -w '%{http_code}\n' localhost:18196/remoteEntry.js   # 200
```

The Nginx web gateway of `warehouse-infra` strips the `/mfes/pick-face-replenishment` prefix before proxying to the pod,
so the pod itself sees root-relative paths. The pod serves static bytes only: the SPA reaches the REST API at
`${apiOrigin}/api/pick-face-replenishment`, with `apiOrigin` read at runtime from the console's `/config.json`
([Configuration](/docs/operations/configuration#web)). Kong never serves these assets. The CI job `web` runs lint,
`tsc -b`, the vitest suite and the production build, then builds this image and requests `/healthz` and
`remoteEntry.js` from it.

## Migrations

- Embedded: `migrations/*.sql` is compiled into the binary (`migrations.FS`); the image needs no files on disk.
- Run at boot, every boot: `postgres.RunMigrations` calls golang-migrate `Up`; "no change" is success. Two replicas
  booting together serialise on golang-migrate's advisory lock.
- `MIGRATIONS_DATABASE_URL` names a **direct** session connection for DDL; when unset the migrations use `DATABASE_URL`.
  Put PgBouncer in front of `DATABASE_URL` only, and set `MIGRATIONS_DATABASE_URL` to the database itself.
- Two migrations exist: `0001_replenishment_schema` (every table) and `0002_forward_assignment_maps` (the stale-map
  guard). They are additive: a change is a new numbered pair, an applied file is never edited, and every `down.sql`
  recreates the previous schema. The binary only ever runs `Up`; a rollback is a manual golang-migrate `down`.
- A migration that fails (bad DDL, no privilege, lock timeout) makes the process exit before it listens, with the error
  from the last attempt. Fix the database and restart; the pod stays unready meanwhile.

## Kafka topics and consumer groups

| Topic | Direction | Consumer group | Mode switch | Notes |
| --- | --- | --- | --- | --- |
| `warehouse.pick-face-replenishment.events` | produced | n/a | relay | integration events; key = request id |
| `warehouse.pick-face-replenishment.analytics` | produced | `ANALYTICS_CONSUMER_GROUP` (`cmd/pfr-projector`) | relay | the same three request events (same CloudEvents `id`) plus `ReplenishmentPolicyDeclared`; key = request id, or `<site_id>/<sku>` for the policy event. Dead-letter: `warehouse.pick-face-replenishment.analytics.dlq` |
| `warehouse.inventory.events` | consumed | value of `INVENTORY_CONSUMER_GROUP` | `INVENTORY_MODE=kafka` | acts on `BinStockLevelChanged` and `StockRelocated` only |
| `warehouse.slotting-optimization.events` | consumed | value of `SLOTTING_CONSUMER_GROUP` | `SLOTTING_MODE=kafka` | acts on `SlotPlanApproved` only |
| `warehouse.facility.events` | consumed | value of `LAYOUT_CONSUMER_GROUP` | `LAYOUT_MODE=kafka` | acts on `ZoneRegistered`, `LocationSlotRegistered`, `LocationSlotDecommissioned` |
| `warehouse.inventory.events.dlq`, `warehouse.slotting-optimization.events.dlq`, `warehouse.facility.events.dlq` | produced on failure | n/a | consumers | dead letters, see below |

There are no group id defaults: the deployment chooses them, and they must stay stable (changing one makes the consumer
start again from the first offset). Every message is a CloudEvents 1.0 structured-mode event; a message that is not one
is logged at WARN and committed past, an unknown `type` is ignored silently.

### Delivery semantics of the consumers

At-least-once with an atomic effect. For each message the CloudEvents `id` claim (`processed_events`) and the effect run
in **one** database transaction; the offset is committed only after that transaction committed. A redelivery whose claim
already exists is a `duplicate` and changes nothing. A transient failure retries the same message with backoff
(200 ms doubling to 5 s) up to 5 attempts, then the message is dead-lettered and the loop moves on. A payload of a
handled type that can never be applied (undecodable `data`, a missing required field, a `REPLENISHMENT` relocation with
quantity below 1) is dead-lettered at once, without retries. A handler that failed only because the context was
cancelled (shutdown) is never dead-lettered: its offset stays uncommitted and it is redelivered after restart.

### Dead-letter topics

`<consumed topic>.dlq`, written with acks=all, keyed by the original key, carrying the original value and headers plus:

| Header | Value |
| --- | --- |
| `x-dlq-source-topic` | topic the message came from |
| `x-dlq-source-partition` | partition |
| `x-dlq-source-offset` | offset |
| `x-dlq-error` | the error text |
| `x-dlq-failed-at` | RFC 3339 UTC time |

Procedure: read the dead letter, fix the cause (usually the producer's payload or a database problem), then republish the
original value to the source topic. The CloudEvents `id` is unchanged and the claim was rolled back with the failed
effect, so the message is processed, not skipped. A dead-letter publish that fails is retried forever and blocks the
partition rather than dropping the message.

## Outbox relay

- Writes: every state change inserts its CloudEvents messages into `outbox_events` in the same transaction as the
  aggregate. The `event_id` is minted once, so a retry republishes the same id.
- Reads: the relay runs only when `DATABASE_URL` and `KAFKA_BROKERS` are both set. Every `OUTBOX_RELAY_INTERVAL` (default
  1 s) it claims up to 100 unpublished rows in `id` order with `FOR UPDATE SKIP LOCKED`, sends them **one at a time** with
  acks=all, and marks each published. A full batch is followed immediately by another pass.
- Ordering: the pass stops at the first failing row, so a later event of the same request never overtakes an earlier one
  that is still being retried.
- Attempt cap: a failing row's `attempts` and `last_error` are recorded. When `attempts` reaches `OUTBOX_MAX_ATTEMPTS`
  (default 20) the relay logs one ERROR (`outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause`)
  and stops claiming the row; later rows overtake it. `0` means unlimited.
- Delivery is at-least-once: a crash between the Kafka ack and the `UPDATE` republishes the row with the same event id.

Inspect and re-arm (run against the service database):

```sql
-- unpublished rows and why they are stuck
SELECT id, topic, event_type, subject, attempts, last_error, created_at
FROM outbox_events WHERE published_at IS NULL ORDER BY id;

-- re-arm a row that hit the cap, after fixing the cause (the relay's own message prescribes this)
UPDATE outbox_events SET attempts = 0 WHERE id = <id>;
```

To re-publish an event that was already published, clear `published_at` on its row; the relay sends it again with the
same CloudEvents `id`, which consumers that dedupe on `id` (all fleet consumers do) will treat as a duplicate, so use it
only for a consumer that never saw the message.

## Housekeeping

There is **none**. No job deletes rows, so `outbox_events` (published rows stay forever), `processed_events` and
`idempotency_keys` grow without bound. `idx_idempotency_keys_created_at` exists to support a purge, but no purge is
implemented. Until one is, plan table maintenance by hand. Published `outbox_events` rows are safe to delete because
the relay reads only `published_at IS NULL`. Old `idempotency_keys` can be deleted at the price of losing replay
protection for those keys. Do **not** delete `processed_events` rows of messages that can still be replayed from a topic
(see the next section).

## Routine procedures

| Procedure | Steps |
| --- | --- |
| Re-run detection for a site | `POST /sites/{siteId}/evaluations` with a fresh `Idempotency-Key`. It converges: faces with an open request are `alreadyOpen`. There is no scheduler: nothing re-evaluates a site periodically, requests are raised when a bin level or forward map is applied, or by this call. |
| Rebuild the local copies | Stop the service, truncate the copy tables you want rebuilt (`bin_stock_levels`, `forward_assignments`, `forward_assignment_maps`, `zones`, `locations`), delete the matching `processed_events` rows (`consumer` is `pick-face-replenishment-inventory`, `-slotting` or `-layout`; otherwise the replay is all `duplicate`), reset the group offsets to the beginning with your Kafka tooling, start. The consumers replay the topic from the first offset and the levels converge because they are absolute and versioned. Until `bin_stock_levels` is repopulated pick faces read as level 0 (see Troubleshooting). |
| Change a group id | Stop, change `*_CONSUMER_GROUP`, start. The new group has no committed offset and replays from the first offset; the claims in `processed_events` make already-applied events `duplicate`. |
| Rotate the database password | Update `DATABASE_URL` (and `MIGRATIONS_DATABASE_URL`) in the secret and restart; the pool is opened at boot only. |
| Add a forward or reserve zone code | Set `FORWARD_ZONE_CODES` or `RESERVE_ZONE_CODES` and restart. |
| Cancel a stuck request | `POST /replenishments/{requestId}/cancellations`. A request already released as floor work is not recalled by this service. |

## Scaling notes

- **Replicas.** The HTTP side is stateless over Postgres. Overlapping relays are safe (`SKIP LOCKED`), but each replica
  runs its own relay, so strict per-request ordering holds per claim, not across two relays that interleave different
  batches; the Kafka key keeps a request's events on one partition, and a request's events are inserted in order.
  Consumers of one group split the topic's partitions; the three topics' partition counts bound consumer parallelism.
- **Database.** Each process holds at most 10 connections (`MaxConns`) with a 5 s `statement_timeout`; size Postgres (or
  PgBouncer) for replicas times 10. The chart has an HPA (`autoscaling.api`, off by default); the relay and the
  version-guarded writes are safe with N > 1, which has not been load-tested.
- **Sweep cost.** `POST /sites/{siteId}/evaluations` assesses every forward assignment of the site one unit of work at a
  time; `GET /pick-faces` assesses faces in order until a page is full. Both are linear in the number of faces.
