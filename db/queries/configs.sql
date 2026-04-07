-- name: LoadConfigs :many
-- Loads all active rate-limit configurations for a tenant.
-- capacity is cast to float8 so sqlc generates float64, matching the bucket's type.
SELECT
    tenant_id,
    client_id,
    capacity::float8    AS capacity,
    refill_rate::float8 AS refill_rate
FROM  rate_limit_configs
WHERE tenant_id = $1
  AND is_active = TRUE
ORDER BY client_id;
