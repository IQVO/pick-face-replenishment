---
id: introduction
title: Introduction
sidebar_label: Introduction
sidebar_position: 1
---

# Pick-Face Replenishment

`pick-face-replenishment` is the bounded context that keeps every forward pick face stocked. It holds an explicit
**min/max policy** per site and SKU, watches the stock level of each forward bin through event-fed local copies, and when
the projected level reaches the minimum it raises a **replenishment request**: move a quantity of the SKU from the
fullest reserve bin to the forward bin, never more than the policy maximum. It closes the request when
inventory-storage records the matching relocation.

| | |
| --- | --- |
| Tier | WMS (CloudEvents type segment `wms`, source `/warehouse/pick-face-replenishment`) |
| Subdomain class | Supporting ([reasoning](/docs/ddd/subdomain-classification)) |
| Binary | one: `cmd/api` (REST, outbox relay and three Kafka consumers in one process, all always on) |
| Datastore | PostgreSQL (required: there is no in-memory mode, ADR 0006) |
| Messaging | Kafka, CloudEvents 1.0 structured mode, transactional outbox |
| Auth | none (fleet decision 2026-09-11) |
| Repository | `IQVO/pick-face-replenishment`, site `https://iqvo.github.io/pick-face-replenishment/` |

## What it owns

- **ReplenishmentPolicy**: the `MIN_MAX` rule of one (site, SKU): `minQty`, `maxQty`, `multiple`, a `version` for
  optimistic concurrency. Declared and removed through REST.
- **ReplenishmentRequest**: one decision to move stock from one reserve bin to one forward bin, in the states
  `Requested`, `Completed` and `Cancelled`. At most one is open per (site, forward bin, SKU).
- **The detector**: a pure function that turns a pick face, its policy, its open request and the candidate reserve bins
  into one of five verdicts (`Raise`, `Healthy`, `AlreadyOpen`, `NoPolicy`, `NoSource`). The source bin policy is
  `fullest-reserve-bin-v1`.

It does **not** own stock levels, slot assignments or locations. Those arrive as events and live in local copies
(`bin_stock_levels`, `forward_assignments`, `zones`, `locations`); the service never calls a sibling context
([Integration](/docs/ecosystem/integration)). It creates no floor work and never writes the stock ledger.

## Main capabilities

1. **Declare policies** with `PUT /policies/{siteId}/{sku}` (`If-Match` versioning) and publish the analytics fact
   `ReplenishmentPolicyDeclared`.
2. **See every pick face** with its status (`BELOW_MIN`, `OK`, `NO_SOURCE`, `NO_POLICY`) through `GET /pick-faces`.
3. **Raise requests** three ways: automatically when a bin level or a new forward map is applied, with the sweep
   `POST /sites/{siteId}/evaluate`, or by hand with `POST /replenishments`.
4. **Complete requests** from inventory-storage's `StockRelocated` (reason `REPLENISHMENT`, `source_ref` = the request
   id), recording the units actually moved.
5. **Cancel requests** on the operator's word, when a newer approved slot plan drops the assignment, or when the policy
   is removed.
6. **Publish** `ReplenishmentRequested`, `ReplenishmentCompleted` and `ReplenishmentCancelled` on
   `warehouse.pick-face-replenishment.events` (and again on the analytics topic) through a transactional outbox.

## Status of the execution chain

Replenishment is meant to run through the relocation chain pick-face-replenishment, wes-work-planning (`REPLENISH`
work), fulfillment-execution, inventory-storage (`RelocateStock`) and back as `StockRelocated`. Checked on each sibling's
`origin/develop`: the inputs from slotting-optimization and facility-layout are live, inventory-storage publishes
`BinStockLevelChanged` (its ADR 0040) and a `StockRelocated` with reason `REPLENISHMENT` for a completed `REPLENISH`
task (its ADR 0041), and fulfillment-execution creates, announces and completes `REPLENISH` tasks (its ADR 0050). Still
missing is the first hop: wes-work-planning consumes nothing from this service's topic and emits no `replenishment`
object on `WorkReleased`, so today a request can be raised and cancelled but no task is created for it and it does not
complete. The per-edge table is on [Integration](/docs/ecosystem/integration).

## Where to go next

| I want to | Page |
| --- | --- |
| understand the layout and the moving parts | [Architecture](/docs/overview/architecture) |
| run it and make the first calls | [Quickstart](/docs/overview/quickstart) |
| configure it | [Configuration](/docs/operations/configuration) |
| deploy, migrate and operate it | [Runbook](/docs/operations/runbook) |
| watch it | [Observability](/docs/operations/observability) |
| debug it | [Troubleshooting](/docs/operations/troubleshooting) |
| test it | [Testing](/docs/development/testing) |
| know what it talks to | [Integration](/docs/ecosystem/integration) |
| read the behaviour use case by use case | [Use cases](/docs/ddd/use-cases) |
| read the REST and event contracts | [API reference](/docs/api-reference), [Event catalogue](/docs/api-reference/events) |
| see why it was built this way | [ADR index](/docs/adr), [DDD artifacts](/docs/ddd/ddd-artifacts) |
