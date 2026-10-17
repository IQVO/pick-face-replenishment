---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
sidebar_position: 3
---

# Quickstart

## Prerequisites

| Tool | Why |
| --- | --- |
| Go 1.27.2 (`go.mod`) | build and test |
| `make` | every gate is a make target |
| Docker | `make integration` boots its own Postgres and Kafka with testcontainers; also handy for a local Postgres |
| `golangci-lint` v2.14.0, `gremlins` v0.6.0, `govulncheck` | only for `make lint`, `make mutation`, `make vuln` (each target prints the install command when the tool is missing) |
| Node 20+ | only to build this documentation site (`docs/`) |

## Make targets

| Target | What it does |
| --- | --- |
| `make build` / `make vet` / `make fmt-check` | `go build ./...`, `go vet ./...`, `gofmt -l .` must be empty |
| `make test` | `go test ./... -race`: unit, httptest and BDD suites, no database needed |
| `make coverage` | the same run with `-coverpkg=./internal/domain/...,./internal/application/...` and a 90% gate |
| `make bdd` | `go test ./... -run TestFeatures -v` (godog/Gherkin) |
| `make arch-test` | `go test ./internal/architecture/... -v` |
| `make integration` | `go test -tags=integration ./... -race -count=1` (testcontainers, needs Docker) |
| `make mutation` / `make mutation-full` | gremlins over the four domain packages, or over `./internal/domain` |
| `make check` | fast bundle: `fmt-check vet build lint test` (the lefthook pre-push hook) |
| `make check-all` | `check` + `coverage arch-test bdd` (run before pushing) |
| `make check-fast`, `make guide-lint`, `make harness-test` | agent-harness gates |

## Run in memory

Without `DATABASE_URL` the service uses in-memory adapters: there is no outbox relay, `Idempotency-Key` is not enforced
and nothing survives a restart.

```bash
go build -o /tmp/pfr-api ./cmd/api
HTTP_ADDR=:18080 /tmp/pfr-api
```

Real startup log (JSON, one object per line):

```text
{"level":"INFO","msg":"DATABASE_URL not configured; using in-memory adapters (no outbox relay, Idempotency-Key not enforced)"}
{"level":"INFO","msg":"no consumer is in kafka mode; the local copies are whatever the database holds"}
{"level":"INFO","msg":"outbox relay off: in-memory mode has no outbox"}
{"level":"INFO","msg":"http server listening","addr":":18080"}
```

In this mode the local copies (bin levels, forward map, layout) can only be fed by the Kafka consumers, so without a
broker `GET /pick-faces` stays empty:

```bash
curl -s -X PUT localhost:18080/policies/SITE-1/SKU-1 -H 'Content-Type: application/json' \
  -d '{"minQty":10,"maxQty":60,"multiple":6}'
# 200 {"siteId":"SITE-1","sku":"SKU-1","minQty":10,"maxQty":60,"multiple":6,"version":1,"declaredAt":"2026-10-17T09:20:45.952247Z"}
curl -s 'localhost:18080/pick-faces?siteId=SITE-1'
# 200 {"items":[]}
curl -s -X POST localhost:18080/replenishments -H 'Content-Type: application/json' -d '{"siteId":"SITE-1","sku":"SKU-1"}'
# 422 application/problem+json, type .../no-forward-assignment
```

## Run on Postgres

Start a throwaway database and the service. The migrations are embedded and run at boot.

```bash
docker run -d --name pfr-pg -e POSTGRES_PASSWORD=pfr -e POSTGRES_DB=pfr -p 55432:5432 postgres:16
HTTP_ADDR=:18080 'DATABASE_URL=postgres://postgres:pfr@localhost:55432/pfr?sslmode=disable' /tmp/pfr-api
```

```text
{"level":"INFO","msg":"postgres adapters configured"}
{"level":"INFO","msg":"no consumer is in kafka mode; the local copies are whatever the database holds"}
{"level":"WARN","msg":"outbox relay off: KAFKA_BROKERS is not set; events accumulate in outbox_events until it is"}
{"level":"INFO","msg":"http server listening","addr":":18080"}
```

