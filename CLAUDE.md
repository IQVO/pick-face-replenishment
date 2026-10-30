# CLAUDE.md — pick-face-replenishment

`pick-face-replenishment` is a Supporting bounded context (`wms` tier) of the `warehouse-systems` fleet (GitHub org `IQVO`, Go module `github.com/claudioed/pick-face-replenishment`). It keeps forward pick faces stocked: from min/max policies and event-fed bin levels it raises replenishment requests that move stock from a reserve bin to the forward pick slot. It owns `ReplenishmentPolicy` and `ReplenishmentRequest` and nothing physical: stock stays in inventory-storage, the map in facility-layout, the forward assignment map in slotting-optimization, the floor work in wes-work-planning / fulfillment-execution. Study project: not production software.

Design source of truth: `docs/adr/` (ADR 0001 context, 0002 aggregates and detection policy, 0003 local copies and consumed contracts, 0004 CloudEvents catalogue, 0005 evaluation semantics and persistence).

## Hard rules (each one is fitness-tested or cost an incident)

1. **Hexagonal dependency rule.** `internal/domain` depends on nothing internal but domain; `internal/application` only on domain and application; adapters never depend on each other; only `cmd/` wires layers. `make arch-test` enforces it.
2. **CloudEvents 1.0 is MANDATORY** on every Kafka message produced or consumed. Build/decode ONLY through `internal/adapters/kafka/cloudevents/`; `type` is `com.warehouse.wms.pick-face-replenishment.<entity>.<Event>` (`replenishment.ReplenishmentRequested|Completed|Cancelled`, analytics-only `policy.ReplenishmentPolicyDeclared`). Payloads: `apis/asyncapi.yaml`; catalogue: ADR 0004.
3. **Events leave through the transactional outbox only**, in the same unit of work as the aggregate save. The relay is built with `WithMaxAttempts` (`OUTBOX_MAX_ATTEMPTS`, default 20, 0 = unlimited): `TestOutboxRelaysPassAttemptCap` fails otherwise. At most ONE open request per (site, forward bin, SKU) (partial unique index).
4. **Levels are read models, not aggregate state.** Bin levels (`BinStockLevelChanged`, absolute values, version-guarded), the forward assignment map (`SlotPlanApproved`, replaced wholesale per site, a stale plan is ignored) and locations/zones (`facility-layout`) are event-fed local copies. The detector is pure and deterministic (policy `MIN_MAX`, source policy `fullest-reserve-bin-v1`, both behind ports): same input, same output.
5. **Never create floor work or write the ledger.** A request is published; wes-work-planning plans the `REPLENISH` work, fulfillment-execution runs it, inventory-storage applies `RelocateStock` (reason `REPLENISHMENT`) and its `StockRelocated` (`source_ref` = request id) closes the request here. That `REPLENISH` chain is built in every service (wes-work-planning, fulfillment-execution, inventory-storage, process-path-management) and released to `main` (images and charts published), but not yet deployed or seeded.
6. **No live lookups of sibling contexts** (REST or MCP). Reserve zones come from `RESERVE_ZONE_CODES` (default `RSV`).
7. **Runtime: real Postgres and real Kafka only** (ADR 0006). There is no in-memory mode: the binary refuses to boot without `DATABASE_URL`, `KAFKA_BROKERS` and the three `*_CONSUMER_GROUP` variables (the error names the variable). All three consumers and the outbox relay always run; `INVENTORY_MODE`, `SLOTTING_MODE`, `LAYOUT_MODE` default to `kafka` and `permissive` is rejected at boot. `/readyz` checks the drain gate, Postgres and that a Kafka broker answers a metadata request; it never checks topics (the broker auto-creates them on first write; ADR 0006 Amendment 1). The memory adapters are test doubles; `TestRuntimeBinaryHasNoInMemoryAdapters` forbids `cmd/**` from importing them. Env table: `.claude/rules/integration-events.md`.
8. **`version` guards every write** (409 on a lost update); POSTs require an `Idempotency-Key`; 5xx outcomes are never stored.
9. **No auth layer on REST or MCP** (fleet-wide revert 2026-09-11): `TestNoAuthMiddlewareReintroduced`. MCP is additive and not built yet (a separate binary with read tools only, when it comes).
10. **Planned, not built:** demand-cover policy (days of cover from demand), aisle-aware source choice, MCP.
11. **An ADR comes first** for a new policy version, a cross-context contract or any auth. ADRs live in `docs/adr/`.
12. **Packaging and layout.** `Dockerfile` and `charts/pick-face-replenishment/` ship the real runtime (ADR 0006; the chart refuses to render without the database, brokers and consumer groups). No Go file sits at the repo root: godog features, steps and `TestFeatures` live in `features/` (`TestNoGoFilesAtRepoRoot`).

## Commands

```bash
make check-fast   # fmt-check + vet + arch-test + tests of changed packages: run before saying "done"
make check        # fmt-check vet build lint test
make check-all    # check + coverage (90% gate on domain+application) + arch-test + bdd
make integration  # real Postgres/Kafka via testcontainers (needs Docker)
make vuln         # govulncheck: run it after any go get / go mod tidy
make mutation     # gremlins on the domain packages (CI job mutation-fast, blocking)
make guide-lint   # these guides: skills load, references resolve, context budget
```

## Where things are written down (read before editing the matching area)

- `.claude/rules/domain-model.md`, `rest-api.md`, `integration-events.md`; `.claude/rules/fleet/*.md` are copied from the harness template, never edit them here.
- `docs/adr/` decisions; `apis/openapi.yaml`, `apis/asyncapi.yaml` contracts; `HARNESS.md` sensors.

harness-template: v4.
harness-template: v3

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `internal/domain/**`, `internal/application/**`, `features/**` ... | `.claude/rules/domain-model.md` |
| `internal/adapters/**/kafka/**`, `internal/adapters/outbound/events/**`, `apis/asyncapi*` | `.claude/rules/integration-events.md` |
| `internal/adapters/inbound/http/**`, `apis/openapi*.yaml`, `apis/openapi/**` | `.claude/rules/rest-api.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->

A2A: `cmd/a2a` (port 8093) is this context's read-only Agent2Agent agent, skills `pick_face_status` (read), `replenishment_lookup` (read), decided in `docs/adr/0010-a2a-inbound-adapter.md`; delegation only (facts stay CloudEvents on Kafka), unauthenticated like REST and MCP; rules in `.claude/rules/fleet/a2a.md`.
