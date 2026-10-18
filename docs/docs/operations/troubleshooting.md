---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
sidebar_position: 4
---

# Troubleshooting

Symptom, cause, check and fix, built from the failure modes visible in the code. Log messages are in
[Observability](/docs/operations/observability); procedures are in the [Runbook](/docs/operations/runbook).

## Process and boot

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Process exits right after start, log `service exited with error` | database unreachable or a migration failed after 5 attempts (about 15 s) | the preceding WARN `retrying` lines name `op` (`run migrations` or `ping postgres`) and the error | fix connectivity, credentials or privileges (DDL needs a direct connection: set `MIGRATIONS_DATABASE_URL`), restart |
| Exits with `unknown INVENTORY_MODE "x" (want kafka)` (or the slotting / layout variant) | a `*_MODE` value other than `kafka` | the environment | correct the value or unset it (the default is `kafka`) |
| Exits with `INVENTORY_MODE=permissive is not supported: this context runs on real Kafka` (or the slotting / layout variant) | `permissive` was the old "do not start this consumer" mode and no longer exists | the environment | unset the variable or set `kafka` |
| Exits with `INVENTORY_CONSUMER_GROUP is required: this context runs on real Kafka` (or the slotting / layout variant) | a consumer group variable is missing | the environment | set the group id (it must stay stable) |
| Exits with `KAFKA_BROKERS is required: this context runs on real Kafka` | no brokers configured | `KAFKA_BROKERS` | set it |
| Exits with `DATABASE_URL is required: this context runs on real Postgres` | no database configured: there is no in-memory mode | `DATABASE_URL` | set it |
| Pod never becomes ready | the listener starts only after the migrations and the first ping succeeded, and `/readyz` also needs Postgres and Kafka reachable | the log: no `http server listening` line means boot is still retrying; otherwise the `/readyz` body names the failing dependency | see the first row, or restore the database or broker |
| `/readyz` answers 503 | shutdown began (SIGTERM or SIGINT), or Postgres or the Kafka broker is unreachable (a missing topic never causes it) | `shutdown: readiness flipped...` in the log, or the dependency named in the response | none while the pod is terminating; otherwise restore the dependency |

