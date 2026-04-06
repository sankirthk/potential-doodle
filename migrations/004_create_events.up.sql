CREATE TABLE IF NOT EXISTS events (
    id            BIGSERIAL PRIMARY KEY,
    device_id     TEXT             NOT NULL REFERENCES devices(device_id),
    message_type  TEXT             NOT NULL,  -- 'alert' | 'recovery'
    timestamp_ms  BIGINT           NOT NULL,
    alert_type    TEXT             NOT NULL,
    severity      TEXT             NOT NULL,  -- 'critical' | 'warning'
    threshold     DOUBLE PRECISION,           -- NULL for non-threshold events
    reading_value DOUBLE PRECISION,           -- NULL for non-threshold events
    reading_name  TEXT,                       -- NULL for non-threshold events
    CONSTRAINT chk_threshold_fields_consistent
        CHECK (
            (threshold IS NULL AND reading_value IS NULL AND reading_name IS NULL)
            OR
            (threshold IS NOT NULL AND reading_value IS NOT NULL AND reading_name IS NOT NULL)
        )
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_events_dedup
    ON events(device_id, timestamp_ms, message_type);

CREATE INDEX IF NOT EXISTS idx_events_device_ts
    ON events(device_id, timestamp_ms);

CREATE INDEX IF NOT EXISTS idx_events_severity
    ON events(severity);
