-- Migration 002 UP: Bucket state table.
--
-- This is the write-behind target. Every 100ms, the background flusher goroutine
-- UPSERTs the in-memory token counts for all "dirty" buckets into this table.
-- On instance startup, this table is read to restore the last known state,
-- avoiding a cold-start that would give every client a full bucket.
--
-- IMPORTANT: This table is NOT on the hot path. CheckRateLimit never touches it.
-- The authoritative token count lives in memory. This table is persistence only.

CREATE TABLE bucket_states (
    id             BIGSERIAL       PRIMARY KEY,

    tenant_id      TEXT            NOT NULL,
    client_id      TEXT            NOT NULL,

    -- Current token count as of last_refill_at.
    -- Stored as NUMERIC(12,4) to match the float64 in-memory representation.
    -- Fractional tokens are valid: a refill_rate of 0.5 tok/s produces 0.05 tokens
    -- every 100ms flush window.
    tokens         NUMERIC(12, 4)  NOT NULL,

    -- Timestamp of the last token refill calculation. Written by the flusher
    -- alongside tokens so that on restart, the instance can calculate how many
    -- tokens to add for the time elapsed since last flush.
    last_refill_at TIMESTAMPTZ     NOT NULL,

    updated_at     TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_bucket_states_tenant_client
        UNIQUE (tenant_id, client_id),

    -- Cascade deletes: removing a config removes its state.
    CONSTRAINT fk_bucket_states_config
        FOREIGN KEY (tenant_id, client_id)
        REFERENCES rate_limit_configs (tenant_id, client_id)
        ON DELETE CASCADE
);

-- The startup query: JOIN against configs to get capacity for clamp-on-restore.
-- This index makes that JOIN fast.
CREATE INDEX idx_bucket_states_tenant_client
    ON bucket_states (tenant_id, client_id);

CREATE TRIGGER trg_bucket_states_updated_at
    BEFORE UPDATE ON bucket_states
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- ---------------------------------------------------------------------------
-- The write-behind UPSERT pattern.
--
-- Called by the flusher goroutine every 100ms, once per dirty bucket.
-- Uses ON CONFLICT to handle both first-write and subsequent updates without
-- a separate INSERT/UPDATE branch in application code.
--
-- $1 = tenant_id, $2 = client_id, $3 = tokens, $4 = last_refill_at
-- ---------------------------------------------------------------------------
-- (This comment block documents the query used in internal/store/postgres.go)
--
-- INSERT INTO bucket_states (tenant_id, client_id, tokens, last_refill_at)
-- VALUES ($1, $2, $3, $4)
-- ON CONFLICT (tenant_id, client_id)
-- DO UPDATE SET
--     tokens         = EXCLUDED.tokens,
--     last_refill_at = EXCLUDED.last_refill_at,
--     updated_at     = NOW();
--
-- ---------------------------------------------------------------------------
-- The startup state-restore query (also in internal/store/postgres.go):
--
-- SELECT
--     b.tenant_id,
--     b.client_id,
--     b.tokens,
--     b.last_refill_at,
--     c.capacity,
--     c.refill_rate
-- FROM   bucket_states        b
-- JOIN   rate_limit_configs   c USING (tenant_id, client_id)
-- WHERE  c.is_active  = TRUE
--   AND  c.tenant_id  = $1;   -- load one tenant at a time on startup
-- ---------------------------------------------------------------------------
