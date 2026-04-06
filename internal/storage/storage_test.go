package storage_test

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/sankirthkalahasti/knaq-take-home/internal/models"
	"github.com/sankirthkalahasti/knaq-take-home/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDB opens a connection to the test database and runs migrations.
// Tests are skipped if DATABASE_URL is not set.
func testDB(t *testing.T) *storage.PostgresStore {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration tests")
	}

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, db.Ping())

	require.NoError(t, storage.RunMigrations(db, "../../migrations"))

	store := storage.New(db)

	t.Cleanup(func() {
		// Truncate all tables in reverse FK order so each test starts clean.
		_, _ = db.Exec(`TRUNCATE events, reading_inputs, readings, devices RESTART IDENTITY CASCADE`)
		db.Close()
	})

	return store
}

// seedDevice inserts a device and returns it for use in tests.
func seedDevice(t *testing.T, store *storage.PostgresStore) models.Device {
	t.Helper()
	d := models.Device{
		DeviceID:      "ELV-001",
		Type:          "elevator",
		Company:       "Brookfield Properties",
		Name:          "Main Lobby Elevator #1",
		Location:      "One World Trade Center",
		Timezone:      "America/New_York",
		InstalledDate: "2023-06-15",
		ReadingTypes:  []string{"current", "frequency", "motor_status"},
		AlertThresholds: map[string]float64{
			"current_high":   180,
			"current_low":    5,
			"frequency_high": 65,
			"frequency_low":  55,
		},
	}
	require.NoError(t, store.UpsertDevices([]models.Device{d}))
	return d
}

// --- UpsertDevices ---

func TestUpsertDevices_Idempotent(t *testing.T) {
	store := testDB(t)
	d := seedDevice(t, store)

	// Second call must not error.
	err := store.UpsertDevices([]models.Device{d})
	assert.NoError(t, err)
}

func TestGetDevice_Found(t *testing.T) {
	store := testDB(t)
	seedDevice(t, store)

	d, err := store.GetDevice("ELV-001")
	require.NoError(t, err)
	assert.Equal(t, "ELV-001", d.DeviceID)
	assert.Equal(t, "Brookfield Properties", d.Company)
	assert.Equal(t, "America/New_York", d.Timezone)
	assert.Equal(t, float64(180), d.AlertThresholds["current_high"])
}

func TestGetDevice_NotFound(t *testing.T) {
	store := testDB(t)

	_, err := store.GetDevice("UNKNOWN")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

// --- InsertReading ---

func TestInsertReading_StoredCorrectly(t *testing.T) {
	store := testDB(t)
	seedDevice(t, store)

	r := models.Reading{
		DeviceID:    "ELV-001",
		TimestampMs: 1_000_000,
		Inputs: []models.ReadingInput{
			{InputName: "current", InputValue: 60.5},
			{InputName: "frequency", InputValue: 61.0},
		},
	}
	require.NoError(t, store.InsertReading(r))

	readings, err := store.GetReadings("ELV-001", 0, 2_000_000)
	require.NoError(t, err)
	require.Len(t, readings, 1)
	assert.Equal(t, int64(1_000_000), readings[0].TimestampMs)
	require.Len(t, readings[0].Inputs, 2)
	assert.Equal(t, "current", readings[0].Inputs[0].InputName)
	assert.Equal(t, 60.5, readings[0].Inputs[0].InputValue)
}

func TestInsertReading_DuplicateSilentlyIgnored(t *testing.T) {
	store := testDB(t)
	seedDevice(t, store)

	r := models.Reading{
		DeviceID:    "ELV-001",
		TimestampMs: 1_000_000,
		Inputs:      []models.ReadingInput{{InputName: "current", InputValue: 60.0}},
	}
	require.NoError(t, store.InsertReading(r))
	require.NoError(t, store.InsertReading(r)) // second insert must not error

	readings, err := store.GetReadings("ELV-001", 0, 2_000_000)
	require.NoError(t, err)
	assert.Len(t, readings, 1) // only one row
}

// --- GetReadings time range ---

func TestGetReadings_ReturnsOnlyWithinRange(t *testing.T) {
	store := testDB(t)
	seedDevice(t, store)

	for _, ts := range []int64{100, 500, 1000, 2000} {
		require.NoError(t, store.InsertReading(models.Reading{
			DeviceID:    "ELV-001",
			TimestampMs: ts,
			Inputs:      []models.ReadingInput{{InputName: "current", InputValue: 50}},
		}))
	}

	readings, err := store.GetReadings("ELV-001", 500, 1000)
	require.NoError(t, err)
	require.Len(t, readings, 2)
	assert.Equal(t, int64(500), readings[0].TimestampMs)
	assert.Equal(t, int64(1000), readings[1].TimestampMs)
}

// --- GetStats ---

func TestGetStats_CorrectAggregates(t *testing.T) {
	store := testDB(t)
	seedDevice(t, store)

	// Two readings on the same day (2026-02-11 UTC), current values 60 and 80.
	for i, ts := range []int64{1_770_800_000_000, 1_770_803_600_000} {
		v := float64(60 + 20*i) // 60, 80
		require.NoError(t, store.InsertReading(models.Reading{
			DeviceID:    "ELV-001",
			TimestampMs: ts,
			Inputs:      []models.ReadingInput{{InputName: "current", InputValue: v}},
		}))
	}

	stats, err := store.GetStats("ELV-001")
	require.NoError(t, err)
	require.Len(t, stats, 1)
	s := stats[0]
	assert.Equal(t, "current", s.ReadingType)
	assert.Equal(t, float64(70), s.Avg)
	assert.Equal(t, float64(60), s.Min)
	assert.Equal(t, float64(80), s.Max)
	assert.Equal(t, 2, s.Count)
}

func TestGetStats_ExcludesMotorStatus(t *testing.T) {
	store := testDB(t)
	seedDevice(t, store)

	require.NoError(t, store.InsertReading(models.Reading{
		DeviceID:    "ELV-001",
		TimestampMs: 1_770_800_000_000,
		Inputs: []models.ReadingInput{
			{InputName: "current", InputValue: 60},
			{InputName: "motor_status", InputValue: 1},
		},
	}))

	stats, err := store.GetStats("ELV-001")
	require.NoError(t, err)
	for _, s := range stats {
		assert.NotEqual(t, "motor_status", s.ReadingType)
	}
}

// --- InsertEvent / GetDeviceEvents ---

func TestInsertEvent_DuplicateSilentlyIgnored(t *testing.T) {
	store := testDB(t)
	seedDevice(t, store)

	e := models.Event{
		DeviceID:    "ELV-001",
		MessageType: "alert",
		TimestampMs: 1_000_000,
		AlertType:   "door_fault",
		Severity:    "warning",
	}
	require.NoError(t, store.InsertEvent(e))
	require.NoError(t, store.InsertEvent(e))

	events, err := store.GetDeviceEvents("ELV-001", "")
	require.NoError(t, err)
	assert.Len(t, events, 1)
}

func TestGetDeviceEvents_FiltersBySeverity(t *testing.T) {
	store := testDB(t)
	seedDevice(t, store)

	require.NoError(t, store.InsertEvent(models.Event{
		DeviceID: "ELV-001", MessageType: "alert", TimestampMs: 1000,
		AlertType: "door_fault", Severity: "warning",
	}))
	require.NoError(t, store.InsertEvent(models.Event{
		DeviceID: "ELV-001", MessageType: "alert", TimestampMs: 2000,
		AlertType: "vibration_anomaly", Severity: "critical",
	}))

	events, err := store.GetDeviceEvents("ELV-001", "critical")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "critical", events[0].Severity)
}

