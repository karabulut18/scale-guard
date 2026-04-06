-- Migration 001 UP: Rate limit configuration table.
--
-- This is the "slow path" table. It is read once at startup and refreshed
-- every 30s by the config-watcher goroutine. It is NEVER touched on the
-- hot path (CheckRateLimit). Writes happen only via an admin API or direct
-- DB update.

CREATE TABLE rate_limit_configs (
    id          BIGSERIAL       PRIMARY KEY,

    -- Multi-tenancy key. tenant_id identifies the owning service or team.
    -- client_id identifies the individual caller: API key, user ID, IP, etc.
    -- Together they form the unique rate-limit identity.
    tenant_id   TEXT            NOT NULL,
    client_id   TEXT            NOT NULL,

    -- Token Bucket parameters.
    -- capacity    = max tokens the bucket can hold (burst ceiling).
    -- refill_rate = tokens added per second (sustained throughput ceiling).
    -- Example: capacity=100, refill_rate=10 means: allow bursts up to 100 req,
    --          recovering at 10 req/sec.
    capacity    INTEGER         NOT NULL CHECK (capacity > 0),
    refill_rate NUMERIC(12, 4)  NOT NULL CHECK (refill_rate > 0),

    is_active   BOOLEAN         NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_rate_limit_configs_tenant_client
        UNIQUE (tenant_id, client_id)
);

-- Index for the 30s config-refresh query: load all active configs for a tenant.
CREATE INDEX idx_rlc_tenant_active
    ON rate_limit_configs (tenant_id)
    WHERE is_active = TRUE;

-- Auto-update updated_at on any row change.
CREATE OR REPLACE FUNCTION fn_set_updated_at()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_rate_limit_configs_updated_at
    BEFORE UPDATE ON rate_limit_configs
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- ---------------------------------------------------------------------------
-- Seed data: two tenants, three clients each, for local dev and integration tests.
-- ---------------------------------------------------------------------------
INSERT INTO rate_limit_configs (tenant_id, client_id, capacity, refill_rate) VALUES
    -- Tenant A: e-commerce storefront
    ('tenant_storefront', 'api_key_browse',   200,  50.0),   -- browse/search: generous
    ('tenant_storefront', 'api_key_checkout',  20,   5.0),   -- checkout: strict
    ('tenant_storefront', 'api_key_default',  100,  10.0),   -- catch-all default

    -- Tenant B: internal analytics pipeline
    ('tenant_analytics',  'pipeline_ingest',  500, 100.0),   -- high-throughput ingest
    ('tenant_analytics',  'pipeline_query',    50,  10.0),   -- ad-hoc queries: limited
    ('tenant_analytics',  'api_key_default',  100,  10.0);
