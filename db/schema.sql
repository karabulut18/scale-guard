-- schema.sql — read by sqlc to understand table shapes and generate Go types.
-- This is NOT used by golang-migrate. The source of truth for the live DB
-- is db/migrations/. Keep these in sync when adding migrations.

CREATE TABLE rate_limit_configs (
    id          BIGSERIAL       PRIMARY KEY,
    tenant_id   TEXT            NOT NULL,
    client_id   TEXT            NOT NULL,
    capacity    INTEGER         NOT NULL CHECK (capacity > 0),
    refill_rate NUMERIC(12, 4)  NOT NULL CHECK (refill_rate > 0),
    is_active   BOOLEAN         NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_rate_limit_configs_tenant_client
        UNIQUE (tenant_id, client_id)
);

CREATE TABLE bucket_states (
    id             BIGSERIAL       PRIMARY KEY,
    tenant_id      TEXT            NOT NULL,
    client_id      TEXT            NOT NULL,
    tokens         NUMERIC(12, 4)  NOT NULL,
    last_refill_at TIMESTAMPTZ     NOT NULL,
    updated_at     TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_bucket_states_tenant_client
        UNIQUE (tenant_id, client_id),
    CONSTRAINT fk_bucket_states_config
        FOREIGN KEY (tenant_id, client_id)
        REFERENCES rate_limit_configs (tenant_id, client_id)
        ON DELETE CASCADE
);
