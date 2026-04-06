-- Migration 001 DOWN
DROP TRIGGER  IF EXISTS trg_rate_limit_configs_updated_at ON rate_limit_configs;
DROP FUNCTION IF EXISTS fn_set_updated_at();
DROP TABLE    IF EXISTS rate_limit_configs;
