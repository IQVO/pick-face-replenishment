---
name: how-to-write-an-adr
description: Write an Architecture Decision Record for pick-face-replenishment in this repo's numbering and format, including companion ADRs for cross-repo changes. Use when a design decision should be recorded or a change contradicts an existing ADR.
---

# How to write an ADR

Use when a change is architecturally significant: a new bounded-context
integration, a reversal of a prior decision, a cross-repo contract change, a
new policy kind, or anything a future reader would otherwise reverse-engineer
from the diff. A bug fix or a routine feature inside an already-decided
architecture does not need one.

## Numbering and location

`docs/adr/NNNN-kebab-case-title.md`, four digits, sequential. List the folder
first (`git ls-tree --name-only origin/develop -- docs/adr/`) and take the next
integer; never reuse or guess. Today the set is `0001` (bounded context),
`0002` (aggregates, invariants, detection policy ports), `0003` (local copies
and consumed contracts) and `0004` (CloudEvents envelope and type catalogue).
Add a row to the table in `docs/adr/index.md` in the same PR; the link form is
`/docs/adr/NNNN-kebab-case-title` (no `.md`) and the *Status* column copies the
ADR's own `## Status`.

For a cross-repo reference, list the sibling's `docs/adr/` (or its docs folder)
on its `origin/develop` before citing a number; do not cite from memory.

## Format

The ADR files carry no frontmatter (only `docs/adr/index.md` does). Copy the
shape of `docs/adr/0002-aggregates-and-detection-policy.md`:

```markdown
# ADR NNNN: a short statement of the decision

## Status

Accepted (YYYY-MM-DD).

## Context

The forces at play, in the past tense, as if explaining to someone who was not
there. State the alternatives seriously considered, not only the one chosen.

## Decision

What is decided, as an active present-tense declaration ("we will...").
Specific enough to implement from scratch without follow-up questions: name the
package, type, topic, env var or table.

## Consequences

What becomes easier, what becomes harder, what is deferred. Be honest about the
downsides.
```

In MDX any `{siteId}`-style path segment must be wrapped in backticks, or the
docs build reads it as an expression.

The `## Decision` section deserves the most editing. Model it on
`docs/adr/0001-pick-face-replenishment-bounded-context.md` (canvas, context map
with the relationship per edge, explicit exclusions, planned edges marked as
planned) and `docs/adr/0003-local-copies-and-consumed-contracts.md` (consumed
contracts verified against each producer's develop branch, consumer env names
and modes).

## Keeping the guides and gates in step

- A new CloudEvents type: add it to `apis/asyncapi.yaml` AND to the type
  catalogue in `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`
  (`TestEventCatalogueMatchesContract` fails otherwise), then update
  `.claude/rules/integration-events.md`.
- A new aggregate or invariant: update `.claude/rules/domain-model.md` in the
  same PR, and add any new `internal/domain/` package to the mutation gate.
- A new route: `apis/openapi.yaml` and `.claude/rules/rest-api.md`.
- Run `python3 scripts/harness/guide_lint.py` (or `make guide-lint`): it fails on
  a guide that cites a missing file.

## Superseding an earlier ADR

Do not edit the old ADR's Decision. Change the old ADR's `## Status` to
`Superseded by ADR NNNN` (a one-line patch) and open the new ADR with
`Accepted (date). Supersedes ADR MMMM.`, then update both rows of
`docs/adr/index.md`.

## Cross-repo decisions: a companion ADR, not one repo's private opinion

When a decision spans two repos, write ONE ADR per repo, each pointing at the
other as "the companion ADR" with a one-line split of responsibility. The
execution chain in ADR 0001 (pick-face-replenishment to wes-work-planning to
fulfillment-execution to inventory-storage and back) and the producer changes
P1 and P4 in ADR 0003 are examples: the producer side records its half in its
own repo, because each bounded context's docs are read independently.

## Verify

Check every link and number you cite, and that the index row matches the ADR's
status. There is no docs site build in this repo, so nothing in CI here catches
a broken ADR link; verify them by hand.
