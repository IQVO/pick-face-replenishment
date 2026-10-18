---
id: testing
title: Testing
sidebar_label: Testing
sidebar_position: 1
---

# Testing

The pyramid of this repository, with the exact commands and what CI runs. Counts below are `func Test...` declarations
at the top level (sub-tests and table rows are not counted), measured on `develop`.

| Layer | Where | Size | Command |
| --- | --- | --- | --- |
| Unit | `internal/domain/...`, `internal/application/...`, adapters, `cmd/api` | 285 tests in 56 files without a build tag | `make test` |
| BDD (godog / Gherkin) | `features/*.feature` + `features/features_*_test.go` (no Go file at the repo root, enforced by `TestNoGoFilesAtRepoRoot`) | 4 feature files, 66 scenarios and 471 steps once outlines are expanded | `make bdd` |
| Integration (testcontainers) | files tagged `//go:build integration` | 43 tests in 13 files | `make integration` |
| Contract | `internal/adapters/inbound/http/contract_internal_test.go`, `internal/testing/repocontract`, golden JSON, `apis/*.yaml` linted by Spectral | see below | `make test`, CI `api-lint` |
| Mutation | `.gremlins.yaml` over `internal/domain/...` | 4 packages | `make mutation` |
| Architecture fitness | `internal/architecture` | 23 tests across 10 files | `make arch-test` |
| Evals | none in this repository | see below | |

## Unit

Plain `go test ./... -race`; no database and no broker. Use-case tests run over the in-memory adapters, the HTTP adapter
is exercised with `httptest`, the Kafka consumers with fake `Reader` and `DeadLetterWriter` implementations
(`consume_loop_test.go`, `deadletter_test.go`), the relay with a fake store and sink. `make coverage` re-runs the suite
with `-coverpkg=./internal/domain/...,./internal/application/...` and fails below **90 %**. It is the only definition of
the coverage scope: `repo_lint` rule R11 fails any workflow that sets its own `-coverpkg`.

## BDD (godog)

`TestFeatures` (in `features/features_test.go`, package `features_test`, godog `Paths: ["."]`) runs every `.feature` file
in `features/` with `Strict: true`. The repository root holds no Go file: `TestNoGoFilesAtRepoRoot` fails if one
appears. It drives the **real use cases over the in-memory
adapters** (the same use cases `cmd/api` wires over Postgres and Kafka), and feeds the local copies through the consumer
use cases with the payloads the producers' contracts define. It does not go through HTTP or Kafka. The clock is fixed
and request ids are sequential (`repl-0001`, ...).

| Feature | Scenarios | Covers |
| --- | --- | --- |
| `detection.feature` | 26 | raising at or below the minimum, URGENT, reserved stock, fullest reserve bin and partial fills, rounding to a multiple, `NO_SOURCE`, stale and redelivered levels, invalid levels, zone and decommissioned filters, pick-face listing, manual requests and their refusals, one open request per face |
| `forward_map.feature` | 5 | the forward map only moves forward in time (stale, equal-instant no-op, replacement, late older map, per-site stamps) |
| `lifecycle.feature` | 16 | completion by a matching relocation, partial relocations, duplicates, relocations that are not ours, cancellation by slot plan or operator, listing and reading requests |
| `policies.feature` | 10 | declare, change with `If-Match`, stale and missing versions, bounds, removal cancelling open requests, paging |

## Integration (testcontainers)

Every integration test boots its own Postgres (and Kafka where needed) with testcontainers on the local Docker daemon:
no `DATABASE_URL`, no docker compose, no `t.Skip` gate (`TestPostgresIntegrationTestsUseTestcontainers` and
`TestKafkaIntegrationTestsUseTestcontainers` enforce it). The suites:

