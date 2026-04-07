-- name: LoadBucketStates :many
-- Loads persisted token counts for all clients of a tenant.
-- Used once at startup to restore in-memory bucket state.
SELECT
    tenant_id,
    client_id,
    tokens::float8 AS tokens,
    last_refill_at
FROM  bucket_states
WHERE tenant_id = $1
ORDER BY client_id;

-- name: UpsertBucketState :batchexec
-- Write-behind flush: upserts a single bucket's state.
-- Called in a pgx batch by the flusher goroutine every 100ms.
-- ON CONFLICT handles both the first write and all subsequent updates.
INSERT INTO bucket_states (tenant_id, client_id, tokens, last_refill_at)
VALUES (@tenant_id, @client_id, @tokens, @last_refill_at)
ON CONFLICT (tenant_id, client_id)
DO UPDATE SET
    tokens         = EXCLUDED.tokens,
    last_refill_at = EXCLUDED.last_refill_at,
    updated_at     = NOW();
