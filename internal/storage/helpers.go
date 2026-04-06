package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/lib/pq"
	"github.com/sankirthkalahasti/knaq-take-home/internal/models"
)

// thresholdsToJSON marshals an alert-thresholds map to a JSON byte slice
// suitable for storing in a JSONB column.
func thresholdsToJSON(m map[string]float64) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

// nullableString returns nil for an empty string so it stores as SQL NULL.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// scanDevice scans one devices row into a models.Device.
func scanDevice(row *sql.Row) (models.Device, error) {
	var d models.Device
	var thresholdsJSON []byte
	err := row.Scan(
		&d.DeviceID, &d.Type, &d.Company, &d.Name, &d.Location, &d.Timezone,
		&d.FloorCount, &d.InstalledDate,
		pq.Array(&d.ReadingTypes),
		&thresholdsJSON,
	)
	if err != nil {
		return models.Device{}, err
	}
	if err := json.Unmarshal(thresholdsJSON, &d.AlertThresholds); err != nil {
		return models.Device{}, fmt.Errorf("unmarshal thresholds: %w", err)
	}
	return d, nil
}

// collectReadings assembles a flat join result (one row per input) into
// a slice of Readings, each with its Inputs populated.
func collectReadings(rows *sql.Rows) ([]models.Reading, error) {
	var readings []models.Reading
	idx := map[int64]int{} // readingID → index in readings slice

	for rows.Next() {
		var (
			id          int64
			deviceID    string
			timestampMs int64
			inputName   string
			inputValue  float64
		)
		if err := rows.Scan(&id, &deviceID, &timestampMs, &inputName, &inputValue); err != nil {
			return nil, fmt.Errorf("scan reading row: %w", err)
		}

		i, exists := idx[id]
		if !exists {
			readings = append(readings, models.Reading{
				ID:          id,
				DeviceID:    deviceID,
				TimestampMs: timestampMs,
			})
			i = len(readings) - 1
			idx[id] = i
		}
		readings[i].Inputs = append(readings[i].Inputs, models.ReadingInput{
			ReadingID:  id,
			InputName:  inputName,
			InputValue: inputValue,
		})
	}
	return readings, rows.Err()
}

// scanEvents scans an events query result into a slice of models.Event.
func scanEvents(rows *sql.Rows) ([]models.Event, error) {
	var events []models.Event
	for rows.Next() {
		var e models.Event
		var readingName sql.NullString
		if err := rows.Scan(
			&e.ID, &e.DeviceID, &e.MessageType, &e.TimestampMs,
			&e.AlertType, &e.Severity,
			&e.Threshold, &e.ReadingValue, &readingName,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if readingName.Valid {
			e.ReadingName = readingName.String
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
