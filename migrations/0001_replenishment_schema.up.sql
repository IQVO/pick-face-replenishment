-- 0001_replenishment_schema.up.sql: pick-face-replenishment's OLTP schema
-- (ADR 0005).
--
-- Text keys that are compared or ordered in Go (request ids, SKUs, bins, site
-- ids) use the "C" collation so ORDER BY and keyset comparisons follow byte
-- order, the same order the in-memory adapters use.

-- policies: the MIN_MAX ReplenishmentPolicy of one (site, SKU). version starts
-- at 1 and guards every UPDATE and DELETE (... WHERE version = <loaded>).
CREATE TABLE policies (
    site_id     TEXT COLLATE "C" NOT NULL,
    sku         TEXT COLLATE "C" NOT NULL,
    min_qty     BIGINT      NOT NULL CHECK (min_qty >= 0),
    max_qty     BIGINT      NOT NULL,
    multiple    BIGINT      NOT NULL DEFAULT 1 CHECK (multiple >= 1),
    version     BIGINT      NOT NULL CHECK (version >= 1),
    declared_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (site_id, sku),
    CHECK (min_qty < max_qty)
);

-- replenishment_requests: one row per ReplenishmentRequest aggregate (the
-- whole snapshot). version starts at 1 and guards every UPDATE.
CREATE TABLE replenishment_requests (
    request_id     TEXT COLLATE "C" PRIMARY KEY,
    site_id        TEXT COLLATE "C" NOT NULL,
    sku            TEXT COLLATE "C" NOT NULL,
    forward_bin    TEXT COLLATE "C" NOT NULL,
    source_bin     TEXT COLLATE "C" NOT NULL,
    quantity       BIGINT      NOT NULL CHECK (quantity >= 1),
    moved_quantity BIGINT      NOT NULL DEFAULT 0 CHECK (moved_quantity >= 0),
    priority       TEXT        NOT NULL CHECK (priority IN ('NORMAL', 'URGENT')),
    state          TEXT        NOT NULL CHECK (state IN ('Requested', 'Completed', 'Cancelled')),
    policy_min     BIGINT      NOT NULL CHECK (policy_min >= 0),
    policy_max     BIGINT      NOT NULL,
    requested_at   TIMESTAMPTZ NOT NULL,
    completed_at   TIMESTAMPTZ,
    cancelled_at   TIMESTAMPTZ,
    cancel_reason  TEXT        CHECK (cancel_reason IN ('OPERATOR', 'ASSIGNMENT_CHANGED', 'POLICY_REMOVED')),
    version        BIGINT      NOT NULL CHECK (version >= 1),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (policy_min < policy_max),
    CHECK (source_bin <> forward_bin)
);

-- Invariant 1 (ADR 0002): at most ONE open request per (site, forward bin,
-- SKU). The rule spans requests, so the use case checks it and this index backs
-- it: the losing insert of a race fails here and becomes
-- repository.ErrOpenRequestExists.
CREATE UNIQUE INDEX uq_requests_one_open_per_pick_face
    ON replenishment_requests (site_id, forward_bin, sku)
    WHERE state = 'Requested';

-- The open requests of a site / of a site and SKU (cancel on policy removal or
-- on an assignment change).
CREATE INDEX idx_requests_open_by_site_sku
    ON replenishment_requests (site_id, sku)
    WHERE state = 'Requested';

-- GET /replenishments: newest first, keyset paged on (requested_at, request_id).
CREATE INDEX idx_requests_listing
    ON replenishment_requests (requested_at DESC, request_id DESC);

-- bin_stock_levels: the bin stock copy (ADR 0003), one row per (site, bin,
-- SKU), the ABSOLUTE level guarded by the per-(bin, SKU) version of the last
-- message applied. A bin never seen has no row and reads as level 0 (ADR 0005).
CREATE TABLE bin_stock_levels (
    site_code  TEXT COLLATE "C" NOT NULL,
    bin        TEXT COLLATE "C" NOT NULL,
    sku        TEXT COLLATE "C" NOT NULL,
    on_hand    BIGINT      NOT NULL CHECK (on_hand >= 0),
    reserved   BIGINT      NOT NULL CHECK (reserved >= 0),
    version    BIGINT      NOT NULL CHECK (version >= 1),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (site_code, bin, sku)
);

-- The reserve bins of a SKU at a site.
CREATE INDEX idx_bin_stock_levels_site_sku ON bin_stock_levels (site_code, sku);

-- forward_assignments: the site's forward map (ADR 0003), replaced wholesale
-- by every applied SlotPlanApproved. At most one forward bin per (site, SKU).
CREATE TABLE forward_assignments (
    site_id TEXT COLLATE "C" NOT NULL,
    sku     TEXT COLLATE "C" NOT NULL,
    bin     TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (site_id, sku)
);

CREATE INDEX idx_forward_assignments_site_bin ON forward_assignments (site_id, bin);

-- zones and locations: the layout copy. A location joins its zone through
-- zone_id at read time, so the two may arrive in any order.
CREATE TABLE zones (
    zone_id    TEXT PRIMARY KEY,
    site_code  TEXT        NOT NULL,
    zone_code  TEXT        NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_zones_site_zone_code ON zones (site_code, zone_code);

-- decommissioned is irreversible, and the row stays so a late registration
-- cannot bring the location back. An empty role means Storage.
CREATE TABLE locations (
    location_code  TEXT COLLATE "C" PRIMARY KEY,
    zone_id        TEXT        NOT NULL DEFAULT '',
    role           TEXT        NOT NULL DEFAULT '',
    decommissioned BOOLEAN     NOT NULL DEFAULT false,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_locations_zone ON locations (zone_id);

-- processed_events: idempotency guard of the inbound consumers. (consumer,
-- event_id) is claimed with INSERT ... ON CONFLICT DO NOTHING in the SAME
-- transaction as the effect, so a rollback un-claims it.
CREATE TABLE processed_events (
    consumer     TEXT        NOT NULL,
    event_id     TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- outbox_events: the transactional outbox (same shape as the fleet's). One row
-- per already-encoded Kafka message, inserted in the SAME transaction as the
-- aggregate; the relay drains unpublished rows to Kafka.
--   event_id    the CloudEvents `id`, minted once at encode time and persisted:
--               a relay retry republishes the same id.
--   event_type  the FULL CloudEvents `type`.
--   subject     the CloudEvents `subject`.
--   key         the Kafka message key.
--   dataschema  the CloudEvents `dataschema` URN.
--   value       the structured-mode CloudEvents JSON bytes.
--   headers     [{"key":..,"value":..}] Kafka headers (content-type, trace).
CREATE TABLE outbox_events (
    id           BIGSERIAL   PRIMARY KEY,
    event_id     TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    key          BYTEA,
    dataschema   TEXT        NOT NULL,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT,
    CONSTRAINT outbox_events_event_id_topic_key UNIQUE (event_id, topic)
);

-- The relay only ever scans the unpublished tail; keep that scan tiny.
CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

-- idempotency_keys: the answers of POSTs made with an Idempotency-Key. Only 2xx
-- and 4xx answers are stored, never 5xx; the first writer of a key wins.
-- fingerprint identifies the request body so the same key with another body
-- can be told apart.
CREATE TABLE idempotency_keys (
    key          TEXT PRIMARY KEY,
    fingerprint  TEXT        NOT NULL,
    status       INTEGER     NOT NULL CHECK (status >= 200 AND status < 500),
    content_type TEXT        NOT NULL DEFAULT '',
    body         BYTEA       NOT NULL DEFAULT ''::bytea,
    created_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
