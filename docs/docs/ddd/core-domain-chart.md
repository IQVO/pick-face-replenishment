---
id: core-domain-chart
title: Core domain chart
sidebar_label: Core domain chart
sidebar_position: 11
---

# Core domain chart

Following the ddd-crew [Core Domain Charts](https://github.com/ddd-crew/core-domain-charts): business differentiation on
the vertical axis, model complexity on the horizontal axis. Mermaid numbers the quadrants 1 = top-right (Core),
2 = top-left, 3 = bottom-left (Supporting), 4 = bottom-right (Generic). The coordinates are an assessment made from the
evidence below, not a measurement.

```mermaid
quadrantChart
    title pick-face-replenishment on the core domain chart
    x-axis Low model complexity --> High model complexity
    y-axis Low business differentiation --> High business differentiation
    quadrant-1 Core
    quadrant-2 Decisive - Short-term Core
    quadrant-3 Supporting
    quadrant-4 Generic
    pick-face-replenishment: [0.34, 0.36]
    min-max policy: [0.15, 0.30]
    detector and source bin choice: [0.40, 0.40]
    request lifecycle: [0.35, 0.28]
```

Source: `docs/adr/0001-pick-face-replenishment-bounded-context.md` (canvas, strategic classification),
`docs/adr/0002-aggregates-and-detection-policy.md`, `internal/domain/policy/policy.go`,
`internal/domain/detection/detector.go`, `internal/domain/replenishment/request.go`.
Omits: the sibling contexts (their own charts place them).

## Classification: Supporting

ADR 0001 classifies the context as a **Supporting subdomain**: necessary for picking without stock-outs and specific to
this warehouse's policy, but built from replaceable policy objects rather than the competitive core, which stays the WES
conductor (`wes-work-planning`) and the task lifecycle (`fulfillment-execution`). It sits in the bottom-left quadrant.
The warehouse-docs fleet table agrees ([Subdomain classification](/docs/ddd/subdomain-classification)).

**Business differentiation is moderate-low (y = 0.36).** Keeping forward faces stocked prevents empty faces and short
picks, which matters, and the rules differ per retailer, so it is not Generic. But min/max is a well-understood rule behind a
replaceable policy object, and nothing in the fleet's customer promise depends on it today: no request
is executed on the floor yet ([Integration](/docs/ecosystem/integration)).

**Model complexity is low to moderate (x = 0.34).** Evidence from the code:

- Two small aggregates: a versioned value object (`Policy`) and the `Request` with three states, three events and five
  invariants, restored through a strict `Restore` that rejects an inconsistent snapshot.
- One cross-aggregate rule, at most one open request per (site, forward bin, SKU), enforced by the use case and backed by
  a partial unique index.
- A pure detector with five verdicts and a deterministic, order-independent source bin choice.
- Event-fed read models with version and stamp guards (levels by `version`, the forward map by `approved_at`) and
  idempotent consumers.

What would move the point: a demand-cover policy (planned in ADR 0001, not built) would raise differentiation and
complexity; if a vendor WMS replenishment module sat behind an anti-corruption layer the context would drop towards
Generic.
