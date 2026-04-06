CREATE TABLE IF NOT EXISTS devices (
    device_id        TEXT PRIMARY KEY,
    type             TEXT NOT NULL,
    company          TEXT NOT NULL,
    name             TEXT NOT NULL,
    location         TEXT NOT NULL,
    timezone         TEXT NOT NULL,
    floor_count      INTEGER,
    installed_date   TEXT,
    reading_types    TEXT[]  NOT NULL DEFAULT '{}',
    alert_thresholds JSONB   NOT NULL DEFAULT '{}'
);
