---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
sidebar_position: 9
---

# Ubiquitous language

The terms of the context and the code identifier that implements each one.

| Term | Meaning | Code |
| --- | --- | --- |
| Pick face | A forward bin assigned to one SKU by the site's latest approved slot plan, from which pickers pick | `repository.Assignment`, `usecases.PickFaceView` |
| Forward bin | The location code of a pick face | `Request.ForwardBin()`, table `forward_assignments.bin` |
| Reserve bin | A known, not decommissioned Storage location of the same site whose zone code is in `RESERVE_ZONE_CODES` | `PickFaceReadModel.ReserveBins` |
| Forward zone | A zone whose code is in `FORWARD_ZONE_CODES`; a face in a known zone outside it is not a pick face | `usecases.ZoneConfig.Forward` |
| Policy | The explicit `MIN_MAX` rule of one (site, SKU): `minQty`, `maxQty`, `multiple`, `version` | `policy.Policy` |
| Min, max | Reorder trigger and fill-up ceiling; `0 <= min < max` | `Policy.MinQty`, `Policy.MaxQty` |
| Multiple | The unit a quantity is rounded down to, so a request never overfills the face; default 1 | `Policy.Multiple`, `Policy.RoundDown` |
| Available | `on_hand - reserved` of the forward bin | `detection.Outcome.Available` |
| Open request | The one `Requested` request of a pick face; its quantity counts as inbound stock | `detection.OpenRequest`, `RequestRepo.OpenFor` |
| Projected level | `available + the open request's quantity` (0 without one) | `detection.Outcome.Projected` |
| Usable stock | `on_hand - reserved` of a reserve bin: what it can give up now | `detection.ReserveBin.Usable` |
| Replenishment request | One decision to move `quantity` units of one SKU from one reserve bin to one forward bin, and how it ended | `replenishment.Request` |
| Request id | `repl-` plus a random UUID, 41 characters; the request's subject and Kafka key | `memory.RandomIDs.NewRequestID` |
| Requested, Completed, Cancelled | The states; the last two are final | `replenishment.State` |
| Priority | `URGENT` when nothing was available at detection (`available <= 0`), else `NORMAL` | `replenishment.Priority` |
| Cancel reason | `OPERATOR`, `ASSIGNMENT_CHANGED` (a newer slot plan dropped the assignment), `POLICY_REMOVED` | `replenishment.CancelReason` |
| Moved quantity | The units a relocation actually moved; can be less than requested | `Request.MovedQuantity` |
| Verdict | The detector's result: `Raise`, `Healthy`, `AlreadyOpen`, `NoPolicy`, `NoSource` | `detection.Kind` |
| Pick-face status | The REST view of a verdict: `BELOW_MIN`, `OK`, `NO_SOURCE`, `NO_POLICY` | `usecases.StatusBelowMin` and siblings |
| Source bin policy | The versioned rule that picks the reserve bin; v1 is `fullest-reserve-bin-v1` | `detection.SourceBinPolicy`, `detection.FullestReserve` |
| Sweep | `POST /sites/{siteId}/evaluations`: detect over every pick face of a site; converges | `usecases.EvaluateSite` |
| Local copy | An event-fed table this context reads instead of calling a sibling | `bin_stock_levels`, `forward_assignments`, `zones`, `locations` |
| Forward map | The site's full SKU-to-slot assignment from the latest approved slot plan, with the plan's `approved_at` stamp | `forward_assignments`, `forward_assignment_maps` |
| Stale map | A `SlotPlanApproved` whose `approved_at` is not after the recorded one; ignored | `ApplyForwardAssignmentMap.isStale` |
| Replenishment relocation | A `StockRelocated` with reason `REPLENISHMENT` and `source_ref` = the request id; it completes the request | `replenishment.RelocationReason`, `Request.Complete` |
| Outcome | What a consumer did with a message: `applied`, `duplicate`, `stale`, `ignored` | `usecases.Outcome` |
| Claim | The `(consumer, event id)` row that makes a consumer effect idempotent | `processed_events`, `ports.ProcessedEvents` |
| Outbox | The transactional table of already-encoded CloudEvents waiting for the relay | `outbox_events`, `outbox.Message` |
| Idempotency key | The caller's `Idempotency-Key`, whose first answer is stored and replayed | `idempotency_keys`, `inboundhttp.Idempotency` |
| Dead letter | A message moved to `<topic>.dlq` with `x-dlq-*` headers | `inboundkafka.DLQSuffix` |
