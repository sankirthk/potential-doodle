package validate

import (
	"testing"

	"github.com/sankirthkalahasti/knaq-take-home/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDevice returns a standard elevator device for use in tests.
func testDevice() models.Device {
	return models.Device{
		DeviceID:     "ELV-001",
		Type:         "elevator",
		Company:      "Brookfield Properties",
		Timezone:     "America/New_York",
		ReadingTypes: []string{"current", "frequency", "motor_status"},
		AlertThresholds: map[string]float64{
			"current_high":   180,
			"current_low":    5,
			"frequency_high": 65,
			"frequency_low":  55,
		},
	}
}

// testCompressor returns a compressor device with a temperature threshold.
func testCompressor() models.Device {
	return models.Device{
		DeviceID:     "CMP-001",
		Type:         "compressor",
		Company:      "Brookfield Properties",
		Timezone:     "America/New_York",
		ReadingTypes: []string{"current", "frequency", "temperature", "motor_status"},
		AlertThresholds: map[string]float64{
			"current_high":     95,
			"current_low":      5,
			"frequency_high":   65,
			"frequency_low":    55,
			"temperature_high": 130,
			"temperature_low":  -5,
		},
	}
}

func registry(devices ...models.Device) map[string]models.Device {
	m := make(map[string]models.Device, len(devices))
	for _, d := range devices {
		m[d.DeviceID] = d
	}
	return m
}

func rawReading(deviceID string, ts int64, inputs ...models.RawInput) models.RawMessage {
	return models.RawMessage{
		DeviceID:    deviceID,
		MessageType: "reading",
		TimestampMs: ts,
		Inputs:      inputs,
	}
}

func inp(name string, value float64) models.RawInput {
	return models.RawInput{Name: name, Value: value}
}

// --- Unknown device ---

func TestProcess_UnknownDevice(t *testing.T) {
	v := New(registry(testDevice()))
	msg := rawReading("ELV-999", 1000, inp("current", 50))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, batch.Readings)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Reason, "unknown device_id")
}

// --- Deduplication ---

func TestProcess_DuplicateDiscarded_FirstRetained(t *testing.T) {
	v := New(registry(testDevice()))
	msg := rawReading("ELV-001", 1000, inp("current", 50))

	batch, errs := v.Process([]models.RawMessage{msg, msg})

	require.Len(t, batch.Readings, 1)
	assert.Equal(t, int64(1000), batch.Readings[0].TimestampMs)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Reason, "duplicate")
}

func TestProcess_SameTimestamp_DifferentType_NotDuplicate(t *testing.T) {
	v := New(registry(testDevice()))
	reading := rawReading("ELV-001", 1000, inp("current", 50))
	alert := models.RawMessage{
		DeviceID:    "ELV-001",
		MessageType: "alert",
		TimestampMs: 1000,
		AlertType:   "door_fault",
		Severity:    "warning",
	}

	batch, errs := v.Process([]models.RawMessage{reading, alert})

	assert.Empty(t, errs)
	assert.Len(t, batch.Readings, 1)
	assert.Len(t, batch.Events, 1)
}

// --- Reading validation ---

func TestProcess_ValidReading_StoredCorrectly(t *testing.T) {
	v := New(registry(testDevice()))
	msg := rawReading("ELV-001", 1000, inp("current", 60), inp("frequency", 60))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	require.Len(t, batch.Readings, 1)
	r := batch.Readings[0]
	assert.Equal(t, "ELV-001", r.DeviceID)
	assert.Equal(t, int64(1000), r.TimestampMs)
	require.Len(t, r.Inputs, 2)
	assert.Equal(t, "current", r.Inputs[0].InputName)
	assert.Equal(t, 60.0, r.Inputs[0].InputValue)
}

func TestProcess_EmptyInputs_Rejected(t *testing.T) {
	v := New(registry(testDevice()))
	msg := rawReading("ELV-001", 1000)

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, batch.Readings)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Reason, "no inputs")
}

