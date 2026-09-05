-- +goose Up
CREATE TABLE events (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    api_key_id      uuid NOT NULL REFERENCES api_keys (id),
    type            text NOT NULL,
    payload         jsonb NOT NULL,
    idempotency_key text,
    occurred_at     timestamptz,
    received_at     timestamptz NOT NULL DEFAULT now(),
    fanned_out      boolean NOT NULL DEFAULT false
);

CREATE UNIQUE INDEX uq_events_idempotency
    ON events (api_key_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_events_unfanned ON events (received_at) WHERE fanned_out = false;
CREATE INDEX idx_events_type ON events (type);

-- One row per (event, endpoint). This table is also the work queue.
CREATE TABLE deliveries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),  -- also the Webhook-Id
    event_id        uuid NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    endpoint_id     uuid NOT NULL REFERENCES endpoints (id) ON DELETE CASCADE,
    status          text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'delivering', 'succeeded', 'failed', 'dead', 'blocked')),
    attempt_count   int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    locked_until    timestamptz,
    locked_by       text,
    last_status_code int,
    last_error      text,
    delivered_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, endpoint_id)
);

CREATE INDEX idx_deliveries_due ON deliveries (next_attempt_at)
    WHERE status IN ('pending', 'failed');
CREATE INDEX idx_deliveries_endpoint ON deliveries (endpoint_id, created_at DESC);
CREATE INDEX idx_deliveries_delivering ON deliveries (locked_until)
    WHERE status = 'delivering';

-- +goose Down
DROP TABLE deliveries;
DROP TABLE events;
