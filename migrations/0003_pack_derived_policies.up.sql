-- 0003_pack_derived_policies.up.sql: pack-derived replenishment multiple
-- (ADR 0009).
--
-- policies gains the source of its multiple and a stale flag. Additive: every
-- existing row is a DECLARED, not stale policy, which is exactly what it was.
ALTER TABLE policies
    ADD COLUMN multiple_source TEXT    NOT NULL DEFAULT 'DECLARED'
        CHECK (multiple_source IN ('DECLARED', 'PACK_DERIVED')),
    ADD COLUMN stale           BOOLEAN NOT NULL DEFAULT false,
    ADD CONSTRAINT policies_stale_only_pack_derived
        CHECK (NOT stale OR multiple_source = 'PACK_DERIVED');

-- Re-deriving finds the PACK_DERIVED policies of one SKU across sites.
CREATE INDEX idx_policies_pack_derived_by_sku
    ON policies (sku)
    WHERE multiple_source = 'PACK_DERIVED';

-- pack_levels: the local copy of product-master's pack levels (ADR 0009), one
-- row per (SKU, level), the latest state guarded by the product aggregate
-- `version` of the last message applied. A retired level stays (effective_to
-- set): an end-dated level is history, not a deletion.
CREATE TABLE pack_levels (
    sku            TEXT COLLATE "C" NOT NULL,
    level          TEXT        NOT NULL CHECK (level IN ('EACH', 'INNER', 'CASE', 'PALLET')),
    gtin           TEXT        NOT NULL,
    units_per_pack BIGINT      NOT NULL CHECK (units_per_pack >= 1),
    effective_from TIMESTAMPTZ NOT NULL,
    effective_to   TIMESTAMPTZ,
    version        BIGINT      NOT NULL CHECK (version >= 1),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (sku, level)
);