| Package | What it proves |
| --- | --- |
| `cmd/api` | Postgres mode migrates, enforces `Idempotency-Key` and writes the outbox; a rejection leaves no effect and replays; concurrent requests with one key run once; boot fails closed on an unreachable database |
| `internal/adapters/outbound/postgres` | the repository contract (policy, request, read model, processed events, idempotency store) against real Postgres; migrations apply, rerun as a no-op, and every `down` recreates the previous schema; unit-of-work commit, rollback, panic and nesting; the partial unique index allows one open request per pick face; racing updates; the stale-map guard; outbox ordering, retries, attempt cap and trace context |
| `internal/adapters/outbound/outbox` | the relay against real Postgres and Kafka publishes the exact envelopes; a rolled-back unit of work publishes nothing; the attempt cap excludes a poison row and a re-armed row is claimed again |
| `internal/adapters/inbound/kafka` | the inventory, slotting and layout consumers against real Kafka and Postgres |

## Contract

There is no schemathesis job. The REST and event contracts are guarded by:

- `TestRouterServesExactlyTheDocumentedOperations`: all 12 operations of `apis/openapi.yaml` are routed and the router
  serves nothing else; `TestProblemSlugsMatchTheContract` and `TestProblemStatusesMatchTheContract`: every problem slug
  the adapter can answer has an example in the spec and every documented slug is produced, with matching statuses.
- `internal/testing/repocontract`: one behavioural contract that the in-memory repositories and the Postgres repositories
  both pass (`memory/contract_test.go` and `postgres/contract_integration_test.go`), so the BDD world behaves like
  production.
- Golden files in `internal/adapters/outbound/events/testdata/` (7 files: requested, completed and cancelled on both
  topics, and the analytics-only policy declaration) pin the exact bytes of every published CloudEvent.
- `TestEventCatalogueMatchesContract`: every full type string `apis/asyncapi.yaml` declares for this service appears in the
  CloudEvents ADR.
- CI `api-lint`: Spectral over `apis/openapi.yaml` (`.spectral.yaml`) and `apis/asyncapi.yaml`
  (`.spectral.asyncapi.yaml`) with `--fail-severity=warn`.

## Mutation (gremlins)

`.gremlins.yaml`: one worker, `timeout-coefficient` 30, thresholds `efficacy: 90` and `mutant-coverage: 90`. Gremlins fails
when the measured value is at or below the threshold, so the thresholds sit strictly below the measured 100 % / 100 % (no
lived mutants) on the four domain packages `replenishment`, `policy`, `detection` and `shared`. `make mutation` runs
those four, each on its own (the `mutation-fast` CI job, blocking on every PR). `make mutation-full` runs
`./internal/domain` as a whole (the scheduled `mutation` job). A new package under `internal/domain/` must be added to
`MUTATION_FAST_PKGS` in the Makefile and to the `mutation-fast` loop in `ci.yml`.

## Architecture fitness tests

`internal/architecture` (arch-go and source scanners):

