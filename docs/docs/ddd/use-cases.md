---
id: use-cases
title: Use cases
sidebar_label: Use cases
sidebar_position: 1
---

# Use cases

Every use case of `internal/application/usecases`: what triggers it, its inputs, the invariants it checks and the events
it raises. A write use case runs in one `UnitOfWork`; the facts it raises are handed to the outbox in that same
transaction. There is **no scheduler and no MCP trigger**: a use case is started by REST or by a Kafka message.

| # | Use case | Trigger | Writes | Events raised |
| --- | --- | --- | --- | --- |
| 1 | `DeclarePolicy` | REST `PUT /policies/{siteId}/{sku}` | `policies` | `ReplenishmentPolicyDeclared` (analytics) |
| 2 | `RemovePolicy` | REST `DELETE /policies/{siteId}/{sku}` | `policies`, `replenishment_requests` | `ReplenishmentCancelled` per open request |
| 3 | `GetPolicy` | REST `GET /policies/{siteId}/{sku}` | none | none |
| 4 | `ListPolicies` | REST `GET /policies` | none | none |
| 5 | `ListPickFaces` | REST `GET /pick-faces` | none | none |
| 6 | `RaiseManualRequest` | REST `POST /replenishments` | `replenishment_requests` | `ReplenishmentRequested` |
| 7 | `CancelRequest` | REST `POST /replenishments/{requestId}/cancellations` | `replenishment_requests` | `ReplenishmentCancelled` (`OPERATOR`) |
| 8 | `GetRequest` | REST `GET /replenishments/{requestId}` | none | none |
| 9 | `ListRequests` | REST `GET /replenishments` | none | none |
| 10 | `EvaluateSite` | REST `POST /sites/{siteId}/evaluations` | `replenishment_requests` | `ReplenishmentRequested` per request raised |
| 11 | `ApplyBinLevel` | Kafka `BinStockLevelChanged` | `bin_stock_levels`, `processed_events`, maybe a request | `ReplenishmentRequested` when the face is now low |
| 12 | `ApplyRelocation` | Kafka `StockRelocated` | `replenishment_requests`, `processed_events` | `ReplenishmentCompleted` |
| 13 | `ApplyForwardAssignmentMap` | Kafka `SlotPlanApproved` | `forward_assignments`, `forward_assignment_maps`, `processed_events`, requests | `ReplenishmentCancelled` (`ASSIGNMENT_CHANGED`), `ReplenishmentRequested` for new faces |
| 14 | `ApplyZone` | Kafka `ZoneRegistered` | `zones`, `processed_events` | none |
| 15 | `ApplyLocationRegistered` | Kafka `LocationSlotRegistered` | `locations`, `processed_events` | none |
| 16 | `ApplyLocationDecommissioned` | Kafka `LocationSlotDecommissioned` | `locations`, `processed_events` | none |

Shared building blocks: the `Evaluator` (assess a face read-only, or evaluate it and raise a request) is used by 5, 6, 10,
11 and 13, so they cannot disagree; `Intake` is the claim-and-apply skeleton of 11 to 16; the `canceller` closes open
requests for 2, 7 and 13.

## Detection rules (used by 5, 6, 10, 11, 13)

With `available = on_hand - reserved` of the forward bin and `projected = available + the open request's quantity`:

1. no policy: `NoPolicy`; a request already open: `AlreadyOpen`;
2. `projected > minQty`: `Healthy`; otherwise raise;
3. `need = maxQty - projected`, rounded down to a multiple of the policy's `multiple`;
4. the source is the reserve bin of the same site, other than the forward bin, with the largest usable stock
   (`on_hand - reserved`, at least 1), ties to the smallest bin code (`fullest-reserve-bin-v1`); the quantity is capped at
   its usable stock and rounded down again;
5. no source, or a quantity below 1: `NoSource`; else `Raise` with `URGENT` when `available <= 0`, otherwise `NORMAL`.

A **pick face** is a forward assignment whose location is not decommissioned and not known to sit outside
`FORWARD_ZONE_CODES`; a location whose zone has not arrived counts as a pick face. A bin never seen reads as level 0.

## REST use cases

### 1. DeclarePolicy

- Input: `siteId`, `sku` (path), body `minQty`, `maxQty` (required), `multiple` (default 1), optional `If-Match`.
- Invariants: site and SKU not blank and at most 64 characters; `0 <= minQty < maxQty`; `multiple >= 1`; without
  `If-Match` the policy must not exist (`policy-already-exists`); with it, the stored version must equal it
  (`concurrent-modification`, also when the policy does not exist). A change is the same policy at `version + 1`.
- Events: `ReplenishmentPolicyDeclared` on the analytics topic only, in the same transaction.

### 2. RemovePolicy

- Input: `siteId`, `sku`, `If-Match` (required).
- Invariants: the policy exists (`policy-not-found`) and its version equals `If-Match`.
- In one transaction it deletes the policy and cancels every open request of that site and SKU with reason
  `POLICY_REMOVED`. No policy event is raised; the cancellations are the visible effect.

