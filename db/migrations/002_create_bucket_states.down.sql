-- Migration 002 DOWN
DROP TRIGGER IF EXISTS trg_bucket_states_updated_at ON bucket_states;
DROP TABLE   IF EXISTS bucket_states;
