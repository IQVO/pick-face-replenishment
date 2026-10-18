---
id: index
title: Architecture decision records
sidebar_label: ADR index
slug: /
---

# Architecture decision records

The decisions of `pick-face-replenishment`, in the order they were taken. The
ADR bodies are immutable; a changed decision gets a new ADR that supersedes
the old one. The *Status* column is copied from each ADR's own `## Status`
section.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](/docs/adr/0001-pick-face-replenishment-bounded-context) | pick-face-replenishment as the owner of forward pick-face replenishment | Accepted (2026-10-17) |
| [0002](/docs/adr/0002-aggregates-and-detection-policy) | the ReplenishmentPolicy and ReplenishmentRequest aggregates, their invariants, and the detection policy ports | Accepted (2026-10-17) |
| [0003](/docs/adr/0003-local-copies-and-consumed-contracts) | event-fed local copies and the consumed contracts | Accepted (2026-10-17) |
| [0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue) | CloudEvents envelope and type catalogue | Accepted (2026-10-17) |
| [0005](/docs/adr/0005-evaluation-semantics-and-persistence) | evaluation semantics and persistence | Accepted (2026-10-17) |
| [0006](/docs/adr/0006-postgres-and-kafka-are-the-only-runtime) | Postgres and Kafka are the only runtime | Accepted (2026-10-17) |
| [0007](/docs/adr/0007-mcp-server-adoption) | MCP server adoption (read-only, unauthenticated, additive) | Accepted (2026-10-18) |
