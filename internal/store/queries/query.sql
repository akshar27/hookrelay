-- Read-side queries: filtered + keyset-paginated feeds, replay, endpoint health.

-- name: ListDeliveries :many
SELECT * FROM deliveries
WHERE (sqlc.narg('endpoint_id')::uuid IS NULL OR endpoint_id = sqlc.narg('endpoint_id')::uuid)
  AND (sqlc.narg('event_id')::uuid IS NULL OR event_id = sqlc.narg('event_id')::uuid)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR created_at >= sqlc.narg('from_ts')::timestamptz)
  AND (sqlc.narg('to_ts')::timestamptz IS NULL OR created_at < sqlc.narg('to_ts')::timestamptz)
  AND (
    sqlc.narg('cursor_ts')::timestamptz IS NULL
    OR created_at < sqlc.narg('cursor_ts')::timestamptz
    OR (created_at = sqlc.narg('cursor_ts')::timestamptz AND id < sqlc.narg('cursor_id')::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim');

-- name: ListEvents :many
SELECT * FROM events
WHERE (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type')::text)
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR received_at >= sqlc.narg('from_ts')::timestamptz)
  AND (sqlc.narg('to_ts')::timestamptz IS NULL OR received_at < sqlc.narg('to_ts')::timestamptz)
  AND (
    sqlc.narg('cursor_ts')::timestamptz IS NULL
    OR received_at < sqlc.narg('cursor_ts')::timestamptz
    OR (received_at = sqlc.narg('cursor_ts')::timestamptz AND id < sqlc.narg('cursor_id')::uuid)
  )
ORDER BY received_at DESC, id DESC
LIMIT sqlc.arg('lim');

-- name: ReplayDelivery :one
INSERT INTO deliveries (event_id, endpoint_id, status, next_attempt_at, is_replay, replay_of)
SELECT src.event_id, src.endpoint_id, 'pending', now(), true, src.id
FROM deliveries src WHERE src.id = $1
RETURNING *;

-- name: EndpointHealth :one
SELECT
    e.breaker_state AS breaker_state,
    e.breaker_open_until AS breaker_open_until,
    COALESCE(count(a.id) FILTER (WHERE a.outcome = 'success'), 0)::bigint AS successes_24h,
    COALESCE(count(a.id), 0)::bigint AS attempts_24h,
    COALESCE(percentile_disc(0.95) WITHIN GROUP (ORDER BY a.duration_ms), 0)::int AS p95_ms_24h
FROM endpoints e
LEFT JOIN deliveries d ON d.endpoint_id = e.id
LEFT JOIN attempts a ON a.delivery_id = d.id AND a.attempted_at >= now() - interval '24 hours'
WHERE e.id = $1
GROUP BY e.id;
