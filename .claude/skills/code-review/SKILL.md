---
name: code-review
description: Fast pre-commit semantic review of uncommitted changes or a git range in pick-face-replenishment: domain logic in the wrong layer, missing failure-path tests, adapter concerns in domain types, impure ports. Advisory counterpart to make check. Invoke explicitly: /code-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---

Perform a pre-commit semantic code review of the current uncommitted changes
(or, if `$ARGUMENTS` names a branch or commit range, that diff instead, for
example `/code-review origin/develop..HEAD`).

This is a fast, local, advisory pass, the counterpart to `make check`'s
mechanical checks (fmt, vet, lint, test), not a replacement. Run `make check`
FIRST; do not spend attention on anything a linter already catches.

## What to look for, in priority order

1. **Domain logic leaking into the wrong layer.** Business rules belong in
   `internal/domain/` (the `Request` aggregate in `internal/domain/replenishment`,
   `Policy` in `internal/domain/policy`, `Detect` in `internal/domain/detection`).
   An HTTP handler or Kafka consumer (adapters, built in wave 2) does only
   decode, call the use case, encode. Sizing a quantity, choosing a source bin,
   deciding priority or rounding to a multiple outside those packages is a
   leak.
2. **A new use case with no failure-path test.** Every use case in the
   application layer (built in wave 2) needs a test for each domain-rule
   failure (for example exceeds max, exceeds source stock, request not open,
   concurrent modification), not just the happy path. 100% line coverage of
   the happy path often clears the 90% `make coverage` gate on its own.
3. **Decision logic written as `switch`/`case`.** The mutation gate
   (`make mutation`) reports every `case` condition NOT COVERED. Write
   decisions as `if` chains with early returns, as `State.Valid` and
   `detection.Detect` do. Keep cyclomatic and cognitive complexity under
   15 and 20, test files included.
4. **A domain type carrying an adapter concern.** JSON tags, SQL column names,
   CloudEvents types or HTTP status codes on a type under `internal/domain/`
   is a boundary violation the import-direction fitness tests will not catch.
   Domain events (`ReplenishmentRequested` and friends in
   `internal/domain/replenishment/events.go`) carry no wire type; the outbound
   adapter maps them.
5. **A driven port that is not a pure interface**, or a use case constructing a
   concrete adapter instead of depending on a port. The application layer holds
   its ports as interfaces only (built in wave 2); the only domain-level port
   today is `detection.SourceBinPolicy`.
6. **Error handling that swallows or over-wraps.** Domain sentinel errors
   (`ErrExceedsMax`, `ErrNotOpen`, `ErrSourceRefMismatch`, and the rest in
   `internal/domain/replenishment/values.go`) must map to the RFC 7807 slug in
   `apis/openapi.yaml` through one mapping table in the HTTP adapter, using
   `errors.Is`, rather than being stringified and re-wrapped. See
   `.claude/rules/rest-api.md` for the 400, 404, 409 and 422 slugs.
7. **Non-deterministic or impure detection.** `Detect` and any `SourceBinPolicy`
   must give the same outcome for the same input in any slice order, with no
   clock, I/O or randomness. A new ranking needs a total order (ties broken
   explicitly), as `FullestReserve` does.
8. **Consumer correctness.** A new consumer dispatches on the FULL `type`,
   ignores unknown types, dedupes on the CloudEvents `id` in the SAME DB
   transaction as the effect, commits the offset only after success, and skips
   (WARN, commit past) anything that is not a valid CloudEvent. A new Kafka
   `GroupID` must not be an inline string literal
   (`TestKafkaConsumerGroupNeverHardcodedInline`).
9. **A reintroduced auth/bearer/JWT check.** Every REST and MCP endpoint in this
   fleet is deliberately unauthenticated (2026-09-11 revert); a reintroduced
   check is very likely accidental. Flag it even if the code looks correct.
10. **Anything that breaks the no-sibling-call rule.** This service has no
    outbound HTTP or MCP client (ADR 0001, ADR 0003). Flag a new one.

## Output format

For each finding: file:line, a one-sentence description of the issue, and a
one-sentence suggested fix. Group findings by severity (blocking, should-fix,
nit). If nothing needs fixing, say so plainly; do not manufacture findings.

This command never modifies files or runs `git commit` or `git push`. It only
reports.
