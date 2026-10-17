---
name: architecture-review
description: Bounded-context boundary and ADR-compliance review of a pick-face-replenishment change (expensive, post-integration): hexagonal direction, cross-context coupling, contradicted ADRs. Invoke explicitly: /architecture-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---

Perform a bounded-context boundary and ADR-compliance review of the
current changes (or `$ARGUMENTS` if given, e.g. a branch/PR diff range).

This is EXPENSIVE relative to `/code-review`: it reasons about cross-context
implications, not just this diff's local correctness. Use it before merging a
PR that touches architecture (a new adapter, a new consumed topic, a new
published type), not on every small commit.

## What to check, in priority order

1. **Hexagonal dependency direction.** Domain (`internal/domain/...`) imports
   nothing internal but domain; the application layer depends on domain and
   ports; adapters never import each other; only `cmd/` wires. Run
   `go test ./internal/architecture/... -v` first (`TestHexagonalArchitecture`
   in `internal/architecture/architecture_test.go`). If it is red, report that
   and stop; do not hand-review what a fitness test already caught.
2. **No live sibling call (ADR 0001 exclusions, ADR 0003).** This service has
   no outbound HTTP or MCP client; stock levels, forward assignments and
   locations arrive as events into local copies. Flag any new outbound call to
   inventory-storage, slotting-optimization, facility-layout,
   wes-work-planning or any other context: it is a new architectural decision
   and needs its own ADR in `docs/adr/`.
3. **Ownership boundary (ADR 0001).** pick-face-replenishment requests a move;
   it never writes the stock ledger and never creates floor tasks. Flag code
   that records stock, plans a task, or sets task state.
4. **Domain stays pure (ADR 0002).** `internal/domain/replenishment`,
   `policy`, `detection` and `shared` import no CloudEvents, Kafka, HTTP or SQL
   code and read no clock; time arrives as a parameter. `detection.Detect`
   stays a pure function whose result does not depend on slice order.
5. **MCP additive-boundary rule.** A change under the inbound MCP adapter
   (built in a later wave) must depend only on application and domain, and
   nothing else may depend on it. `TestMCPAdapterDependencyRule` in
   `internal/architecture/fitness_test.go` enforces it; confirm it is green.
6. **Consumed contracts match the producer (ADR 0003, ADR 0004).** Type strings
   in `apis/asyncapi.yaml` are byte-identical to the producer's catalogue;
   `TestEventCatalogueMatchesContract` ties asyncapi to ADR 0004. A change to a
   consumed or published type needs the asyncapi, ADR 0004 and
   `.claude/rules/integration-events.md` updated together. Breaking payload
   change means a new `.v2` type, never a mutation.
7. **Kafka consumer-group pattern.** Group ids come from env
   (`INVENTORY_CONSUMER_GROUP`, `SLOTTING_CONSUMER_GROUP`,
   `LAYOUT_CONSUMER_GROUP`), never an inline literal
   (`TestKafkaConsumerGroupNeverHardcodedInline`). These consumers feed durable
   copies and resume from the committed offset, so a stable group is correct; a
   consumer that replays from FirstOffset into memory needs a per-process-unique
   group (see the Kafka section of `HARNESS.md`). Flag a pattern that does not
   match the consumer's replay behaviour: it is a correctness bug.
8. **Events leave through the outbox only.** No direct `Writer.WriteMessages`
   from a use case; the relay sets `Message.Topic` per message
   (`TestKafkaWriterTopicNotSetTwice`) and caps attempts
   (`TestOutboxRelaysPassAttemptCap`). See
   `.claude/rules/fleet/idempotency-and-outbox.md`.
9. **A new cross-repo contract with no ADR.** A new REST call, topic
   subscription or planned edge needs an ADR here and, by the fleet convention,
   a companion ADR in the OTHER repo (see `.claude/skills/how-to-write-an-adr/SKILL.md`).
10. **Auth reintroduction.** Fleet-wide auth was reverted on 2026-09-11. Check
    not only for a Bearer literal but whether the change's intent requires
    re-litigating that decision; that needs its own ADR
    (`TestNoAuthMiddlewareReintroduced`).

## Output format

State clearly: PASS (no architectural concerns), CONCERNS (list them, each
tied to the specific rule or ADR it would violate), or NEEDS-ADR (the change is
sound but undocumented; name what the ADR should cover). Cite the file, rule or
ADR for every finding; a finding with no citation is not actionable.

This is advisory. It never blocks a merge on its own and never modifies files.
If a finding conflicts with a decision stated in an accepted ADR of this repo,
defer to the ADR and say so.
