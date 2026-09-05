-- Claim a batch of due deliveries: mark them 'delivering', take a lease, and
-- bump attempt_count now so a crash can't cause unbounded retries. SKIP LOCKED
-- lets many workers/instances pull disjoint batches with no coordinator.
-- name: ClaimDueDeliveries :many
WITH due AS (
    SELECT id FROM deliveries
    WHERE status IN ('pending', 'failed', 'blocked') AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg('batch_size')
)
UPDATE deliveries d
SET status        = 'delivering',
    locked_until  = now() + (sqlc.arg('lease_seconds')::int * interval '1 second'),
    locked_by     = sqlc.arg('locked_by'),
    attempt_count = attempt_count + 1
FROM due
WHERE d.id = due.id
RETURNING d.id;

-- Everything the delivery pipeline needs, in one row.
-- name: GetDeliveryDispatch :one
SELECT
    d.id             AS delivery_id,
    d.attempt_count  AS attempt_count,
    d.event_id       AS event_id,
    e.id             AS endpoint_id,
    e.url            AS url,
    e.secret_enc     AS secret_enc,
    e.timeout_ms     AS timeout_ms,
    e.max_attempts   AS max_attempts,
    e.max_4xx_attempts AS max_4xx_attempts,
    e.rate_limit_rps AS rate_limit_rps,
    e.breaker_threshold AS breaker_threshold,
    e.breaker_cooldown_s AS breaker_cooldown_s,
    e.status         AS endpoint_status,
    e.allow_private  AS allow_private,
    ev.type          AS event_type,
    ev.payload       AS payload,
    ev.api_key_id    AS api_key_id
FROM deliveries d
JOIN endpoints e ON e.id = d.endpoint_id
JOIN events ev ON ev.id = d.event_id
WHERE d.id = $1;

-- name: InsertAttempt :exec
INSERT INTO attempts (delivery_id, n, request_headers, status_code, response_snippet, duration_ms, outcome, error)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListAttempts :many
SELECT * FROM attempts WHERE delivery_id = $1 ORDER BY n;

-- name: MarkDeliverySucceeded :exec
UPDATE deliveries
SET status = 'succeeded', delivered_at = now(), locked_until = NULL, locked_by = NULL,
    last_status_code = $2, last_error = NULL
WHERE id = $1;

-- name: MarkDeliveryFailed :exec
UPDATE deliveries
SET status = 'failed', next_attempt_at = $2, locked_until = NULL, locked_by = NULL,
    last_status_code = $3, last_error = $4
WHERE id = $1;

-- name: MarkDeliveryDead :exec
UPDATE deliveries
SET status = 'dead', locked_until = NULL, locked_by = NULL,
    last_status_code = $2, last_error = $3
WHERE id = $1;

-- Release a claimed delivery without an HTTP attempt (paused / rate-limited):
-- give back the attempt_count that ClaimDueDeliveries pre-charged.
-- name: ReleaseDelivery :exec
UPDATE deliveries
SET status = 'pending', next_attempt_at = $2, locked_until = NULL, locked_by = NULL,
    attempt_count = GREATEST(attempt_count - 1, 0)
WHERE id = $1;

-- name: MarkDeliveryBlocked :exec
UPDATE deliveries
SET status = 'blocked', next_attempt_at = $2, locked_until = NULL, locked_by = NULL,
    last_error = $3, attempt_count = GREATEST(attempt_count - 1, 0)
WHERE id = $1;

-- Flip leases that outlived their worker back to 'failed' so they're retried.
-- name: ReapStaleDeliveries :many
UPDATE deliveries
SET status = 'failed', next_attempt_at = now(), locked_until = NULL, locked_by = NULL,
    last_error = 'worker_lost'
WHERE status = 'delivering' AND locked_until < now()
RETURNING id, attempt_count, endpoint_id;
