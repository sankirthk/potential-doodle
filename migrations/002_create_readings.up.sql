CREATE TABLE IF NOT EXISTS readings (
    id           BIGSERIAL PRIMARY KEY,
    device_id    TEXT   NOT NULL REFERENCES devices(device_id),
    timestamp_ms BIGINT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_readings_dedup
    ON readings(device_id, timestamp_ms);