func TestGetDeviceEvents_ThresholdFieldsRoundTrip(t *testing.T) {
	store := testDB(t)
	seedDevice(t, store)

	threshold := float64(180)
	value := float64(200)
	e := models.Event{
		DeviceID:     "ELV-001",
		MessageType:  "alert",
		TimestampMs:  1000,
		AlertType:    "current_high",
		Severity:     "warning",
		Threshold:    &threshold,
		ReadingValue: &value,
		ReadingName:  "current",
	}
	require.NoError(t, store.InsertEvent(e))

	events, err := store.GetDeviceEvents("ELV-001", "")
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Threshold)
	assert.Equal(t, threshold, *events[0].Threshold)
	require.NotNil(t, events[0].ReadingValue)
	assert.Equal(t, value, *events[0].ReadingValue)
	assert.Equal(t, "current", events[0].ReadingName)
}

// --- GetCompanyEvents ---

func TestGetCompanyEvents_OnlyReturnsCompanyDevices(t *testing.T) {
	store := testDB(t)

	brookfield := models.Device{
		DeviceID: "ELV-001", Type: "elevator", Company: "Brookfield Properties",
		Name: "A", Location: "B", Timezone: "UTC", InstalledDate: "2023-01-01",
		ReadingTypes: []string{"current"}, AlertThresholds: map[string]float64{},
	}
	hines := models.Device{
		DeviceID: "ELV-002", Type: "elevator", Company: "Hines",
		Name: "C", Location: "D", Timezone: "UTC", InstalledDate: "2023-01-01",
		ReadingTypes: []string{"current"}, AlertThresholds: map[string]float64{},
	}
	require.NoError(t, store.UpsertDevices([]models.Device{brookfield, hines}))

	require.NoError(t, store.InsertEvent(models.Event{
		DeviceID: "ELV-001", MessageType: "alert", TimestampMs: 1000,
		AlertType: "door_fault", Severity: "warning",
	}))
	require.NoError(t, store.InsertEvent(models.Event{
		DeviceID: "ELV-002", MessageType: "alert", TimestampMs: 2000,
		AlertType: "door_fault", Severity: "warning",
	}))

	events, err := store.GetCompanyEvents("Brookfield Properties", "")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "ELV-001", events[0].DeviceID)
}

func TestGetCompanyEvents_FiltersBySeverity(t *testing.T) {
	store := testDB(t)
	seedDevice(t, store)

	require.NoError(t, store.InsertEvent(models.Event{
		DeviceID: "ELV-001", MessageType: "alert", TimestampMs: 1000,
		AlertType: "door_fault", Severity: "warning",
	}))
	require.NoError(t, store.InsertEvent(models.Event{
		DeviceID: "ELV-001", MessageType: "alert", TimestampMs: 2000,
		AlertType: "vibration_anomaly", Severity: "critical",
	}))

	events, err := store.GetCompanyEvents("Brookfield Properties", "warning")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "warning", events[0].Severity)
}
