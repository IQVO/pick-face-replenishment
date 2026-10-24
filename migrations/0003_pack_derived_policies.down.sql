DROP TABLE IF EXISTS pack_levels;
DROP INDEX IF EXISTS idx_policies_pack_derived_by_sku;
ALTER TABLE policies
    DROP CONSTRAINT IF EXISTS policies_stale_only_pack_derived,
    DROP COLUMN IF EXISTS stale,
    DROP COLUMN IF EXISTS multiple_source;
