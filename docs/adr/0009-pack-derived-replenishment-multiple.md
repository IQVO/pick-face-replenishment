# ADR 0009: Pack-derived replenishment multiple

## Status

Proposed (2026-10-24). Additive: a policy declared the way it is today (source `DECLARED`)
behaves exactly as before. Part of the mastercase / GTIN adoption
(`.hermes/plans/mastercase-briefs/contracts.md` sections 0, 1, 8 of the warehouse-systems
workspace).

## Context

A policy's `multiple` is hand-declared (ADR 0002). Once product-master publishes the pack
hierarchy of a SKU (`ProductPackLevelDeclared` / `ProductPackLevelRetired`), the case size is
known data, and a hand-typed multiple can drift from the real case.

## Decision

- A policy has a source: `DECLARED` (default, today's behaviour) or `PACK_DERIVED`.
- A `PACK_DERIVED` policy is declared without a multiple; its `multiple` is the `units_per_pack`
  of the SKU's active CASE level, read from a local `PackLevelView`. If the SKU has no active
  CASE level the declaration is a 422 `no-active-case-level`.
- The view is fed by a consumer of product-master's pack-level events (stable group
  `PACK_LEVEL_CONSUMER_GROUP`, inbox for idempotency). There is no synchronous call to
  product-master.
- When a CASE level is superseded, a `PACK_DERIVED` policy re-derives its multiple (new
  version, `ReplenishmentPolicyDeclared` with `multiple_source=PACK_DERIVED`). When a CASE level
  is retired and none remains, the policy is flagged `stale` and keeps its last multiple.
- `minQty`, `maxQty` and request quantities stay in eaches; rounding down to the multiple is
  unchanged, so a derived multiple yields whole cases.
- `ReplenishmentPolicyDeclared` gains the optional `multiple_source`; `GET /policies` returns
  `source` and `stale`.

## Consequences

- The chart renders `PACK_LEVEL_CONSUMER_GROUP` (required); warehouse-infra needs no new topic.
- `DECLARED` policies never change when pack levels change.
