-- +goose Up
CREATE TABLE attempts (
    id               bigserial PRIMARY KEY,
    delivery_id      uuid NOT NULL REFERENCES deliveries (id) ON DELETE CASCADE,
    n                int NOT NULL,                       -- 1-based attempt number
    request_headers  jsonb NOT NULL DEFAULT '{}'::jsonb, -- signature redacted
    status_code      int,
    response_snippet text,                              -- first 2 KB of the body
    duration_ms      int NOT NULL DEFAULT 0,
    outcome          text NOT NULL
                         CHECK (outcome IN ('success', 'http_error', 'timeout',
                                            'connection_error', 'worker_lost',
                                            'ssrf_blocked', 'throttled')),
    error            text,
    attempted_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_attempts_delivery ON attempts (delivery_id, n);

-- +goose Down
DROP TABLE attempts;
