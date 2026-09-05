-- +goose Up
-- A replay creates a fresh delivery row for the same (event, endpoint); the
-- original stays as history. So the fan-out uniqueness constraint must apply
-- only to non-replay rows.
ALTER TABLE deliveries ADD COLUMN is_replay  boolean NOT NULL DEFAULT false;
ALTER TABLE deliveries ADD COLUMN replay_of  uuid REFERENCES deliveries (id);

ALTER TABLE deliveries DROP CONSTRAINT deliveries_event_id_endpoint_id_key;
CREATE UNIQUE INDEX uq_deliveries_fanout
    ON deliveries (event_id, endpoint_id)
    WHERE NOT is_replay;

-- +goose Down
DROP INDEX uq_deliveries_fanout;
ALTER TABLE deliveries ADD CONSTRAINT deliveries_event_id_endpoint_id_key UNIQUE (event_id, endpoint_id);
ALTER TABLE deliveries DROP COLUMN replay_of;
ALTER TABLE deliveries DROP COLUMN is_replay;
