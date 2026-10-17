-- 0002_forward_assignment_maps.up.sql: the stale-map guard (ADR 0005).
--
-- One row per site: the plan and the approval instant of the forward map
-- currently held in forward_assignments. ApplyForwardAssignmentMap ignores a
-- SlotPlanApproved whose approved_at is not after the recorded one. Additive:
-- a site without a row has never had a map applied (its first map is never
-- stale), so existing data needs no backfill.
CREATE TABLE forward_assignment_maps (
    site_id     TEXT COLLATE "C" PRIMARY KEY,
    plan_id     TEXT        NOT NULL,
    approved_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