## Events not flowing out

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Nothing arrives on `warehouse.pick-face-replenishment.events` | the relay is off: no `DATABASE_URL`, or no `KAFKA_BROKERS` | log `outbox relay off: ...` | set `KAFKA_BROKERS` and restart; the queued rows are sent |
| Unpublished rows keep growing | broker down or unreachable, or the first row is failing | `SELECT id, attempts, last_error FROM outbox_events WHERE published_at IS NULL ORDER BY id LIMIT 5`; log `outbox relay pass failed` | fix the broker; the relay retries every `OUTBOX_RELAY_INTERVAL` and keeps order |
| One row sits at `attempts` = `OUTBOX_MAX_ATTEMPTS` and later events overtook it | the attempt cap was reached (log `row reached the attempt cap`) | the row's `last_error` | fix the cause, then `UPDATE outbox_events SET attempts = 0 WHERE id = <id>` |
| Events are published twice | at-least-once delivery after a crash between the ack and the `UPDATE` | the two messages carry the same CloudEvents `id` | consumers must dedupe on `id` (the fleet's do) |

## Events not flowing in

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Local copies stay empty | the upstream context has not published yet, so its topic does not exist (the broker creates a topic on its producer's first write; the service never creates a consumed topic and `/readyz` does not wait for it) | the `kafka topics` boot line, `consumer running` per consumer, ERROR `kafka reader error; retrying` naming the topic, and the consumer group's lag on the broker | wait for the producer to publish; a new group replays from the first offset |
| Consumer lag grows on one partition | the same message keeps failing with a transient error and blocks the partition until the 5th attempt | ERROR `handling failed; retrying the same message` with `partition`, `offset`, `attempt` | fix the database or dependency; after 5 attempts the message goes to the `.dlq` topic and the loop continues |
| Consumer lag grows and the log shows `dead-letter publish failed` or `offset commit failed` | the broker rejects the DLQ write or the commit; both retry forever and never skip | those ERROR lines | restore the broker, check ACLs and topic auto-creation for `<topic>.dlq` |
| Consumer not running | the goroutine ended | log `consumer stopped` with `error` | restart the process; there is no supervisor inside it |
| `.dlq` topics grow | poison payloads: undecodable `data`, blank site, bin or SKU, negative levels, `version` below 1, an assignment without `sku` or `slot`, a SKU assigned twice in one map, a `REPLENISHMENT` relocation of quantity below 1 | read the dead letter's `x-dlq-error` header | fix the producer, republish ([Runbook](/docs/operations/runbook#dead-letter-topics)) |
| `event processed` shows `"outcome":"duplicate"` | the CloudEvents `id` was already claimed (redelivery or a replay) | `processed_events` | expected; to rebuild copies also clear the matching claims |
| `"outcome":"stale"` | a bin level whose `version` is not above the stored one, or a `SlotPlanApproved` whose `approved_at` is not after the recorded one | `bin_stock_levels.version`, `forward_assignment_maps.approved_at` | expected for late or replayed messages; nothing to fix |
| `"outcome":"ignored"` on a `StockRelocated` | reason is not `REPLENISHMENT`, `source_ref` is not a request id, the request is unknown, belongs to another site, or SKU / from bin / to bin do not match, or it is no longer open | the request and the relocation payload | expected for other contexts' moves; for a real replenishment compare `source_ref` with the request id |

## Detection results

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `GET /pick-faces` returns `{"items":[]}` | no forward map applied for that `siteId` (`forward_assignments` empty), or every face is filtered out as decommissioned or as sitting in a known zone that is not in `FORWARD_ZONE_CODES` | `SELECT * FROM forward_assignments WHERE site_id = ...`; the layout copy | feed the slotting consumer (`SlotPlanApproved`), check `siteId` spelling and the zone codes |
| A face is `NO_POLICY` | no policy for that (site, SKU) | `GET /policies/{siteId}/{sku}` is 404 | `PUT /policies/{siteId}/{sku}` |
| A face is `NO_SOURCE` | the level is low but no usable reserve stock: `bin_stock_levels` has no rows for the SKU (the inventory consumer is lagging or stopped, or no `BinStockLevelChanged` has reached it yet: inventory-storage publishes one per level change and has no backfill, so a bin that has not changed since the feed shipped is unknown until its next change, inventory-storage ADR 0040), the reserve bins' zones or locations have not arrived, their zone code is not in `RESERVE_ZONE_CODES`, their role is not Storage, usable stock (`on_hand - reserved`) is 0, or less than one `multiple` fits | `SELECT * FROM bin_stock_levels WHERE sku = ...`, `locations`, `zones` | feed the missing copy, fix the zone codes or the policy `multiple` |
| A pick face is low and no request appears | an open request already counts as inbound (`AlreadyOpen`), or `projected` is above `minQty` (the rule is `projected <= minQty`), or `need` rounds down below one `multiple` | `GET /pick-faces` shows `openRequestId`; `GET /replenishments?state=Requested` | wait for the request or cancel it |
| A request is `URGENT` although the shelf has stock | a bin never seen reads as level 0 (ADR 0005), so a face whose level message has not arrived yet looks empty | `bin_stock_levels` for the forward bin | expected and accepted by the ADR; the next absolute level corrects the copy |
| A request stays `Requested` forever | completion needs a `StockRelocated` with reason `REPLENISHMENT` and `source_ref` = the request id; inventory-storage produces it for a completed `REPLENISH` task (its ADR 0041) and fulfillment-execution runs that task (its ADR 0050), and wes-work-planning plans the task's work from `ReplenishmentRequested` (its ADR 0042), but only when its `REPLENISHMENT_MODE=kafka` consumer is on (the default `permissive` consumes nothing, and `warehouse-infra` sets neither variable), and it dead-letters the requests until the `REPLENISH` path is seeded in process-path-management (its ADR 0036), so no task exists | [Integration](/docs/ecosystem/integration); the `.dlq` topic of `warehouse.pick-face-replenishment.events` on the wes-work-planning side | cancel it, or enable the wes-work-planning consumer and seed the path |
| Open requests were cancelled with `ASSIGNMENT_CHANGED` | a newer approved slot plan no longer assigns the SKU to that forward bin | the request's `cancelReason` | expected; the new face is evaluated in the same transaction |

## REST problem types

| Status | Slug | Cause | Fix |
| --- | --- | --- | --- |
| 400 | `malformed-request` | invalid JSON, unknown field, trailing data, empty body, body over 64 KiB | send a valid body |
| 400 | `idempotency-key-required` | a POST without `Idempotency-Key` (or over 128 characters) while running on Postgres | send a unique key per logical request |
| 400 | `invalid-if-match` | `If-Match` is not a non-negative integer, or a DELETE without it | send the current policy version (the `ETag`) |
| 400 | `invalid-site-id`, `invalid-sku`, `invalid-request-id` | blank, over 64 characters, bad encoding, or a request id that does not start with `repl-` | fix the identifier |
| 400 | `invalid-policy` | `minQty`/`maxQty` missing or not `0 <= min < max`, or `multiple` below 1 | fix the bounds |
| 400 | `invalid-quantity` | `quantity` below 1 | send at least 1 |
| 400 | `invalid-limit`, `invalid-cursor`, `invalid-state`, `invalid-status` | `limit` outside 1 to 500, a cursor this API did not issue, an unknown `state` or `status` filter | use the documented values |
| 404 | `policy-not-found`, `replenishment-not-found` | unknown key | check the ids |
| 409 | `policy-already-exists` | PUT without `If-Match` on an existing policy | send `If-Match` with the current version |
| 409 | `concurrent-modification` | a stale `If-Match`, `If-Match` on a policy that does not exist, or a racing write | re-read, retry |
| 409 | `replenishment-already-open` | a request is already open for that forward bin and SKU (also lost races of two sweeps: the partial unique index decides) | read it with `GET /replenishments?state=Requested`, or cancel it |
| 409 | `replenishment-not-open` | cancelling a request that is already `Completed` or `Cancelled` | none, it is final |
| 422 | `no-forward-assignment` | the site's approved map assigns no forward bin to the SKU | wait for or fix the slot plan |
| 422 | `no-policy` | the SKU has a forward bin but no policy declared at the site (with or without a `quantity`) | declare a policy first |
| 422 | `no-source-stock` | no reserve bin has usable stock for the request (or the quantity exceeds the source's usable stock) | check `bin_stock_levels`, send a smaller quantity |
| 422 | `nothing-to-replenish` | no `quantity` sent and the face is above its minimum | send a `quantity`, or leave it |
| 422 | `quantity-exceeds-max` | projected level plus `quantity` would pass `maxQty` | send a smaller quantity |
| 422 | `idempotency-key-reused` | the same key with a different method, path or body | use a new key |
| 500 | `internal-error` | an unmapped error, a recovered panic, or a database statement over the 5 s timeout | the ERROR line `request failed` or `handler panicked` has the cause; the response never contains it |

There is no 412 or 428 in this API (a missing or stale `If-Match` is 400 or 409), and no circuit breaker: the service
makes no outbound HTTP or MCP call, so the only dependencies that can fail are Postgres and Kafka.

## Idempotency-Key behaviour worth knowing

- A 4xx answer is stored and replayed for the same key and body, so retrying a rejected request with the same key
  returns the same rejection even if you have since fixed the cause (for example after the missing policy was declared).
  Use a new key after fixing the cause.
- A 5xx is never stored: the same key may retry the real work.
- Under Postgres two concurrent requests with one new key both run, but the first to store wins and the other rolls back
  and replays the winner's answer.
- On the in-memory adapters the header is ignored.
