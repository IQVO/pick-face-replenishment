# pick-face-replenishment

Pick-Face Replenishment: WMS-tier Supporting bounded context of the warehouse-systems fleet. From min/max policies and event-fed bin levels it raises replenishment requests that move stock from a reserve bin to the forward pick slot, so pick faces do not run dry. Hexagonal Go, Postgres, REST, Kafka (CloudEvents 1.0).

- Design and pinned contracts: `docs/adr/` and the fleet plan `.hermes/plans/2026-10-17_005500-pick-face-replenishment-and-task-interleaving.md`.
- Scaffolded from `warehouse-harness-template` (v3). Guides: `.claude/rules/`, sensors: `HARNESS.md`.
- Commands: `make check-fast`, `make check-all`, `make integration`.
