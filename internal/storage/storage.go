package storage

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/lib/pq"
	"github.com/sankirthkalahasti/knaq-take-home/internal/models"
)

// Store defines all database operations used by the pipeline and API.
type Store interface {
	HasData() (bool, error)
	UpsertDevices(devices []models.Device) error
	InsertReading(r models.Reading) error
	InsertEvent(e models.Event) error

	GetDevice(deviceID string) (models.Device, error)
	GetReadings(deviceID string, from, to int64) ([]models.Reading, error)
	GetStats(deviceID string) ([]models.StatsResponse, error)
	GetDeviceEvents(deviceID, severity string) ([]models.Event, error)
	GetCompanyEvents(company, severity string) ([]models.Event, error)
}

// PostgresStore is the PostgreSQL implementation of Store.
type PostgresStore struct {
	db *sql.DB
}

// New returns a PostgresStore backed by the given *sql.DB.
func New(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// RunMigrations applies all pending up migrations from the given source path.
func RunMigrations(db *sql.DB, migrationsPath string) error {
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("migration driver: %w", err)
	}
	m, err := migrate.NewWithDatabaseInstance("file://"+migrationsPath, "postgres", driver)
	if err != nil {
		return fmt.Errorf("migration init: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migration up: %w", err)
	}
	return nil
}

// isUniqueViolation returns true if err is a PostgreSQL unique-constraint error.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

