package ingest

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/sankirthkalahasti/knaq-take-home/internal/models"
)

// IngestError records a parse failure for a single message.
type Error struct {
	Index  int
	Reason string
}

func (e Error) Error() string {
	return fmt.Sprintf("message[%d]: %s", e.Index, e.Reason)
}

// rawJSON is the intermediate shape used to decode a single message from JSON
// before normalisation. Fields are kept as json.RawMessage so we can handle
// the timestamp being either a number or an ISO 8601 string.
type rawJSON struct {
	DeviceID     *string         `json:"device_id"`
	MessageType  *string         `json:"message_type"`
	Timestamp    json.RawMessage `json:"timestamp"`
	Inputs       []rawInputJSON  `json:"inputs"`
	AlertType    string          `json:"alert_type"`
	Severity     string          `json:"severity"`
	Threshold    *float64        `json:"threshold"`
	ReadingValue *float64        `json:"reading_value"`
	ReadingName  string          `json:"reading_name"`
}

type rawInputJSON struct {
	InputName  string          `json:"input_name"`
	InputValue json.RawMessage `json:"input_value"` // may be number or quoted string
}

// Parse reads sensor_messages.json at path, normalises each message into a
// RawMessage, and returns the valid messages alongside any per-message errors.
// A failure on one message never aborts processing of the rest.
func Parse(path string) (msgs []models.RawMessage, errs []Error) {
	data, err := os.ReadFile(path)
	if err != nil {
		errs = append(errs, Error{Index: -1, Reason: fmt.Sprintf("cannot read file: %v", err)})
		return
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		errs = append(errs, Error{Index: -1, Reason: fmt.Sprintf("cannot parse JSON array: %v", err)})
		return
	}

	for i, item := range raw {
		msg, err := parseOne(i, item)
		if err != nil {
			slog.Warn("ingest: rejected message", "index", i, "reason", err.Error())
			errs = append(errs, Error{Index: i, Reason: err.Error()})
			continue
		}
		msgs = append(msgs, msg)
	}

	return msgs, errs
}

// parseOne parses and normalises a single raw JSON message.
// It is wrapped in a recover so a panic on one message cannot crash the pipeline.
func parseOne(i int, data json.RawMessage) (msg models.RawMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("unexpected panic: %v", r)
		}
	}()

	var r rawJSON
	if jsonErr := json.Unmarshal(data, &r); jsonErr != nil {
		return msg, fmt.Errorf("invalid JSON: %w", jsonErr)
	}

	// --- required: device_id ---
	if r.DeviceID == nil || *r.DeviceID == "" {
		return msg, fmt.Errorf("missing device_id")
	}

	// --- required: message_type ---
	if r.MessageType == nil || *r.MessageType == "" {
		return msg, fmt.Errorf("missing message_type")
	}

	// --- required: timestamp (int64 epoch ms or ISO 8601 string) ---
	tsMs, tsErr := parseTimestamp(r.Timestamp)
	if tsErr != nil {
		return msg, fmt.Errorf("unparseable timestamp: %w", tsErr)
	}

	msg = models.RawMessage{
		DeviceID:     *r.DeviceID,
		MessageType:  *r.MessageType,
		TimestampMs:  tsMs,
		AlertType:    r.AlertType,
		Severity:     r.Severity,
		Threshold:    r.Threshold,
		ReadingValue: r.ReadingValue,
		ReadingName:  r.ReadingName,
	}

	for _, inp := range r.Inputs {
		val, err := parseInputValue(inp.InputValue)
		if err != nil {
			return msg, fmt.Errorf("input %q has unparseable value: %w", inp.InputName, err)
		}
		msg.Inputs = append(msg.Inputs, models.RawInput{
			Name:  inp.InputName,
			Value: val,
		})
	}

	return msg, nil
}

// parseTimestamp handles two valid shapes:
//   - JSON number  → interpreted as epoch milliseconds
//   - JSON string  → parsed as ISO 8601 / RFC 3339, then converted to epoch ms
func parseTimestamp(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("timestamp is absent")
	}

	// Reject explicit null
	if string(raw) == "null" {
		return 0, fmt.Errorf("timestamp is null")
	}

	// Try numeric first (most messages)
	var ms int64
	if err := json.Unmarshal(raw, &ms); err == nil {
		return ms, nil
	}

	// Try string (EC-001: "2026-02-11T10:30:00Z")
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, fmt.Errorf("not a number or string")
	}

	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t.UnixMilli(), nil
		}
	}

	return 0, fmt.Errorf("unrecognised timestamp format: %q", s)
}

// parseInputValue handles input_value being either a JSON number or a quoted
// numeric string (e.g. "85.5"). Returns an error if the value cannot be
// interpreted as a float64.
func parseInputValue(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("value is absent")
	}

	// Try number first (common case)
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f, nil
	}

	// Try quoted string containing a number (EC: "85.5")
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, fmt.Errorf("not a number or string")
	}

	var parsed float64
	if _, err := fmt.Sscanf(s, "%f", &parsed); err != nil {
		return 0, fmt.Errorf("cannot parse %q as float64", s)
	}
	return parsed, nil
}