| Test | Rule |
| --- | --- |
| `TestHexagonalArchitecture` | domain imports only domain; application only domain and application; inbound and outbound adapters never import each other; only `cmd` wires layers |
| `TestEventCatalogueMatchesContract`, `TestEventCatalogueDetector` | the catalogue ADR lists every type the asyncapi declares for this service |
| `TestCloudEventsOnly`, `TestNoEventEnvelopeToggleOrFlatEnvelope` | every Kafka message is CloudEvents 1.0; no flat envelope or toggle |
| `TestReplayConsumersSetCommitInterval` | a consumer on a process-unique group id must set `CommitInterval`; consumers on a fixed shared group (all three here) commit per message on purpose and are not flagged |
| `TestKafkaConsumerGroupNeverHardcodedInline` | group ids come from the environment |
| `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterTopicNotSetTwice` | writers use acks=all and carry no fixed topic |
| `TestOutboxRelaysPassAttemptCap` | every relay is built with `WithMaxAttempts` |
| `TestDeadLetterCallsGuardCancellation` | every function that sends to a dead-letter topic checks for a cancelled context (or carries a `//dlq:deterministic` reason), so a message that failed only because of shutdown is never dead-lettered |
| `TestNoAuthMiddlewareReintroduced` | no auth middleware (fleet decision 2026-09-11) |
| `TestNoGoFilesAtRepoRoot`, `TestNoGoFilesAtRepoRootSensorBites` | the repository root holds no `.go` file (the godog runner and steps live in `features/`) |
| `TestMCPAdapterDependencyRule`, `TestMCPOptionalToolInputsCarryOmitempty` | the rules of the MCP adapter (`internal/adapters/inbound/mcp`): dependency direction, optional tool inputs carry `omitempty` |
| `TestMCPReadOnly...` (`mcp_readonly_fitness_test.go`) | the MCP adapter and `cmd/mcp` are read-only: every use case is classified read or write, and they may not import the outbox, event sink, Kafka, memory doubles or the ports package, nor build an outbox, unit of work or idempotency repository. `governance_test.go` also fails on any write-verb tool name |
| `TestHexagonalArchitecture` analytics subtests | `internal/analytics/**` depends on nothing internal except itself; the OLTP domain and application never import the analytics store |
| `TestPostgresIntegrationTestsUseTestcontainers`, `TestKafkaIntegrationTestsUseTestcontainers` | integration tests boot their own containers |
| `Test...SensorBites` / `...SensorFailsOnBadFixtures` | each sensor is proven to fail on a bad fixture |

## Evals

`EVALS.md` in the root is the fleet's record of harness evaluations (agent runs against `inventory-storage`, runner
`tools/harness_eval.py`). This repository contains neither the runner nor results of its own, so it has no evals.

## CI jobs

`.github/workflows/ci.yml` runs on push and pull request to `main` and `develop`, weekly (Monday 06:00 UTC) and on
dispatch.

| Job | Runs | Blocks |
| --- | --- | --- |
| `lint` | golangci-lint v2.14.0 | yes |
| `guide-lint` | `scripts/harness/guide_lint.py` and `test_hook.py` (agent guides) | no (`continue-on-error`) |
| `complexity` | gocyclo, gocognit, cyclop, funlen, nestif (thresholds in `.golangci.yml`) plus an informational gocyclo report | yes |
| `test` | `go build`, `go vet`, then `make coverage` (race, 90 % gate), uploads `coverage.out` | yes |
| `bdd` | `go test ./... -run TestFeatures -v` | yes |
| `integration` | `go test -tags=integration ./... -race -count=1` | yes |
| `mutation-fast` | gremlins on the four domain packages | yes |
| `mutation` | gremlins on `./internal/domain`; opens or closes a `harness:red` issue | only on schedule / dispatch |
| `drift` | dead code, `go mod tidy -diff`, knip on `web/`, coverage-quality | only on schedule / dispatch, advisory |
| `api-lint` | Spectral on both specs | yes |
| `vuln` | `govulncheck ./...` | yes |
| `chart-tests` | `render_test.sh`, `test_service_selectors.py`, `test_kong_config.py` under `charts/pick-face-replenishment/tests` | yes |
| `arch-test` | `go test ./internal/architecture/... -v` | yes |
| `docker-publish` | build the root `Dockerfile`, push, cosign, SBOM to GHCR | only on push to `main` |
| `release` | tag, retag image, package and push the Helm chart, GitHub release | only on push to `main` |

Two more workflows: `docs.yml` builds this site on every pull request touching `docs/**`, `apis/**` or the workflow, and
deploys to GitHub Pages on push to `main`; `ai-review.yml` is an advisory review of pull requests that touch the domain,
application, `apis/` or `charts/`.

Locally: `make check` is the fast gate (`fmt-check vet build lint test`, also the lefthook pre-push hook) and `make
check-all` adds `coverage arch-test bdd`. Integration and mutation are deliberately not part of either.
