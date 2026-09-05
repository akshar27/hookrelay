-- +goose Up
-- 'blocked' deliveries (circuit open) are re-claimed after the cooldown, so the
-- due-work partial index must cover them too.
DROP INDEX idx_deliveries_due;
CREATE INDEX idx_deliveries_due ON deliveries (next_attempt_at)
    WHERE status IN ('pending', 'failed', 'blocked');

-- +goose Down
DROP INDEX idx_deliveries_due;
CREATE INDEX idx_deliveries_due ON deliveries (next_attempt_at)
    WHERE status IN ('pending', 'failed');
