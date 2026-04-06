CREATE TABLE IF NOT EXISTS reading_inputs (
    id          BIGSERIAL PRIMARY KEY,
    reading_id  BIGINT           NOT NULL REFERENCES readings(id) ON DELETE CASCADE,
    input_name  TEXT             NOT NULL,
    input_value DOUBLE PRECISION NOT NULL,
    CONSTRAINT chk_motor_status_value
        CHECK (input_name != 'motor_status' OR input_value IN (0, 1))
);

CREATE INDEX IF NOT EXISTS idx_reading_inputs_reading_id
    ON reading_inputs(reading_id);

CREATE INDEX IF NOT EXISTS idx_reading_inputs_name
    ON reading_inputs(input_name);
