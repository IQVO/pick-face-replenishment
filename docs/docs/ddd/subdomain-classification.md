---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
sidebar_position: 2
---

# Subdomain classification

**`pick-face-replenishment` is a Supporting subdomain of the WMS tier** (CloudEvents subdomain segment `wms`). ADR 0001
decides it; the warehouse-docs fleet table (`docs/strategic-design/subdomain-classification.md` on `main`, section
"Scaffolded, not yet built") lists the same verdict: Supporting, `wms`, business model "cost reduction (no empty forward
pick face, fewer short picks)", evolution "custom-built (genesis)".

## Reasoning

- **Not Generic.** Min/max and source-bin rules differ per retailer: how low a pick face may fall, how much to move in
  which multiple, and which reserve bin to draw from are warehouse policy, not a commodity.
- **Not Core.** The min/max rule is a replaceable policy object (v1 `MIN_MAX` per site and SKU, with the source choice
  behind the `SourceBinPolicy` port), and the competitive core of the fleet stays the WES conductor
  (`wes-work-planning`) and the task lifecycle (`fulfillment-execution`).
- **Evidence in code.** Two small aggregates and a pure, deterministic detector; the request lifecycle has three states
  and five invariants. It generates replenishment requests only: it never creates floor work and never writes the stock
  ledger.

See the [core domain chart](/docs/ddd/core-domain-chart) for the position on the differentiation/complexity plane.

## Neighbour classifications

Copied from the fleet table in warehouse-docs (`origin/main`), not from memory:

| Context | Classification | CloudEvents subdomain | Relation |
| --- | --- | --- | --- |
| `inventory-storage` | Core | `wms` | upstream: `BinStockLevelChanged`, `StockRelocated` (reason `REPLENISHMENT` included) |
| `slotting-optimization` | Supporting | `wms` | upstream: `SlotPlanApproved` |
| `facility-layout` | Generic | `wms` | upstream: zones and location slots |
| `wes-work-planning` | Core | `wes` | downstream, built and opt-in: plans one `REPLENISH` work unit per `ReplenishmentRequested` (its ADR 0042; consumer starts only with `REPLENISHMENT_MODE=kafka`) and releases it with a `replenishment` object |
| `fulfillment-execution` | Core | `wes` | downstream, built: runs the `REPLENISH` task from `WorkReleased.data.replenishment` (its ADR 0050) |
| `warehouse-ops-agent` | Supporting | `wes` | planned REST reader (not built: warehouse-ops-agent `origin/develop` has no reference to this context) |

The tier (`wms`, `wes`) groups contexts by where they sit in the platform; it is not the Core/Supporting/Generic
classification. `task-interleaving`, created the same day, is Supporting in the `wes` tier and has no edge to this
service.