func TestProcess_UnrecognizedInputSkipped_RestStored(t *testing.T) {
	v := New(registry(testDevice()))
	// "vibration" is not in ELV-001's reading_types
	msg := rawReading("ELV-001", 1000, inp("current", 60), inp("vibration", 3.5))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	require.Len(t, batch.Readings, 1)
	require.Len(t, batch.Readings[0].Inputs, 1)
	assert.Equal(t, "current", batch.Readings[0].Inputs[0].InputName)
}

func TestProcess_PartialInputs_Accepted(t *testing.T) {
	// Device has current, frequency, motor_status — message only sends current
	v := New(registry(testDevice()))
	msg := rawReading("ELV-001", 1000, inp("current", 60))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	require.Len(t, batch.Readings, 1)
	assert.Len(t, batch.Readings[0].Inputs, 1)
}

// --- Motor status ---

func TestProcess_MotorStatus_One_Stored(t *testing.T) {
	v := New(registry(testDevice()))
	msg := rawReading("ELV-001", 1000, inp("motor_status", 1))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	require.Len(t, batch.Readings, 1)
	require.Len(t, batch.Readings[0].Inputs, 1)
	assert.Equal(t, "motor_status", batch.Readings[0].Inputs[0].InputName)
	assert.Equal(t, 1.0, batch.Readings[0].Inputs[0].InputValue)
}

func TestProcess_MotorStatus_Zero_Stored(t *testing.T) {
	v := New(registry(testDevice()))
	msg := rawReading("ELV-001", 1000, inp("motor_status", 0))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	require.Len(t, batch.Readings[0].Inputs, 1)
	assert.Equal(t, 0.0, batch.Readings[0].Inputs[0].InputValue)
}

func TestProcess_MotorStatus_InvalidValue_Skipped(t *testing.T) {
	v := New(registry(testDevice()))
	// 2 is invalid; current is valid — reading should still be stored without motor_status
	msg := rawReading("ELV-001", 1000, inp("motor_status", 2), inp("current", 60))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	require.Len(t, batch.Readings, 1)
	require.Len(t, batch.Readings[0].Inputs, 1)
	assert.Equal(t, "current", batch.Readings[0].Inputs[0].InputName)
}

func TestProcess_MotorStatus_NotCheckedAgainstThresholds(t *testing.T) {
	// motor_status=1 should not trigger a threshold event even if a "motor_status_high" key existed
	v := New(registry(testDevice()))
	msg := rawReading("ELV-001", 1000, inp("motor_status", 1))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	assert.Empty(t, batch.Events) // no threshold event
}

// --- Threshold breach ---

func TestProcess_AboveHighThreshold_EventCreated(t *testing.T) {
	v := New(registry(testDevice()))
	// current_high = 180; value 200 > 180
	msg := rawReading("ELV-001", 1000, inp("current", 200))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	require.Len(t, batch.Events, 1)
	e := batch.Events[0]
	assert.Equal(t, "ELV-001", e.DeviceID)
	assert.Equal(t, "alert", e.MessageType)
	assert.Equal(t, "current_high", e.AlertType)
	require.NotNil(t, e.Threshold)
	assert.Equal(t, 180.0, *e.Threshold)
	require.NotNil(t, e.ReadingValue)
	assert.Equal(t, 200.0, *e.ReadingValue)
	assert.Equal(t, "current", e.ReadingName)
}

func TestProcess_BelowLowThreshold_EventCreated(t *testing.T) {
	v := New(registry(testDevice()))
	// current_low = 5; value 2 < 5
	msg := rawReading("ELV-001", 1000, inp("current", 2))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	require.Len(t, batch.Events, 1)
	e := batch.Events[0]
	assert.Equal(t, "current_low", e.AlertType)
	require.NotNil(t, e.Threshold)
	assert.Equal(t, 5.0, *e.Threshold)
}

func TestProcess_WithinThresholds_NoEvent(t *testing.T) {
	v := New(registry(testDevice()))
	// current_low=5, current_high=180; value 60 is within bounds
	msg := rawReading("ELV-001", 1000, inp("current", 60))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	assert.Empty(t, batch.Events)
	assert.Len(t, batch.Readings, 1)
}

