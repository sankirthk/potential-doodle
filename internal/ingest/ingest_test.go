package ingest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTemp writes JSON content to a temp file and returns the path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "messages*.json")
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

func TestParse_ValidReading(t *testing.T) {
	path := writeTemp(t, `[
		{
			"device_id": "ELV-001",
			"message_type": "reading",
			"timestamp": 1770737458000,
			"inputs": [
				{"input_name": "current",   "input_value": 58.94},
				{"input_name": "frequency", "input_value": 61.99}
			]
		}
	]`)

	msgs, errs := Parse(path)

	require.Empty(t, errs)
	require.Len(t, msgs, 1)
	assert.Equal(t, "ELV-001", msgs[0].DeviceID)
	assert.Equal(t, "reading", msgs[0].MessageType)
	assert.Equal(t, int64(1770737458000), msgs[0].TimestampMs)
	require.Len(t, msgs[0].Inputs, 2)
	assert.Equal(t, "current", msgs[0].Inputs[0].Name)
	assert.Equal(t, 58.94, msgs[0].Inputs[0].Value)
	assert.Equal(t, "frequency", msgs[0].Inputs[1].Name)
}

func TestParse_ValidAlert(t *testing.T) {
	path := writeTemp(t, `[
		{
			"device_id": "CMP-001",
			"message_type": "alert",
			"timestamp": 1770924235000,
			"alert_type": "high_temperature",
			"severity": "critical",
			"threshold": 130,
			"reading_value": 136.51,
			"reading_name": "temperature"
		}
	]`)

	msgs, errs := Parse(path)

	require.Empty(t, errs)
	require.Len(t, msgs, 1)
	assert.Equal(t, "CMP-001", msgs[0].DeviceID)
	assert.Equal(t, "alert", msgs[0].MessageType)
	assert.Equal(t, "high_temperature", msgs[0].AlertType)
	assert.Equal(t, "critical", msgs[0].Severity)
	require.NotNil(t, msgs[0].Threshold)
	assert.Equal(t, float64(130), *msgs[0].Threshold)
	require.NotNil(t, msgs[0].ReadingValue)
	assert.Equal(t, 136.51, *msgs[0].ReadingValue)
	assert.Equal(t, "temperature", msgs[0].ReadingName)
}

func TestParse_ValidRecovery(t *testing.T) {
	path := writeTemp(t, `[
		{
			"device_id": "CMP-001",
			"message_type": "recovery",
			"timestamp": 1770927835000,
			"alert_type": "high_temperature",
			"severity": "critical",
			"threshold": 130,
			"reading_value": 118.42,
			"reading_name": "temperature"
		}
	]`)

	msgs, errs := Parse(path)

	require.Empty(t, errs)
	require.Len(t, msgs, 1)
	assert.Equal(t, "recovery", msgs[0].MessageType)
	assert.Equal(t, "high_temperature", msgs[0].AlertType)
}

func TestParse_NonThresholdAlert(t *testing.T) {
	path := writeTemp(t, `[
		{
			"device_id": "CMP-001",
			"message_type": "recovery",
			"timestamp": 1770814564270,
			"alert_type": "vibration_anomaly",
			"severity": "warning"
		}
	]`)

	msgs, errs := Parse(path)

	require.Empty(t, errs)
	require.Len(t, msgs, 1)
	assert.Nil(t, msgs[0].Threshold)
	assert.Nil(t, msgs[0].ReadingValue)
	assert.Equal(t, "", msgs[0].ReadingName)
}

func TestParse_ISOTimestamp(t *testing.T) {
	// EC-001: timestamp is an ISO 8601 string instead of epoch ms integer
	path := writeTemp(t, `[
		{
			"device_id": "ELV-001",
			"message_type": "reading",
			"timestamp": "2026-02-11T10:30:00Z",
			"inputs": [
				{"input_name": "current",   "input_value": 45.0},
				{"input_name": "frequency", "input_value": 60.0}
			]
		}
	]`)

	msgs, errs := Parse(path)

	require.Empty(t, errs)
	require.Len(t, msgs, 1)
	// 2026-02-11T10:30:00Z = 1770805800000 ms
	assert.Equal(t, int64(1770805800000), msgs[0].TimestampMs)
}

func TestParse_MissingMessageType(t *testing.T) {
	// EC-002: message_type field absent
	path := writeTemp(t, `[
		{
			"device_id": "CMP-002",
			"timestamp": 1770888600000,
			"inputs": [{"input_name": "temperature", "input_value": 80.1}]
		}
	]`)

	msgs, errs := Parse(path)

	assert.Empty(t, msgs)
	require.Len(t, errs, 1)
	assert.Equal(t, 0, errs[0].Index)
	assert.Contains(t, errs[0].Reason, "missing message_type")
}

func TestParse_MissingDeviceID(t *testing.T) {
	// EC-003: device_id field absent
	path := writeTemp(t, `[
		{
			"message_type": "reading",
			"timestamp": 1770805800000,
			"inputs": [{"input_name": "temperature", "input_value": 72.5}]
		}
	]`)

	msgs, errs := Parse(path)

	assert.Empty(t, msgs)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Reason, "missing device_id")
}

