-- +goose Up
-- Baseline. goose creates its own goose_db_version table; real schema starts in
-- 0002 (api_keys, endpoints).
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- +goose Down
DROP EXTENSION IF EXISTS pgcrypto;
