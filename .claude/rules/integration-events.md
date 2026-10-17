---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/events/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

pick-face-replenishment both PUBLISHES and CONSUMES. It publishes the
`ReplenishmentRequest` events to `warehouse.pick-face-replenishment.events`
(integration) and mirrors them, plus the analytics-only
`ReplenishmentPolicyDeclared`, to `warehouse.pick-face-replenishment.analytics`
(read only by this service's own analytics side). It consumes three sibling
topics to feed durable local copies and to close requests (ADR 0003). Contract:
`apis/asyncapi.yaml`; decision records: `docs/adr/0003-local-copies-and-consumed-contracts.md`
and `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`.

Events leave only through the transactional outbox (request row and encoded
events in ONE database transaction, a relay drains to Kafka). The relay builds
on the scaffold writer in `internal/adapters/outbound/kafka/writer.go`: set
`Message.Topic` per message and never add a Topic to the Writer. The consumers
are in `internal/adapters/inbound/kafka`; `cmd/api` wires them.

Topics follow the fleet convention (warehouse-infra's Kafka Terraform): the
cluster broker auto-creates every business topic on first write and
infrastructure declares none, so the relay's Writer sets
`AllowAutoTopicCreation: true` (`TestNewWriterAllowsAutoTopicCreation`). The
service never calls a topic-admin API. `/readyz` needs Postgres and a Kafka
broker answering a topic-less metadata request, never topics (ADR 0006
Amendment 1); a missing consumed topic shows as consumer retry logs.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes (integration
`warehouse.<ctx>.events` AND analytics `warehouse.<ctx>.analytics`) is a
CloudEvents 1.0 event in structured content mode. This is a hard fleet rule,
not a preference — there is nothing to "choose" here:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write,
  no dual-read, no envelope toggle env var (`EVENT_ENVELOPE_MODE` is gone
  fleet-wide). `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build/validate/(un)marshal with `github.com/cloudevents/sdk-go/v2/event`
  (latest v2) via ONE helper package,
  `internal/adapters/kafka/cloudevents/` — copy it from this template's
  `templates/cloudevents/cloudevents.go.tmpl` (+ its test) and change only
  the per-repo constants. Transport stays `segmentio/kafka-go` (no sdk-go
  protocol/client packages, no hand-rolled CloudEvent structs).
- Every produced message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8`
  (`cloudevents.ContentTypeHeader()`), next to the W3C trace headers
  (`traceparent`/`tracestate` stay in headers, never duplicated into
  extension attributes). Message key = aggregate id, `kafkago.Hash{}`
  balancer.
- Required attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per
  domain event and persisted with the outbox row, so redelivery carries
  the same id); `source=/warehouse/pick-face-replenishment`; `type`; `subject` (aggregate
  instance id, never empty); `time` (domain occurred-at, UTC);
  `datacontenttype=application/json`;
  `dataschema=urn:warehouse:pick-face-replenishment:<events|analytics>:<EventName>:v<N>`.
  No custom extension attributes without an ADR.
- `type` = `com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>`
  (subdomain `wms` or `wes`; this repo's exact prefix is
  `com.warehouse.wms.pick-face-replenishment`, entity `replenishment` or
  `policy`). The SAME `type` names the
  occurrence on both the integration and the analytics topic; `dataschema`
  names the payload shape. Breaking payload change => new `.v2` type + new
  dataschema version, never mutate an existing one.
- Consumers decode with `cloudevents.Decode` (validates), dispatch on the
  FULL `type` string (never a short name or suffix match), ignore unknown
  types, read `time`/`subject` from attributes and the payload via
  `DataAs`, dedupe on `id`, and DLQ/skip — WARN log + commit past, never
  crash, never block the partition, never fall back to parsing a legacy
  shape — anything that fails CloudEvents validation.
- Tests: a golden exact-JSON test per published `type` (all attributes +
  the `content-type` header); a legacy-flat-message-rejected test per
  consumer; Kafka integration tests via testcontainers only.

Full standard, subdomain table and the fleet's cross-service type catalogue:
the CloudEvents event standard page in the warehouse-docs repository. This repo
records its own copy of the envelope and the type catalogue as ADR 0004,
`docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`;
`TestEventCatalogueMatchesContract` (`internal/architecture/catalogue_fitness_test.go`)
fails when a type in `apis/asyncapi.yaml` has no line in that ADR.

### Published types

Key and `subject` = the request id (`repl-<uuid>`), except the policy event,
whose key and `subject` are `<site_id>/<sku>`. `events` is
`warehouse.pick-face-replenishment.events`, `analytics` is
`warehouse.pick-face-replenishment.analytics`.

| `type` | topic(s) | `subject` | `dataschema` |
| --- | --- | --- | --- |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentRequested` | `events` + `analytics` | request id | `urn:warehouse:pick-face-replenishment:events:ReplenishmentRequested:v1` (analytics: `...:analytics:...`) |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCompleted` | `events` + `analytics` | request id | `urn:warehouse:pick-face-replenishment:events:ReplenishmentCompleted:v1` (analytics: `...:analytics:...`) |
| `com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCancelled` | `events` + `analytics` | request id | `urn:warehouse:pick-face-replenishment:events:ReplenishmentCancelled:v1` (analytics: `...:analytics:...`) |
| `com.warehouse.wms.pick-face-replenishment.policy.ReplenishmentPolicyDeclared` | `analytics` only | `<site_id>/<sku>` | `urn:warehouse:pick-face-replenishment:analytics:ReplenishmentPolicyDeclared:v1` |

Payloads (snake_case, optional fields omitted when unset, RFC 3339 UTC times):
`ReplenishmentRequested` `{request_id, site_id, sku, forward_bin, source_bin,
quantity, priority, policy_min, policy_max, requested_at}`;
`ReplenishmentCompleted` `{request_id, site_id, sku, forward_bin, source_bin,
quantity, completed_at}` (`quantity` is what the relocation moved);
`ReplenishmentCancelled` `{request_id, site_id, sku, reason, cancelled_at}`;
`ReplenishmentPolicyDeclared` `{site_id, sku, min_qty, max_qty, multiple,
version, declared_at}`. `priority` is `NORMAL` or `URGENT`; `reason` is
`OPERATOR`, `ASSIGNMENT_CHANGED` or `POLICY_REMOVED`. Removing a policy raises
no event of its own.

### Consumed types

Byte-identical to each producer's catalogue (ADR 0003, ADR 0004). Every other
type on these topics is ignored. Status: `BinStockLevelChanged` is pinned
consumer-first (inventory-storage producer change P1 is pending);
`StockRelocated` gains the reason `REPLENISHMENT` with producer change P4.

| `type` | topic | producer | effect | group env | mode env |
| --- | --- | --- | --- | --- | --- |
| `com.warehouse.wms.inventory-storage.stock.BinStockLevelChanged` | `warehouse.inventory.events` | inventory-storage | `bin_stock_levels` copy; apply only when `version` is greater than the stored one | `INVENTORY_CONSUMER_GROUP` | `INVENTORY_MODE` |
| `com.warehouse.wms.inventory-storage.stock.StockRelocated` | `warehouse.inventory.events` | inventory-storage | completes a request only when `reason` is `REPLENISHMENT` and `source_ref`, SKU and bins match | `INVENTORY_CONSUMER_GROUP` | `INVENTORY_MODE` |
| `com.warehouse.wms.slotting-optimization.slotplan.SlotPlanApproved` | `warehouse.slotting-optimization.events` | slotting-optimization | replaces the site's `forward_assignments` wholesale; cancels requests with `ASSIGNMENT_CHANGED` | `SLOTTING_CONSUMER_GROUP` | `SLOTTING_MODE` |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | `warehouse.facility.events` | facility-layout | zone code per zone | `LAYOUT_CONSUMER_GROUP` | `LAYOUT_MODE` |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `warehouse.facility.events` | facility-layout | `locations` copy | `LAYOUT_CONSUMER_GROUP` | `LAYOUT_MODE` |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | `warehouse.facility.events` | facility-layout | removes a location | `LAYOUT_CONSUMER_GROUP` | `LAYOUT_MODE` |

A reserve bin is a Storage location (absent `role` = Storage) in a zone listed
in `RESERVE_ZONE_CODES` (default `RSV`).

## Consumer group id and environment

Three consumers, each with a stable group id read from env, never a hardcoded
string literal: `INVENTORY_CONSUMER_GROUP`, `SLOTTING_CONSUMER_GROUP`,
`LAYOUT_CONSUMER_GROUP`.
`internal/architecture/fitness_test.go`'s
TestKafkaConsumerGroupNeverHardcodedInline enforces this (a real incident:
wes-work-planning's hardcoded group id let a locally-run e2e-tests process
silently collide with the live in-cluster Deployment's consumer group on
the shared fleet Kafka broker).

Every consumer always runs: this context has one runtime, real Postgres and real
Kafka (ADR 0006). `INVENTORY_MODE`, `SLOTTING_MODE`, `LAYOUT_MODE` keep their
names (the fleet docs name them) but default to `kafka`; `permissive` is
rejected at boot (`<NAME>=permissive is not supported: this context runs on real
Kafka`). A missing `*_CONSUMER_GROUP`, `KAFKA_BROKERS` or `DATABASE_URL` is a
boot error naming the variable (`cmd/api/consumers.go`). The local copies
are durable in Postgres and resume from the committed offset, so the groups are
stable, not unique per process (the unique-per-instance rule in HARNESS.md's
Kafka section is only for consumers that replay from FirstOffset into memory).
A new group starts at the first offset, so a fresh deployment replays each topic
and the copies converge (an unseen bin reads as level 0, ADR 0005).

| env | default | meaning |
| --- | --- | --- |
| `DATABASE_URL` | none | required: Postgres for state, idempotency and the outbox |
| `KAFKA_BROKERS` | none | required: comma-separated brokers for the consumers, the outbox relay and `/readyz` |
| `INVENTORY_MODE` | `kafka` | the inventory consumer (`warehouse.inventory.events`) always runs; `permissive` is rejected |
| `INVENTORY_CONSUMER_GROUP` | none | required: group id of the inventory consumer |
| `SLOTTING_MODE` | `kafka` | the slotting consumer (`warehouse.slotting-optimization.events`) always runs; `permissive` is rejected |
| `SLOTTING_CONSUMER_GROUP` | none | required: group id of the slotting consumer |
| `LAYOUT_MODE` | `kafka` | the layout consumer (`warehouse.facility.events`) always runs; `permissive` is rejected |
| `LAYOUT_CONSUMER_GROUP` | none | required: group id of the layout consumer |
| `RESERVE_ZONE_CODES` / `FORWARD_ZONE_CODES` | `RSV` / `FWD` | zone codes that make a location a reserve bin / a forward pick face |

There is no in-memory runtime: the local copies live in Postgres, so a restart
loses nothing while the committed offsets survive it. The memory adapters exist
for unit and BDD tests only.

Failure handling (`internal/adapters/inbound/kafka`): each message is fetched,
handled in ONE transaction together with its `processed_events` claim, and only
then committed (`FetchMessage` + `CommitMessages`). A transient failure retries
the same message with capped backoff (200ms doubling to 5s), at most five
attempts, then the message goes to `<topic>.dlq` with `x-dlq-*` headers. A
payload of a handled type that can never be applied (a missing field, a negative
level) goes to `<topic>.dlq` at once. A message that is not a CloudEvents 1.0
event (the retired flat envelope included) is skipped with a WARN and committed
past; so is an unknown type, silently. A handler that fails because the process
is stopping is never dead-lettered and never committed
(`TestDeadLetterCallsGuardCancellation`).
Consumer rules (FetchMessage + CommitMessages after success, dedupe on `id` in
the same transaction as the effect, skip invalid CloudEvents with a WARN, never
dead-letter on a cancelled context, `//dlq:deterministic` only for poison
payloads) are in `.claude/rules/fleet/kafka-testing-and-consumers.md` and
`.claude/rules/fleet/idempotency-and-outbox.md`.