func TestProcess_MultipleThresholdBreaches_MultipleEvents(t *testing.T) {
	v := New(registry(testCompressor()))
	// current > 95 and temperature > 130
	msg := rawReading("CMP-001", 1000, inp("current", 100), inp("temperature", 140))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	assert.Len(t, batch.Events, 2)
}

func TestProcess_InputWithNoThresholdDefined_NoEvent(t *testing.T) {
	// temperature has no threshold on the elevator device
	v := New(registry(testDevice()))
	// add temperature to reading_types so it's not rejected as unknown
	d := testDevice()
	d.ReadingTypes = append(d.ReadingTypes, "temperature")
	v = New(registry(d))
	msg := rawReading("ELV-001", 1000, inp("temperature", 999))

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	assert.Empty(t, batch.Events)
	assert.Len(t, batch.Readings[0].Inputs, 1)
}

// --- Alert / recovery ---

func TestProcess_ValidAlert_Stored(t *testing.T) {
	v := New(registry(testDevice()))
	msg := models.RawMessage{
		DeviceID:    "ELV-001",
		MessageType: "alert",
		TimestampMs: 1000,
		AlertType:   "door_fault",
		Severity:    "critical",
	}

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	require.Len(t, batch.Events, 1)
	e := batch.Events[0]
	assert.Equal(t, "alert", e.MessageType)
	assert.Equal(t, "door_fault", e.AlertType)
	assert.Equal(t, "critical", e.Severity)
	assert.Nil(t, e.Threshold)
	assert.Nil(t, e.ReadingValue)
}

func TestProcess_ValidRecovery_NoThresholdFields_Accepted(t *testing.T) {
	v := New(registry(testDevice()))
	msg := models.RawMessage{
		DeviceID:    "ELV-001",
		MessageType: "recovery",
		TimestampMs: 1000,
		AlertType:   "vibration_anomaly",
		Severity:    "warning",
	}

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, errs)
	require.Len(t, batch.Events, 1)
	assert.Equal(t, "recovery", batch.Events[0].MessageType)
}

func TestProcess_AlertMissingSeverity_Rejected(t *testing.T) {
	v := New(registry(testDevice()))
	msg := models.RawMessage{
		DeviceID:    "ELV-001",
		MessageType: "alert",
		TimestampMs: 1000,
		AlertType:   "door_fault",
		// Severity intentionally omitted
	}

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, batch.Events)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Reason, "missing severity")
}

func TestProcess_AlertMissingAlertType_Rejected(t *testing.T) {
	v := New(registry(testDevice()))
	msg := models.RawMessage{
		DeviceID:    "ELV-001",
		MessageType: "alert",
		TimestampMs: 1000,
		Severity:    "critical",
		// AlertType intentionally omitted
	}

	batch, errs := v.Process([]models.RawMessage{msg})

	assert.Empty(t, batch.Events)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Reason, "missing alert_type")
}

// --- Mixed batch ---

func TestProcess_BadMessageDoesNotBlockRest(t *testing.T) {
	v := New(registry(testDevice()))
	msgs := []models.RawMessage{
		{DeviceID: "ELV-999", MessageType: "reading", TimestampMs: 1000, Inputs: []models.RawInput{inp("current", 50)}},
		rawReading("ELV-001", 2000, inp("current", 60)),
	}

	batch, errs := v.Process(msgs)

	require.Len(t, errs, 1)
	require.Len(t, batch.Readings, 1)
	assert.Equal(t, "ELV-001", batch.Readings[0].DeviceID)
}

func TestProcess_ThresholdEventTimestampMatchesReading(t *testing.T) {
	v := New(registry(testDevice()))
	msg := rawReading("ELV-001", 9999, inp("current", 200))

	batch, _ := v.Process([]models.RawMessage{msg})

	require.Len(t, batch.Events, 1)
	assert.Equal(t, int64(9999), batch.Events[0].TimestampMs)
}
