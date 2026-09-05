-- name: InsertEvent :one
INSERT INTO events (api_key_id, type, payload, idempotency_key, occurred_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetEventByIdempotencyKey :one
SELECT * FROM events
WHERE api_key_id = $1 AND idempotency_key = $2;

-- name: GetEvent :one
SELECT * FROM events WHERE id = $1;

-- name: ListUnfannedEventIDs :many
SELECT id FROM events
WHERE fanned_out = false
ORDER BY received_at
LIMIT $1;

-- name: MarkEventFannedOut :exec
UPDATE events SET fanned_out = true WHERE id = $1;

-- Fan an event out to every enabled endpoint whose filter matches its type.
-- Idempotent: ON CONFLICT (event_id, endpoint_id) DO NOTHING.
-- name: FanOutEvent :execrows
INSERT INTO deliveries (event_id, endpoint_id)
SELECT sqlc.arg('event_id'), e.id
FROM endpoints e
WHERE e.status = 'enabled'
  AND (
    e.filter->>'mode' = 'all'
    OR EXISTS (
      SELECT 1
      FROM jsonb_array_elements_text(e.filter->'types') AS pat
      WHERE pat = '*'
         OR pat = sqlc.arg('event_type')::text
         OR (right(pat, 2) = '.*'
             AND left(sqlc.arg('event_type')::text, length(pat) - 1) = left(pat, length(pat) - 1))
    )
  )
ON CONFLICT (event_id, endpoint_id) DO NOTHING;

-- name: CountEventsByType :many
SELECT type, count(*)::bigint AS n FROM events GROUP BY type;
