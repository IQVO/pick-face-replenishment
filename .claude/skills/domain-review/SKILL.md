---
name: domain-review
description: Ubiquitous-language drift review of a pick-face-replenishment change against .claude/rules/domain-model.md: renamed or invented terms, aggregate-invariant leaks. Invoke explicitly: /domain-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---

Perform a ubiquitous-language drift review of the current changes (or
`$ARGUMENTS` if given), comparing new and changed code against
`.claude/rules/domain-model.md` and `docs/adr/0002-aggregates-and-detection-policy.md`.

Drift is the quiet failure mode DDD is meant to prevent: code that works but
silently renames, reshapes or duplicates a concept the domain-model rule
already named, so readers can no longer map code to domain conversation.

## What to check, in priority order

1. **A new type, field or method that duplicates an existing concept under a
   different name.** The domain names `Available` (`OnHand - Reserved`),
   `Projected` (`Available` plus the open request's quantity), `Usable`
   (a reserve bin's `OnHand - Reserved`), `Priority` (`NORMAL`, `URGENT`),
   `Policy` with `MinQty`, `MaxQty`, `Multiple`, and `Request` with its
   `State`. A second calculation of the same thing as `FreeStock`,
   `StockGap`, `Level` or `Threshold` is drift even if the math is right.
   Flag it and point at the existing name.
2. **Implementation vocabulary in domain code.** `internal/domain/` reads like
   the ubiquitous language: `Plan`, `Complete`, `Cancel`, `RoundDown`,
   `WithBounds`, `Detect`, `Choose`. A method such as `UpdateRow`,
   `SetStatus` or `PatchState` is drift. Fitness tests do not catch naming.
3. **Synonyms for the fixed terms.** Say "pick face" and "forward bin", never
   "pick slot" or "picking location" for the same thing; "reserve bin", never
   "bulk bin" or "overflow"; "request" for `ReplenishmentRequest`, never
   "task" or "work" (tasks belong to fulfillment-execution, work to
   wes-work-planning). "Replenishment" is not "relocation": a relocation is
   inventory-storage's `StockRelocated`, which closes a request.
4. **An invariant in code that the domain-model rule or ADR 0002 does not
   mention, or the reverse.** A new validation or transition guard needs the
   rule file (and, if it changes behaviour, an ADR) updated in the SAME PR.
   An undocumented invariant looks dead to the next person.
5. **A closed set implemented open, or the reverse.** `State`
   (`Requested`, `Completed`, `Cancelled`), `Priority` and `CancelReason`
   (`OPERATOR`, `ASSIGNMENT_CHANGED`, `POLICY_REMOVED`) are deliberately
   closed, each with a `Valid` method, and `Restore` rejects an unknown value.
   `StockRelocated.reason` is the producer's open set: this service acts only
   on `REPLENISHMENT` and ignores the rest. A new categorical field must
   choose deliberately.
6. **Local versus cross-aggregate rules.** One open request per (site, forward
   bin, SKU) spans requests, so the use case enforces it with a partial unique
   index, not the aggregate. Levels, forward assignments and locations are
   event-fed read models, not aggregate state, and nothing in the domain calls
   another context. A change that makes a domain check call out to another
   service is a real regression.
7. **Policy ports.** A new way to choose the source bin is a new
   `detection.SourceBinPolicy` implementation with its own versioned name and
   ADR, not a branch inside `FullestReserve`. A demand-cover policy is a
   second policy kind, not a flag on MIN_MAX.
8. **New terminology without a domain-model update.** A genuinely new concept
   is fine, but `.claude/rules/domain-model.md` needs the entry in the SAME PR,
   or prose and code fork.

## Output format

For each finding: the term or concept involved, where it appears in the
domain-model rule (or "not yet documented"), where the drift appears in code,
and a one-sentence recommendation (rename, document, or confirm it is an
intentional new concept). If the changes introduce no new concepts and use
existing vocabulary correctly, say so plainly.

This command never modifies files. It is advisory input for the author, not a
blocking gate.