func TestParse_EmptyInputs(t *testing.T) {
	// EC-005: inputs array is present but empty — caught later in validate,
	// but ingest should still parse and return the message (validate rejects it)
	path := writeTemp(t, `[
		{
			"device_id": "ELV-002",
			"message_type": "reading",
			"timestamp": 1770818400000,
			"inputs": []
		}
	]`)

	msgs, errs := Parse(path)

	// Ingest accepts it — empty inputs is a validation concern, not a parse concern
	require.Empty(t, errs)
	require.Len(t, msgs, 1)
	assert.Empty(t, msgs[0].Inputs)
}

func TestParse_ValidMessageAfterMalformed(t *testing.T) {
	// A bad message at index 0 must not prevent index 1 from being returned
	path := writeTemp(t, `[
		{
			"message_type": "reading",
			"timestamp": 1770805800000,
			"inputs": [{"input_name": "temperature", "input_value": 72.5}]
		},
		{
			"device_id": "ELV-001",
			"message_type": "reading",
			"timestamp": 1770737458000,
			"inputs": [{"input_name": "current", "input_value": 58.94}]
		}
	]`)

	msgs, errs := Parse(path)

	require.Len(t, errs, 1)
	assert.Equal(t, 0, errs[0].Index)
	require.Len(t, msgs, 1)
	assert.Equal(t, "ELV-001", msgs[0].DeviceID)
}

func TestParse_StringInputValue(t *testing.T) {
	// Real data edge case: input_value is a quoted string "85.5" instead of number
	path := writeTemp(t, `[
		{
			"device_id": "CMP-004",
			"message_type": "reading",
			"timestamp": 1770823200000,
			"inputs": [
				{"input_name": "temperature", "input_value": "85.5"}
			]
		}
	]`)

	msgs, errs := Parse(path)

	require.Empty(t, errs)
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Inputs, 1)
	assert.Equal(t, "temperature", msgs[0].Inputs[0].Name)
	assert.Equal(t, 85.5, msgs[0].Inputs[0].Value)
}

func TestParse_FileNotFound(t *testing.T) {
	msgs, errs := Parse("/nonexistent/path/messages.json")

	assert.Empty(t, msgs)
	require.Len(t, errs, 1)
	assert.Equal(t, -1, errs[0].Index)
	assert.Contains(t, errs[0].Reason, "cannot read file")
}

func TestParse_InvalidJSON(t *testing.T) {
	path := writeTemp(t, `not valid json`)

	msgs, errs := Parse(path)

	assert.Empty(t, msgs)
	require.Len(t, errs, 1)
	assert.Equal(t, -1, errs[0].Index)
	assert.Contains(t, errs[0].Reason, "cannot parse JSON array")
}

func TestParse_RealDataFile(t *testing.T) {
	// Smoke test against the actual sensor_messages.json
	path := filepath.Join("..", "..", "sensor_messages.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("sensor_messages.json not found")
	}

	msgs, errs := Parse(path)

	// From our analysis: 816 total, 5 malformed (missing device_id, missing
	// message_type, unknown device, ISO timestamp parses fine)
	assert.NotEmpty(t, msgs)
	// Errors should be a small fraction — we know of at least 2 hard rejects
	// (missing device_id, missing message_type) and the ISO timestamp is valid
	t.Logf("parsed %d messages, %d errors", len(msgs), len(errs))

	// Sanity: ISO timestamp message (ELV-001, "2026-02-11T10:30:00Z") must parse
	var isoFound bool
	for _, m := range msgs {
		if m.DeviceID == "ELV-001" && m.TimestampMs == 1770805800000 {
			isoFound = true
			break
		}
	}
	assert.True(t, isoFound, "ISO 8601 timestamp message should be parsed and converted")

	// All returned messages must have non-empty device_id, message_type, timestamp
	for _, m := range msgs {
		assert.NotEmpty(t, m.DeviceID)
		assert.NotEmpty(t, m.MessageType)
		assert.NotZero(t, m.TimestampMs)
	}
}

func TestParseTimestamp_EpochMs(t *testing.T) {
	raw := json.RawMessage(`1770737458000`)
	ms, err := parseTimestamp(raw)
	require.NoError(t, err)
	assert.Equal(t, int64(1770737458000), ms)
}

func TestParseTimestamp_ISOString(t *testing.T) {
	raw := json.RawMessage(`"2026-02-11T10:30:00Z"`)
	ms, err := parseTimestamp(raw)
	require.NoError(t, err)
	assert.Equal(t, int64(1770805800000), ms)
}

func TestParseTimestamp_Invalid(t *testing.T) {
	raw := json.RawMessage(`"not-a-date"`)
	_, err := parseTimestamp(raw)
	assert.Error(t, err)
}

func TestParseTimestamp_Null(t *testing.T) {
	_, err := parseTimestamp(json.RawMessage(`null`))
	assert.Error(t, err)
}

func TestParseTimestamp_Empty(t *testing.T) {
	_, err := parseTimestamp(json.RawMessage(``))
	assert.Error(t, err)
}