`/readyz` answers `200 {"status":"ready"}` as soon as the listener is up. What it does and does not wait for is on the
[Runbook](/docs/operations/runbook#readiness-and-shutdown).

### Seed the local copies

The consumers are opt-in (`INVENTORY_MODE`, `SLOTTING_MODE`, `LAYOUT_MODE`, default `permissive` = not started), so in
local development the three read-model tables are simply whatever the database holds. To exercise detection without a
broker, insert the rows the consumers would have written (this is the exact seed used for the calls below):

```sql
INSERT INTO forward_assignment_maps (site_id, plan_id, approved_at) VALUES ('SITE-1','plan-demo-1','2026-10-17T08:00:00Z');
INSERT INTO forward_assignments (site_id, sku, bin) VALUES ('SITE-1','SKU-1','FWD-A01-01');
INSERT INTO zones (zone_id, site_code, zone_code) VALUES ('SITE-1-FWD','SITE-1','FWD'),('SITE-1-RSV','SITE-1','RSV');
INSERT INTO locations (location_code, zone_id, role) VALUES ('FWD-A01-01','SITE-1-FWD',''),('RSV-A07-01','SITE-1-RSV',''),('RSV-A08-01','SITE-1-RSV','');
INSERT INTO bin_stock_levels (site_code, bin, sku, on_hand, reserved, version)
  VALUES ('SITE-1','FWD-A01-01','SKU-1',8,3,1),('SITE-1','RSV-A07-01','SKU-1',120,20,1),('SITE-1','RSV-A08-01','SKU-1',50,0,1);
```

An empty `role` means a Storage location. `FWD` and `RSV` are the default zone codes (`FORWARD_ZONE_CODES`,
`RESERVE_ZONE_CODES`).

### First calls

Declare a policy, change it with `If-Match`, and look at the pick face (all outputs below are real):

```bash
curl -si -X PUT localhost:18080/policies/SITE-1/SKU-1 -H 'Content-Type: application/json' -d '{"minQty":10,"maxQty":60,"multiple":6}'
# 200, ETag: "1"
# {"siteId":"SITE-1","sku":"SKU-1","minQty":10,"maxQty":60,"multiple":6,"version":1,"declaredAt":"2026-10-17T09:16:29.914601Z"}

# the same PUT again, without If-Match
# 409 .../policy-already-exists  "send If-Match with the current policy version to replace it"

curl -si -X PUT localhost:18080/policies/SITE-1/SKU-1 -H 'If-Match: 1' -H 'Content-Type: application/json' -d '{"minQty":12,"maxQty":60,"multiple":6}'
# 200, ETag: "2"  ... "version":2

curl -s 'localhost:18080/pick-faces?siteId=SITE-1'
# {"items":[{"siteId":"SITE-1","forwardBin":"FWD-A01-01","sku":"SKU-1","onHand":8,"reserved":3,"available":5,"minQty":12,"maxQty":60,"status":"BELOW_MIN"}]}
```

Run the sweep. On Postgres the POSTs need an `Idempotency-Key`:

```bash
curl -s -X POST localhost:18080/sites/SITE-1/evaluate
# 400 .../idempotency-key-required  "send an Idempotency-Key header with a unique value per logical request"

curl -s -X POST localhost:18080/sites/SITE-1/evaluate -H 'Idempotency-Key: eval-1'
# {"siteId":"SITE-1","evaluatedAt":"2026-10-17T09:16:37.73254Z","evaluated":1,"raised":1,"healthy":0,"alreadyOpen":0,"noPolicy":0,"noSource":0,"requestIds":["repl-c4d022ba-da77-4729-bd02-ea880ad8ceaa"]}
# the same key again: the identical body is replayed, nothing new is raised
curl -s -X POST localhost:18080/sites/SITE-1/evaluate -H 'Idempotency-Key: eval-2'
# {"siteId":"SITE-1",...,"evaluated":1,"raised":0,"healthy":0,"alreadyOpen":1,"noPolicy":0,"noSource":0,"requestIds":[]}
```

The request was sized as `need = maxQty - projected = 60 - 5 = 55`, rounded down to a multiple of 6 = 54, and drawn
from the fullest reserve bin (`RSV-A07-01`, usable 120 - 20 = 100):

```bash
curl -s localhost:18080/replenishments/repl-c4d022ba-da77-4729-bd02-ea880ad8ceaa
# {"requestId":"repl-c4d022ba-...","siteId":"SITE-1","sku":"SKU-1","forwardBin":"FWD-A01-01","sourceBin":"RSV-A07-01","quantity":54,"priority":"NORMAL","state":"Requested","policyMin":12,"policyMax":60,"requestedAt":"2026-10-17T09:16:37.734717Z","version":1}

curl -s -X POST localhost:18080/replenishments -H 'Idempotency-Key: manual-1' -H 'Content-Type: application/json' -d '{"siteId":"SITE-1","sku":"SKU-1","quantity":6}'
# 409 .../replenishment-already-open  "... request repl-c4d022ba-... is open for this forward bin and sku"

curl -s -X POST localhost:18080/replenishments/repl-c4d022ba-da77-4729-bd02-ea880ad8ceaa/cancel -H 'Idempotency-Key: cancel-1'
# 200 ..."state":"Cancelled","cancelledAt":"2026-10-17T09:16:37.805476Z","cancelReason":"OPERATOR","version":2
```

The request is completed by inventory-storage's `StockRelocated` (reason `REPLENISHMENT`, `source_ref` = the request
id), which only arrives through the Kafka inventory consumer, so a broker-less run can raise and cancel but not complete.

