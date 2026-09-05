-- name: CreateEndpoint :one
INSERT INTO endpoints (
    name, url, secret_enc, filter, rate_limit_rps, timeout_ms,
    max_attempts, max_4xx_attempts, breaker_threshold, breaker_cooldown_s, allow_private
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetEndpoint :one
SELECT * FROM endpoints WHERE id = $1;

-- name: ListEndpoints :many
SELECT * FROM endpoints ORDER BY created_at DESC;

-- name: ListEnabledEndpoints :many
SELECT * FROM endpoints WHERE status = 'enabled';

-- name: UpdateEndpoint :one
UPDATE endpoints SET
    name           = COALESCE(sqlc.narg('name'), name),
    url            = COALESCE(sqlc.narg('url'), url),
    filter         = COALESCE(sqlc.narg('filter'), filter),
    status         = COALESCE(sqlc.narg('status'), status),
    rate_limit_rps = COALESCE(sqlc.narg('rate_limit_rps'), rate_limit_rps),
    timeout_ms     = COALESCE(sqlc.narg('timeout_ms'), timeout_ms),
    max_attempts   = COALESCE(sqlc.narg('max_attempts'), max_attempts),
    updated_at     = now()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: DeleteEndpoint :exec
DELETE FROM endpoints WHERE id = $1;

-- name: RotateEndpointSecret :one
UPDATE endpoints SET secret_enc = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SnapshotBreakerState :exec
UPDATE endpoints SET
    breaker_state        = $2,
    breaker_open_until    = $3,
    consecutive_failures  = $4,
    updated_at            = now()
WHERE id = $1;

-- name: ResetBreaker :exec
UPDATE endpoints SET
    breaker_state = 'closed',
    breaker_open_until = NULL,
    consecutive_failures = 0,
    updated_at = now()
WHERE id = $1;
