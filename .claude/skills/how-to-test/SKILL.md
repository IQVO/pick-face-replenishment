---
name: how-to-test
description: Write or review tests and diagnose a failing coverage, mutation, bdd or integration CI job in pick-face-replenishment: the four test layers, the 90% gate, gremlins threshold semantics, the testcontainers rule. Use when adding tests, killing a surviving mutant, or fixing a red check.
---

# How to test

Use when writing or reviewing tests in this repo, or diagnosing a failing
`test` (coverage), `mutation-fast`, `bdd` or `integration` CI job. The quality
bar is layered: passing `go test` is necessary but is the WEAKEST of four
signals; mutation testing exists because green tests can assert nothing.

## The four layers, in order of what they actually prove

1. **Unit tests** (`go test ./...`, `make test`): prove the code runs and
   returns something. Table-driven; the domain packages
   (`internal/domain/replenishment`, `policy`, `detection`, `shared`) are pure
   and need no fakes. Use cases (wave 2) are tested against in-memory
   implementations of their ports, never a real network or DB call.
2. **Coverage** (`make coverage`, 90% gate over
   `./internal/domain/...,./internal/application/...`, set by `COVERAGE_PKGS`
   and `COVERAGE_THRESHOLD` in the `Makefile`): proves lines executed, nothing
   about whether the test asserted the right thing.
3. **Mutation testing** (`make mutation`, gremlins over every package in
   `MUTATION_FAST_PKGS`: `replenishment`, `policy`, `detection`, `shared`):
   proves the tests actually ASSERT. A mutant is a deliberately broken copy
   (`<` becomes `<=`, `+` becomes `-`); if the suite still passes, it LIVED and
   no test would catch that bug. `make mutation-full` is the exhaustive
   scheduled run. A new package under `internal/domain/` must be added to
   `MUTATION_FAST_PKGS` and to the `mutation-fast` loop in
   `.github/workflows/ci.yml`.
4. **BDD / behaviour** (`make bdd`, godog `TestFeatures`): proves a use case
   end to end over the in-memory adapters (66 scenarios in `features/*.feature`,
   steps in `features/features_*_test.go`, package `features_test`). Never put a
   Go file at the repo root: `TestNoGoFilesAtRepoRoot` fails.

## Mutation testing: thresholds fail on `<=`

`.gremlins.yaml` sets `efficacy` and `mutant-coverage`; gremlins fails when the
MEASURED value is `<=` the threshold, so a threshold must sit strictly BELOW
what the repo meets. Measure first (`make mutation`), then set it. The comment
at the top of `.gremlins.yaml` records the last measurement. If you
deliberately lower a package's score, lower the threshold in the SAME PR with a
dated comment; never silently.

## Pitfalls that cost real CI failures

### 1. Write decisions as `if` chains, not `switch`/`case`

gremlins reports every `case` condition NOT COVERED, which drags the score
down without a real gap. `replenishment.State.Valid`, `Priority.Valid` and
`detection.Detect` are `if` chains with early returns for that reason. Keep
cyclomatic and cognitive complexity under 15 and 20 in production AND test
files (split long table tests into helpers); check with
`golangci-lint run --timeout=5m --enable-only gocyclo,gocognit,cyclop,funlen,nestif ./...`.

### 2. Zero or identical fixtures hide arithmetic mutants

`Projected = OnHand - Reserved + open quantity` and `Usable = OnHand - Reserved`
survive a `-` to `+` mutant if a test uses `Reserved: 0`. Use fixtures where
every operand is distinct and non-zero (for example on hand 17, reserved 5,
open 3) and assert the exact value, not just "no error".

### 3. Boundary guards need the boundary value

`policy` requires `0 <= MinQty < MaxQty`, `Multiple >= 1`, `Version >= 1`;
`replenishment.Plan` requires `quantity >= 1` and `projected + quantity <= max`;
`Detect` raises only when `Projected <= MinQty`. A `<` to `<=` mutant survives
unless a test hits the exact boundary: `MinQty == 0`, `MinQty == MaxQty - 1`,
`MinQty == MaxQty`, `quantity == 1`, `projected + quantity == max` (accepted)
and `max + 1` (rejected), `Projected == MinQty` (raise) and `MinQty + 1`
(healthy). `RoundDown` also needs a value below one multiple (returns 0), an
exact multiple, and one more than a multiple.

### 4. Order independence

`detection.Detect` and `FullestReserve` promise the same outcome in any slice
order. Assert it by permuting the reserve slice, with a tie on usable stock and
a repeated bin code, so a ranking mutant is killed.

### 5. Equivalent mutants: know when NOT to chase them

A mutant that only diverges on a structurally unreachable or unspecified case
(a tie the spec leaves open, a guard that cannot fire) is not worth a contrived
fixture; pinning an arbitrary order as an invariant is worse than an accepted
survivor. Record the reasoning in the PR (or a mutation triage note if the repo
adds one) and move on.

## Diagnosing a `mutation-fast` CI failure

```bash
gremlins unleash ./internal/domain/replenishment   # on your branch, per package
```

Compare the LIVED set against `origin/develop`; only entries NEW on your branch
are your regression. Today every domain package measures 0 lived mutants, so any
LIVED mutant is yours.

## Kafka/Postgres integration tests: testcontainers, never a skip-gate

A `-tags=integration` test touching Kafka or Postgres MUST start its own
container through `testcontainers-go`. Never gate on `os.Getenv("KAFKA_BROKERS")`
or `os.Getenv("DATABASE_URL")` with `t.Skip(...)`, and never hardcode
`localhost:9092`. CI's `integration` job provisions NO service containers
(`make integration` needs only Docker), so a skip-gated test silently skips
there. Fitness tests enforce it (`TestKafkaIntegrationTestsUseTestcontainers`,
`TestPostgresIntegrationTestsUseTestcontainers` in `internal/architecture/`).
Share one container per package through a helper, use a unique topic per test,
create topics explicitly and wait for the partition leader before the first
read or write. There are no integration tests yet (the adapters arrive in wave
2); the CloudEvents helper has unit tests in
`internal/adapters/kafka/cloudevents/cloudevents_test.go`, and the build-tagged
code must still compile: `go build -tags=integration ./...` and
`go vet -tags=integration ./...`.

## Verify before opening the PR

```bash
make check-all   # check + coverage + arch-test + bdd (the full local gate)
make mutation    # CI runs it as mutation-fast; check-all does not
make vuln        # CI runs it too
```
