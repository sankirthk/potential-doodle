package validate

import (
	"fmt"
	"log/slog"

	"github.com/sankirthkalahasti/knaq-take-home/internal/models"
)

// ValidationError records a validation failure for a single message.
type ValidationError struct {
	DeviceID    string
	TimestampMs int64
	Reason      string
}

func (e ValidationError) Error() string {
	return e.Reason
}

// ValidatedBatch holds all successfully validated data ready for storage.
type ValidatedBatch struct {
	Readings []models.Reading
	Events   []models.Event
}

// Validator validates and deduplicates raw messages against the device registry.
type Validator struct {
	devices map[string]models.Device
	seen    map[string]struct{}
}

// New creates a Validator loaded with the given device registry.
func New(devices map[string]models.Device) *Validator {
	return &Validator{
		devices: devices,
		seen:    make(map[string]struct{}),
	}
}

// Process validates a slice of raw messages and returns a ValidatedBatch
// and a list of per-message validation errors.
func (v *Validator) Process(msgs []models.RawMessage) (ValidatedBatch, []ValidationError) {
	var batch ValidatedBatch
	var errs []ValidationError

	for _, msg := range msgs {
		// Unknown device_id — cannot validate reading types or thresholds.
		device, ok := v.devices[msg.DeviceID]
		if !ok {
			ve := ValidationError{
				DeviceID:    msg.DeviceID,
				TimestampMs: msg.TimestampMs,
				Reason:      fmt.Sprintf("unknown device_id: %q", msg.DeviceID),
			}
			slog.Warn("validate: rejected", "device_id", msg.DeviceID, "reason", ve.Reason)
			errs = append(errs, ve)
			continue
		}

		// Dedup on composite key device_id:timestamp_ms:message_type.
		key := fmt.Sprintf("%s:%d:%s", msg.DeviceID, msg.TimestampMs, msg.MessageType)
		if _, seen := v.seen[key]; seen {
			slog.Warn("validate: duplicate", "device_id", msg.DeviceID, "timestamp_ms", msg.TimestampMs, "type", msg.MessageType)
			errs = append(errs, ValidationError{
				DeviceID:    msg.DeviceID,
				TimestampMs: msg.TimestampMs,
				Reason:      "duplicate message",
			})
			continue
		}
		v.seen[key] = struct{}{}

		switch msg.MessageType {
		case "reading":
			reading, thresholdEvents, ve := processReading(msg, device)
			if ve != nil {
				errs = append(errs, *ve)
				continue
			}
			batch.Readings = append(batch.Readings, reading)
			batch.Events = append(batch.Events, thresholdEvents...)

		case "alert", "recovery":
			event, ve := processEvent(msg)
			if ve != nil {
				errs = append(errs, *ve)
				continue
			}
			batch.Events = append(batch.Events, event)

		default:
			ve := ValidationError{
				DeviceID:    msg.DeviceID,
				TimestampMs: msg.TimestampMs,
				Reason:      fmt.Sprintf("unknown message_type: %q", msg.MessageType),
			}
			slog.Warn("validate: rejected", "device_id", msg.DeviceID, "reason", ve.Reason)
			errs = append(errs, ve)
		}
	}

	return batch, errs
}

// processReading validates a reading message. It returns the Reading, any
// threshold-breach Events derived from the inputs, and an error if the whole
// message should be discarded.
func processReading(msg models.RawMessage, device models.Device) (models.Reading, []models.Event, *ValidationError) {
	if len(msg.Inputs) == 0 {
		ve := &ValidationError{
			DeviceID:    msg.DeviceID,
			TimestampMs: msg.TimestampMs,
			Reason:      "reading has no inputs",
		}
		slog.Warn("validate: rejected", "device_id", msg.DeviceID, "reason", ve.Reason)
		return models.Reading{}, nil, ve
	}

	// Build a fast lookup for valid input names for this device.
	validInputs := make(map[string]struct{}, len(device.ReadingTypes))
	for _, rt := range device.ReadingTypes {
		validInputs[rt] = struct{}{}
	}

	reading := models.Reading{
		DeviceID:    msg.DeviceID,
		TimestampMs: msg.TimestampMs,
	}
	var thresholdEvents []models.Event

	for _, inp := range msg.Inputs {
		if _, ok := validInputs[inp.Name]; !ok {
			slog.Warn("validate: skipping unrecognized input", "device_id", msg.DeviceID, "input_name", inp.Name)
			continue
		}

		if inp.Name == "motor_status" {
			if inp.Value != 0 && inp.Value != 1 {
				slog.Warn("validate: skipping invalid motor_status", "device_id", msg.DeviceID, "value", inp.Value)
				continue
			}
		} else {
			if event, breached := checkThreshold(msg, inp, device); breached {
				thresholdEvents = append(thresholdEvents, event)
			}
		}

		reading.Inputs = append(reading.Inputs, models.ReadingInput{
			InputName:  inp.Name,
			InputValue: inp.Value,
		})
	}

	return reading, thresholdEvents, nil
}

// checkThreshold checks inp against the device's alert_thresholds. Returns an
// Event and true if the value is outside bounds; otherwise returns false.
func checkThreshold(msg models.RawMessage, inp models.RawInput, device models.Device) (models.Event, bool) {
	highKey := inp.Name + "_high"
	if high, ok := device.AlertThresholds[highKey]; ok && inp.Value > high {
		threshold, value := high, inp.Value
		return models.Event{
			DeviceID:     msg.DeviceID,
			MessageType:  "alert",
			TimestampMs:  msg.TimestampMs,
			AlertType:    highKey,
			Severity:     "warning",
			Threshold:    &threshold,
			ReadingValue: &value,
			ReadingName:  inp.Name,
		}, true
	}

	lowKey := inp.Name + "_low"
	if low, ok := device.AlertThresholds[lowKey]; ok && inp.Value < low {
		threshold, value := low, inp.Value
		return models.Event{
			DeviceID:     msg.DeviceID,
			MessageType:  "alert",
			TimestampMs:  msg.TimestampMs,
			AlertType:    lowKey,
			Severity:     "warning",
			Threshold:    &threshold,
			ReadingValue: &value,
			ReadingName:  inp.Name,
		}, true
	}

	return models.Event{}, false
}

// processEvent validates an alert or recovery message.
func processEvent(msg models.RawMessage) (models.Event, *ValidationError) {
	if msg.AlertType == "" {
		ve := &ValidationError{
			DeviceID:    msg.DeviceID,
			TimestampMs: msg.TimestampMs,
			Reason:      "missing alert_type",
		}
		slog.Warn("validate: rejected", "device_id", msg.DeviceID, "reason", ve.Reason)
		return models.Event{}, ve
	}
	if msg.Severity == "" {
		ve := &ValidationError{
			DeviceID:    msg.DeviceID,
			TimestampMs: msg.TimestampMs,
			Reason:      "missing severity",
		}
		slog.Warn("validate: rejected", "device_id", msg.DeviceID, "reason", ve.Reason)
		return models.Event{}, ve
	}

	return models.Event{
		DeviceID:     msg.DeviceID,
		MessageType:  msg.MessageType,
		TimestampMs:  msg.TimestampMs,
		AlertType:    msg.AlertType,
		Severity:     msg.Severity,
		Threshold:    msg.Threshold,
		ReadingValue: msg.ReadingValue,
		ReadingName:  msg.ReadingName,
	}, nil
}
