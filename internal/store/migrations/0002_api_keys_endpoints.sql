-- +goose Up
CREATE TABLE api_keys (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    key_hash    text NOT NULL,               -- sha256 hex of the raw key
    key_prefix  text NOT NULL UNIQUE,         -- first 12 chars, for lookup + display
    created_at  timestamptz NOT NULL DEFAULT now(),
    disabled_at timestamptz
);

CREATE TABLE endpoints (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 text NOT NULL,
    url                  text NOT NULL,
    secret_enc           bytea NOT NULL,      -- AES-GCM sealed signing secret
    filter               jsonb NOT NULL DEFAULT '{"mode":"all"}'::jsonb,
    status               text NOT NULL DEFAULT 'enabled'
                             CHECK (status IN ('enabled', 'disabled', 'paused')),
    rate_limit_rps       int NOT NULL DEFAULT 0,      -- 0 = unlimited
    timeout_ms           int NOT NULL DEFAULT 10000,
    max_attempts         int NOT NULL DEFAULT 12,
    max_4xx_attempts     int NOT NULL DEFAULT 3,
    breaker_threshold    int NOT NULL DEFAULT 5,
    breaker_cooldown_s   int NOT NULL DEFAULT 60,
    breaker_state        text NOT NULL DEFAULT 'closed'
                             CHECK (breaker_state IN ('closed', 'open', 'half_open')),
    breaker_open_until    timestamptz,
    consecutive_failures  int NOT NULL DEFAULT 0,
    allow_private         boolean NOT NULL DEFAULT false,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_endpoints_status ON endpoints (status);

-- +goose Down
DROP TABLE endpoints;
DROP TABLE api_keys;
