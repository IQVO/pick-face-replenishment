-- pick-face-replenishment analytics read model (ADR 0008).
--
-- This is the ANALYTICAL database (pick_face_replenishment_analytics),
-- separate from the OLTP database. It is written only by cmd/pfr-projector and
-- read (read-only) by cmd/pfr-reports. Everything here is a projection derived
-- from warehouse.pick-face-replenishment.analytics, not a source of truth: it
-- can be dropped and rebuilt by replaying the topic under a new consumer
-- group.
--
-- Facts, not aggregates: every per-day number of the funnel report is counted
-- from these rows over the requested window. Each milestone column is the
-- CloudEvents time of the EARLIEST such event seen (kept with LEAST, which
-- ignores NULLs), so arrival order never matters.

-- Idempotency: every applied CloudEvents id is recorded here exactly once, in
-- the SAME transaction as its effect. occurred_at is the event's CloudEvents
-- `time`; max(occurred_at) is the freshness of the projection.
CREATE TABLE analytics_processed_events (
    event_id    TEXT        PRIMARY KEY,
    event_type  TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_analytics_processed_events_occurred_at ON analytics_processed_events (occurred_at);

-- replenishment_facts: one row per replenishment request the projection has
-- seen (any of its events), one column per milestone.
--   requested_at   ReplenishmentRequested (quantity, priority)
--   completed_at   ReplenishmentCompleted (the quantity moved)
--   cancelled_at   ReplenishmentCancelled (the reason)
-- A request is OPEN from requested_at until completed_at or cancelled_at is
-- set; completion latency is completed_at - requested_at and exists only when
-- the projection saw both.
CREATE TABLE replenishment_facts (
    request_id         TEXT COLLATE "C" PRIMARY KEY,
    site_id            TEXT COLLATE "C" NOT NULL,
    sku                TEXT COLLATE "C" NOT NULL,
    priority           TEXT COLLATE "C",
    requested_quantity BIGINT,
    completed_quantity BIGINT,
    cancel_reason      TEXT COLLATE "C",
    requested_at       TIMESTAMPTZ,
    completed_at       TIMESTAMPTZ,
    cancelled_at       TIMESTAMPTZ
);

CREATE INDEX idx_replenishment_facts_site         ON replenishment_facts (site_id);
CREATE INDEX idx_replenishment_facts_requested_at ON replenishment_facts (requested_at) WHERE requested_at IS NOT NULL;
CREATE INDEX idx_replenishment_facts_completed_at ON replenishment_facts (completed_at) WHERE completed_at IS NOT NULL;
CREATE INDEX idx_replenishment_facts_cancelled_at ON replenishment_facts (cancelled_at) WHERE cancelled_at IS NOT NULL;

-- policy_declaration_facts: one row per policy VERSION declared for a site and
-- SKU (ReplenishmentPolicyDeclared, analytics-only). declared_at keeps the
-- earliest time seen for the version.
CREATE TABLE policy_declaration_facts (
    site_id     TEXT COLLATE "C" NOT NULL,
    sku         TEXT COLLATE "C" NOT NULL,
    version     BIGINT      NOT NULL,
    min_qty     BIGINT      NOT NULL,
    max_qty     BIGINT      NOT NULL,
    multiple    BIGINT      NOT NULL,
    declared_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (site_id, sku, version)
);

CREATE INDEX idx_policy_declaration_facts_declared_at ON policy_declaration_facts (declared_at);
