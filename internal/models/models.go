package models

// Device represents an IoT sensor unit from the device registry.
type Device struct {
	DeviceID        string             `json:"device_id"`
	Type            string             `json:"type"`
	Company         string             `json:"company"`
	Name            string             `json:"name"`
	Location        string             `json:"location"`
	Timezone        string             `json:"timezone"`
	FloorCount      *int               `json:"floor_count,omitempty"`
	InstalledDate   string             `json:"installed_date"`
	ReadingTypes    []string           `json:"reading_types"`
	AlertThresholds map[string]float64 `json:"alert_thresholds"`
}

// RawMessage is the parsed-but-unvalidated shape of a single message from sensor_messages.json.
type RawMessage struct {
	DeviceID     string
	MessageType  string
	TimestampMs  int64
	Inputs       []RawInput
	AlertType    string
	Severity     string
	Threshold    *float64
	ReadingValue *float64
	ReadingName  string
}

// RawInput is a single sensor input within a RawMessage.
type RawInput struct {
	Name  string
	Value float64
}

// Reading is a validated sensor reading message (storage model).
type Reading struct {
	ID          int64
	DeviceID    string
	TimestampMs int64
	Inputs      []ReadingInput
}

// ReadingInput is one sensor input belonging to a Reading.
// motor_status is stored here as 0.0 or 1.0 alongside numeric inputs.
type ReadingInput struct {
	ReadingID  int64
	InputName  string
	InputValue float64
}

// Event represents a validated alert or recovery message (storage model).
// Threshold, ReadingValue, and ReadingName are non-nil only for threshold-derived events.
type Event struct {
	ID           int64
	DeviceID     string
	MessageType  string // "alert" | "recovery"
	TimestampMs  int64
	AlertType    string
	Severity     string
	Threshold    *float64
	ReadingValue *float64
	ReadingName  string
}

// --- API response types ---

// ReadingResponse is the JSON shape returned by GET /devices/{id}/readings.
type ReadingResponse struct {
	DeviceID  string          `json:"device_id"`
	Timestamp string          `json:"timestamp"` // RFC3339 with tz offset
	Inputs    []InputResponse `json:"inputs"`
}

// InputResponse is a single sensor input within a ReadingResponse.
type InputResponse struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// StatsResponse is one row returned by GET /devices/{id}/stats.
type StatsResponse struct {
	Date        string  `json:"date"`         // "YYYY-MM-DD" in device-local tz
	ReadingType string  `json:"reading_type"`
	Avg         float64 `json:"avg"`
	Min         float64 `json:"min"`
	Max         float64 `json:"max"`
	Count       int     `json:"count"`
}

// EventResponse is the JSON shape returned by alert/recovery endpoints.
type EventResponse struct {
	DeviceID     string   `json:"device_id"`
	MessageType  string   `json:"message_type"`
	Timestamp    string   `json:"timestamp"` // RFC3339 with tz offset
	AlertType    string   `json:"alert_type"`
	Severity     string   `json:"severity"`
	Threshold    *float64 `json:"threshold,omitempty"`
	ReadingValue *float64 `json:"reading_value,omitempty"`
	ReadingName  string   `json:"reading_name,omitempty"`
}