Every state change wrote its events to `outbox_events` in the same transaction (the relay is off, so they wait there):

```text
 id | topic                                       | event_type                                                                      | subject
  1 | warehouse.pick-face-replenishment.analytics | ...policy.ReplenishmentPolicyDeclared                                           | SITE-1/SKU-1
  2 | warehouse.pick-face-replenishment.analytics | ...policy.ReplenishmentPolicyDeclared                                           | SITE-1/SKU-1
  3 | warehouse.pick-face-replenishment.events    | ...replenishment.ReplenishmentRequested                                         | repl-c4d022ba-...
  4 | warehouse.pick-face-replenishment.analytics | ...replenishment.ReplenishmentRequested                                         | repl-c4d022ba-...
  5 | warehouse.pick-face-replenishment.events    | ...replenishment.ReplenishmentCancelled                                         | repl-c4d022ba-...
  6 | warehouse.pick-face-replenishment.analytics | ...replenishment.ReplenishmentCancelled                                         | repl-c4d022ba-...
```

Finally `DELETE /policies/SITE-1/SKU-1` needs `If-Match`: without it `400 invalid-if-match`, with a stale value
`409 concurrent-modification`, with the current version `204`.

## Kafka

The fleet has **one** Kafka broker (in-cluster Bitnami, external access at `localhost:9092` from the kind cluster); the
docker-compose Kafka is retired. To run against it:

```bash
HTTP_ADDR=:18080 'DATABASE_URL=postgres://postgres:pfr@localhost:55432/pfr?sslmode=disable' \
KAFKA_BROKERS=localhost:9092 \
INVENTORY_MODE=kafka INVENTORY_CONSUMER_GROUP=pfr-local-inventory \
SLOTTING_MODE=kafka SLOTTING_CONSUMER_GROUP=pfr-local-slotting \
LAYOUT_MODE=kafka LAYOUT_CONSUMER_GROUP=pfr-local-layout \
/tmp/pfr-api
```

- Each consumer is started only when its `*_MODE` is `kafka`; then its `*_CONSUMER_GROUP` is required and so is
  `KAFKA_BROKERS`, or the process refuses to boot ([Configuration](/docs/operations/configuration#consumer-modes)).
- A new consumer group starts at the first offset, so the local copies are rebuilt by replaying the whole topic.
- Use group ids of your own: two processes sharing a group split the partitions between them.
- The relay starts when `DATABASE_URL` and `KAFKA_BROKERS` are both set and publishes to
  `warehouse.pick-face-replenishment.events` and `.analytics` on that shared broker.
- The service has no Dockerfile, Helm chart or `warehouse-infra` entry on `develop` yet, so it is not part of the kind
  cluster ([Runbook](/docs/operations/runbook#deployment-status)).

## Build this documentation

```bash
cd docs
npm ci
npm run gen-api-docs   # regenerates docs/api-reference/rest from ../apis/openapi.yaml
npm run build          # fails on broken links and anchors
```
