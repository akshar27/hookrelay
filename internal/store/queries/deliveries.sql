-- name: GetDelivery :one
SELECT * FROM deliveries WHERE id = $1;

-- name: ListDeliveriesForEvent :many
SELECT * FROM deliveries WHERE event_id = $1 ORDER BY created_at;

-- name: CountDeliveriesByStatus :many
SELECT status, count(*)::bigint AS n FROM deliveries GROUP BY status;