// HasData returns true if the readings table already contains at least one row.
func (s *PostgresStore) HasData() (bool, error) {
	var exists bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM readings)`).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check existing data: %w", err)
	}
	return exists, nil
}

// UpsertDevices inserts devices, ignoring conflicts on device_id.
func (s *PostgresStore) UpsertDevices(devices []models.Device) error {
	const q = `
		INSERT INTO devices
			(device_id, type, company, name, location, timezone,
			 floor_count, installed_date, reading_types, alert_thresholds)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (device_id) DO NOTHING`

	for _, d := range devices {
		thresholds, err := thresholdsToJSON(d.AlertThresholds)
		if err != nil {
			return fmt.Errorf("marshal thresholds for %s: %w", d.DeviceID, err)
		}
		_, err = s.db.Exec(q,
			d.DeviceID, d.Type, d.Company, d.Name, d.Location, d.Timezone,
			d.FloorCount, d.InstalledDate,
			pq.Array(d.ReadingTypes),
			thresholds,
		)
		if err != nil {
			return fmt.Errorf("upsert device %s: %w", d.DeviceID, err)
		}
	}
	return nil
}

// InsertReading inserts a readings row then batch-inserts its inputs.
// A unique-constraint violation on the reading is treated as a no-op.
func (s *PostgresStore) InsertReading(r models.Reading) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func(tx *sql.Tx) {
		err := tx.Rollback()
		if err != nil {

		}
	}(tx) //nolint:errcheck

	var readingID int64
	err = tx.QueryRow(
		`INSERT INTO readings (device_id, timestamp_ms)
		 VALUES ($1, $2)
		 ON CONFLICT (device_id, timestamp_ms) DO NOTHING
		 RETURNING id`,
		r.DeviceID, r.TimestampMs,
	).Scan(&readingID)

	if errors.Is(err, sql.ErrNoRows) {
		// Duplicate reading — already stored; treat as no-op.
		return tx.Commit()
	}
	if err != nil {
		return fmt.Errorf("insert reading: %w", err)
	}

	for _, inp := range r.Inputs {
		_, err = tx.Exec(
			`INSERT INTO reading_inputs (reading_id, input_name, input_value)
			 VALUES ($1, $2, $3)`,
			readingID, inp.InputName, inp.InputValue,
		)
		if err != nil {
			return fmt.Errorf("insert input %s: %w", inp.InputName, err)
		}
	}

	return tx.Commit()
}

// InsertEvent inserts an alert or recovery event.
// A unique-constraint violation is treated as a no-op.
func (s *PostgresStore) InsertEvent(e models.Event) error {
	_, err := s.db.Exec(
		`INSERT INTO events
			(device_id, message_type, timestamp_ms, alert_type, severity,
			 threshold, reading_value, reading_name)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (device_id, timestamp_ms, message_type) DO NOTHING`,
		e.DeviceID, e.MessageType, e.TimestampMs, e.AlertType, e.Severity,
		e.Threshold, e.ReadingValue, nullableString(e.ReadingName),
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

// GetDevice returns the device with the given ID, or sql.ErrNoRows if not found.
func (s *PostgresStore) GetDevice(deviceID string) (models.Device, error) {
	row := s.db.QueryRow(
		`SELECT device_id, type, company, name, location, timezone,
		        floor_count, installed_date, reading_types, alert_thresholds
		 FROM devices WHERE device_id = $1`,
		deviceID,
	)
	return scanDevice(row)
}

// GetReadings returns all readings (with inputs) for a device in [from, to] epoch ms.
func (s *PostgresStore) GetReadings(deviceID string, from, to int64) ([]models.Reading, error) {
	rows, err := s.db.Query(
		`SELECT r.id, r.device_id, r.timestamp_ms,
		        ri.input_name, ri.input_value
		 FROM readings r
		 JOIN reading_inputs ri ON ri.reading_id = r.id
		 WHERE r.device_id = $1
		   AND r.timestamp_ms >= $2
		   AND r.timestamp_ms <= $3
		 ORDER BY r.timestamp_ms, ri.id`,
		deviceID, from, to,
	)
	if err != nil {
		return nil, fmt.Errorf("query readings: %w", err)
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {

		}
	}(rows)

	return collectReadings(rows)
}

// GetStats returns daily aggregates per input_name for a device, in device-local tz.
// motor_status is excluded from aggregation.
func (s *PostgresStore) GetStats(deviceID string) ([]models.StatsResponse, error) {
	rows, err := s.db.Query(
		`SELECT
		    to_char(
		        date_trunc('day', to_timestamp(r.timestamp_ms / 1000.0) AT TIME ZONE d.timezone),
		        'YYYY-MM-DD'
		    ) AS date,
		    ri.input_name,
		    AVG(ri.input_value),
		    MIN(ri.input_value),
		    MAX(ri.input_value),
		    COUNT(*)
		 FROM readings r
		 JOIN reading_inputs ri ON ri.reading_id = r.id
		 JOIN devices d ON d.device_id = r.device_id
		 WHERE r.device_id = $1
		   AND ri.input_name != 'motor_status'
		 GROUP BY date, ri.input_name
		 ORDER BY date, ri.input_name`,
		deviceID,
	)
	if err != nil {
		return nil, fmt.Errorf("query stats: %w", err)
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {

		}
	}(rows)

	var stats []models.StatsResponse
	for rows.Next() {
		var s models.StatsResponse
		if err := rows.Scan(&s.Date, &s.ReadingType, &s.Avg, &s.Min, &s.Max, &s.Count); err != nil {
			return nil, fmt.Errorf("scan stats: %w", err)
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// GetDeviceEvents returns alert/recovery events for a device, optionally filtered by severity.
func (s *PostgresStore) GetDeviceEvents(deviceID, severity string) ([]models.Event, error) {
	q := `SELECT id, device_id, message_type, timestamp_ms, alert_type, severity,
		         threshold, reading_value, reading_name
		  FROM events
		  WHERE device_id = $1`
	args := []any{deviceID}

	if severity != "" {
		q += ` AND severity = $2`
		args = append(args, severity)
	}
	q += ` ORDER BY timestamp_ms`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query device events: %w", err)
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {

		}
	}(rows)
	return scanEvents(rows)
}

// GetCompanyEvents returns alert/recovery events for all devices owned by a company,
// optionally filtered by severity.
func (s *PostgresStore) GetCompanyEvents(company, severity string) ([]models.Event, error) {
	q := `SELECT e.id, e.device_id, e.message_type, e.timestamp_ms,
		         e.alert_type, e.severity, e.threshold, e.reading_value, e.reading_name
		  FROM events e
		  JOIN devices d ON d.device_id = e.device_id
		  WHERE d.company = $1`
	args := []any{company}

	if severity != "" {
		q += ` AND e.severity = $2`
		args = append(args, severity)
	}
	q += ` ORDER BY e.timestamp_ms`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query company events: %w", err)
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {

		}
	}(rows)
	return scanEvents(rows)
}
