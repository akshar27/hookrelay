-- name: CreateAPIKey :one
INSERT INTO api_keys (name, key_hash, key_prefix)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetAPIKeyByPrefix :one
SELECT * FROM api_keys WHERE key_prefix = $1;

-- name: ListAPIKeys :many
SELECT id, name, key_prefix, created_at, disabled_at
FROM api_keys
ORDER BY created_at DESC;

-- name: DisableAPIKey :exec
UPDATE api_keys SET disabled_at = now() WHERE id = $1 AND disabled_at IS NULL;