### 3 and 4. GetPolicy, ListPolicies

Reads. `ListPolicies` pages by (site, SKU) with an opaque cursor and filters `siteId` and `sku`; `limit` 1 to 500.

### 5. ListPickFaces

- Input: `siteId` (required), optional `status`, `limit`, `cursor`.
- Runs the detector read-only over every forward face of the site, in forward-bin order, and keeps those whose status
  matches. Status: `NO_POLICY` and `NO_SOURCE` come from the detector, otherwise `BELOW_MIN` when `projected <= minQty`
  (a `Raise`, or an `AlreadyOpen` face still low) and `OK` above it.

### 6. RaiseManualRequest

- Input: `siteId`, `sku`, optional `quantity`.
- Invariants, in order: a forward bin must be assigned to the SKU (`no-forward-assignment`); a policy must exist
  (`no-policy`); no request may be open for the face (`replenishment-already-open`, naming the open request).
  Without `quantity` the detector sizes it (`no-source-stock` when low with no source, `nothing-to-replenish` when above
  the minimum). With `quantity`: at least 1, `projected + quantity <= maxQty` (`quantity-exceeds-max`), at most the
  chosen source's usable stock (`no-source-stock`); priority is `URGENT` when nothing is available.
- Events: `ReplenishmentRequested`.

### 7. CancelRequest

- Input: `requestId` (`repl-` prefix, 6 to 64 characters, no whitespace, control characters or `/`).
- Invariants: the request exists and is `Requested` (`replenishment-not-open` otherwise; `Completed` and `Cancelled` are
  final). The save is guarded by the loaded version.
- Events: `ReplenishmentCancelled` with reason `OPERATOR`.

### 8 and 9. GetRequest, ListRequests

Reads. `ListRequests` is newest first (`requested_at`, then id, descending) with filters `siteId`, `sku`, `state`.

### 10. EvaluateSite

- Input: `siteId`.
- For every forward pick face of the site, one unit of work each: assess and, when the verdict is `Raise`, plan and save a
  request. The sweep converges: a face with an open request is `alreadyOpen`, so a second run raises nothing. A failure
  leaves the faces before it done; a rerun finishes the rest.
- Output: counts `evaluated`, `raised`, `healthy`, `alreadyOpen`, `noPolicy`, `noSource` and the new `requestIds` in forward-bin
  order. Events: `ReplenishmentRequested` per request.

## Kafka use cases

All of 11 to 16 go through `Intake`: the CloudEvents `id` is claimed in `processed_events` in the same transaction as the
effect (outcome `duplicate` when already claimed), and a payload that can never be applied is `ErrInvalidEvent`, which the
consumer dead-letters. Outcomes are `applied`, `duplicate`, `stale` or `ignored`.

### 11. ApplyBinLevel

- Input: `site_code`, `bin`, `sku`, `on_hand`, `reserved`, `version` (levels are absolute).
- Invariants: ids not blank, levels not negative, `version >= 1`; the level replaces the stored one only when its version
  is greater (`stale` otherwise).
- Then evaluates the pick face the site's forward map gives the SKU, so a face falling below its minimum, or reserve stock
  arriving for a face that had no source, can raise a request.

### 12. ApplyRelocation

- Input: `relocation_id`, `site_code`, `sku`, `from_bin`, `to_bin`, `quantity`, `reason`, `source_ref`.
- Acts only when `reason` is `REPLENISHMENT` and `source_ref` is a well-formed request id; anything else is `ignored`.
- Invariants (aggregate `Complete`): the request exists, is open, belongs to the same site, and the SKU, from bin
  (= source bin) and to bin (= forward bin) match; the moved quantity (at least 1, possibly less than requested) is
  recorded. Mismatches are `ignored`, not errors; quantity below 1 on a REPLENISHMENT relocation is invalid.
- Events: `ReplenishmentCompleted` with the quantity actually moved.

### 13. ApplyForwardAssignmentMap

- Input: `plan_id`, `site_id`, `approved_at`, `assignments[{sku, slot}]` (the full map).
- Invariants: required fields present, no blank sku or slot, no SKU assigned twice; the stale-map guard ignores a map whose
  `approved_at` is not after the one recorded for the site (`forward_assignment_maps`).
- Replaces the site's assignments wholesale, cancels every open request whose forward bin no longer holds its SKU
  (`ASSIGNMENT_CHANGED`), then evaluates the faces that are new or moved.

### 14 to 16. ApplyZone, ApplyLocationRegistered, ApplyLocationDecommissioned

Upsert zone codes and locations into the layout copy; decommissioning is irreversible and is recorded even for a location
never seen. Required: `zoneId`, `siteCode`, `zoneCode` for a zone; `locationCode` and `zoneId` for a registration;
`locationCode` for a decommission.

Source: `internal/application/usecases/*.go`, `internal/adapters/inbound/http/server.go`,
`internal/adapters/inbound/kafka/*_consumer.go`.
