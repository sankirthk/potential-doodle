package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sankirthkalahasti/knaq-take-home/internal/api"
	"github.com/sankirthkalahasti/knaq-take-home/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fake store ---

type fakeStore struct {
	readings []models.Reading
	stats    []models.StatsResponse
	events   []models.Event
	device   *models.Device
}

func (f *fakeStore) HasData() (bool, error)              { return true, nil }
func (f *fakeStore) UpsertDevices([]models.Device) error { return nil }
func (f *fakeStore) InsertReading(models.Reading) error  { return nil }
func (f *fakeStore) InsertEvent(models.Event) error      { return nil }

func (f *fakeStore) GetDevice(id string) (models.Device, error) {
	if f.device != nil && f.device.DeviceID == id {
		return *f.device, nil
	}
	return models.Device{}, nil
}

func (f *fakeStore) GetReadings(deviceID string, from, to int64) ([]models.Reading, error) {
	return f.readings, nil
}

func (f *fakeStore) GetStats(deviceID string) ([]models.StatsResponse, error) {
	return f.stats, nil
}

func (f *fakeStore) GetDeviceEvents(deviceID, severity string) ([]models.Event, error) {
	if severity == "" {
		return f.events, nil
	}
	var filtered []models.Event
	for _, e := range f.events {
		if e.Severity == severity {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

func (f *fakeStore) GetCompanyEvents(company, severity string) ([]models.Event, error) {
	return f.GetDeviceEvents("", severity)
}

// --- test helpers ---

func newRouter(store *fakeStore, devices map[string]models.Device) http.Handler {
	tokens := map[string]string{"test-token": "Brookfield Properties"}
	return api.NewRouter(store, devices, tokens)
}

func elvDevice() models.Device {
	return models.Device{
		DeviceID:        "ELV-001",
		Company:         "Brookfield Properties",
		Timezone:        "America/New_York",
		ReadingTypes:    []string{"current", "frequency", "motor_status"},
		AlertThresholds: map[string]float64{"current_high": 180, "current_low": 5},
	}
}

func get(t *testing.T, handler http.Handler, path string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	require.NoError(t, json.NewDecoder(rr.Body).Decode(v))
}

// --- /devices/{id}/readings ---

func TestGetReadings_OK(t *testing.T) {
	store := &fakeStore{
		readings: []models.Reading{
			{
				DeviceID:    "ELV-001",
				TimestampMs: 1_770_805_800_000, // 2026-02-11T10:30:00Z
				Inputs: []models.ReadingInput{
					{InputName: "current", InputValue: 58.94},
					{InputName: "frequency", InputValue: 61.99},
				},
			},
		},
	}
	devices := map[string]models.Device{"ELV-001": elvDevice()}
	h := newRouter(store, devices)

	rr := get(t, h, "/devices/ELV-001/readings?start=2026-02-01&end=2026-02-28")

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp []models.ReadingResponse
	decodeJSON(t, rr, &resp)
	require.Len(t, resp, 1)
	assert.Equal(t, "ELV-001", resp[0].DeviceID)
	assert.Len(t, resp[0].Inputs, 2)
	assert.Equal(t, "current", resp[0].Inputs[0].Name)
	assert.Equal(t, 58.94, resp[0].Inputs[0].Value)
}

func TestGetReadings_TimestampInDeviceLocalTZ(t *testing.T) {
	// 1_770_805_800_000 ms = 2026-02-11T10:30:00Z = 2026-02-11T05:30:00-05:00 (America/New_York)
	store := &fakeStore{
		readings: []models.Reading{{
			DeviceID:    "ELV-001",
			TimestampMs: 1_770_805_800_000,
			Inputs:      []models.ReadingInput{{InputName: "current", InputValue: 50}},
		}},
	}
	devices := map[string]models.Device{"ELV-001": elvDevice()}
	h := newRouter(store, devices)

	rr := get(t, h, "/devices/ELV-001/readings?start=2026-02-01&end=2026-02-28")

	var resp []models.ReadingResponse
	decodeJSON(t, rr, &resp)
	require.Len(t, resp, 1)
	// Must contain the NY offset (-05:00 in February)
	assert.Contains(t, resp[0].Timestamp, "-05:00")
}

func TestGetReadings_MissingStart_400(t *testing.T) {
	h := newRouter(&fakeStore{}, map[string]models.Device{"ELV-001": elvDevice()})

	rr := get(t, h, "/devices/ELV-001/readings?end=2026-02-28")

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestGetReadings_MissingEnd_400(t *testing.T) {
	h := newRouter(&fakeStore{}, map[string]models.Device{"ELV-001": elvDevice()})

	rr := get(t, h, "/devices/ELV-001/readings?start=2026-02-01")

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestGetReadings_InvalidStartFormat_400(t *testing.T) {
	h := newRouter(&fakeStore{}, map[string]models.Device{"ELV-001": elvDevice()})

	rr := get(t, h, "/devices/ELV-001/readings?start=not-a-date&end=2026-02-28")

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestGetReadings_UnknownDevice_404(t *testing.T) {
	h := newRouter(&fakeStore{}, map[string]models.Device{})

	rr := get(t, h, "/devices/UNKNOWN/readings?start=2026-02-01&end=2026-02-28")

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// --- /devices/{id}/stats ---

func TestGetStats_OK(t *testing.T) {
	store := &fakeStore{
		stats: []models.StatsResponse{
			{Date: "2026-02-11", ReadingType: "current", Avg: 65, Min: 60, Max: 70, Count: 2},
		},
	}
	h := newRouter(store, map[string]models.Device{"ELV-001": elvDevice()})

	rr := get(t, h, "/devices/ELV-001/stats")

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp []models.StatsResponse
	decodeJSON(t, rr, &resp)
	require.Len(t, resp, 1)
	assert.Equal(t, "current", resp[0].ReadingType)
	assert.Equal(t, float64(65), resp[0].Avg)
}

func TestGetStats_EmptyReturnsArray(t *testing.T) {
	h := newRouter(&fakeStore{}, map[string]models.Device{"ELV-001": elvDevice()})

	rr := get(t, h, "/devices/ELV-001/stats")

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp []models.StatsResponse
	decodeJSON(t, rr, &resp)
	assert.NotNil(t, resp) // must be [] not null
}

func TestGetStats_UnknownDevice_404(t *testing.T) {
	h := newRouter(&fakeStore{}, map[string]models.Device{})

	rr := get(t, h, "/devices/UNKNOWN/stats")

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// --- /devices/{id}/alerts ---

func TestGetDeviceAlerts_OK(t *testing.T) {
	ts := int64(1_770_805_800_000)
	store := &fakeStore{
		events: []models.Event{
			{DeviceID: "ELV-001", MessageType: "alert", TimestampMs: ts, AlertType: "door_fault", Severity: "warning"},
		},
	}
	h := newRouter(store, map[string]models.Device{"ELV-001": elvDevice()})

	rr := get(t, h, "/devices/ELV-001/alerts")

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp []models.EventResponse
	decodeJSON(t, rr, &resp)
	require.Len(t, resp, 1)
	assert.Equal(t, "door_fault", resp[0].AlertType)
	assert.Contains(t, resp[0].Timestamp, "-05:00")
}

func TestGetDeviceAlerts_SeverityFilter(t *testing.T) {
	store := &fakeStore{
		events: []models.Event{
			{DeviceID: "ELV-001", MessageType: "alert", TimestampMs: 1000, AlertType: "a", Severity: "warning"},
			{DeviceID: "ELV-001", MessageType: "alert", TimestampMs: 2000, AlertType: "b", Severity: "critical"},
		},
	}
	h := newRouter(store, map[string]models.Device{"ELV-001": elvDevice()})

	rr := get(t, h, "/devices/ELV-001/alerts?severity=critical")

	var resp []models.EventResponse
	decodeJSON(t, rr, &resp)
	require.Len(t, resp, 1)
	assert.Equal(t, "critical", resp[0].Severity)
}

func TestGetDeviceAlerts_UnknownDevice_404(t *testing.T) {
	h := newRouter(&fakeStore{}, map[string]models.Device{})

	rr := get(t, h, "/devices/UNKNOWN/alerts")

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// --- GET /alerts (auth-protected) ---

func TestGetCompanyAlerts_NoToken_401(t *testing.T) {
	h := newRouter(&fakeStore{}, map[string]models.Device{"ELV-001": elvDevice()})

	rr := get(t, h, "/alerts")

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestGetCompanyAlerts_InvalidToken_401(t *testing.T) {
	h := newRouter(&fakeStore{}, map[string]models.Device{"ELV-001": elvDevice()})

	rr := get(t, h, "/alerts", "Authorization", "Bearer wrong-token")

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestGetCompanyAlerts_ValidToken_200(t *testing.T) {
	store := &fakeStore{
		events: []models.Event{
			{DeviceID: "ELV-001", MessageType: "alert", TimestampMs: 1_770_805_800_000, AlertType: "door_fault", Severity: "warning"},
		},
	}
	h := newRouter(store, map[string]models.Device{"ELV-001": elvDevice()})

	rr := get(t, h, "/alerts", "Authorization", "Bearer test-token")

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp []models.EventResponse
	decodeJSON(t, rr, &resp)
	require.Len(t, resp, 1)
	assert.Equal(t, "ELV-001", resp[0].DeviceID)
}

func TestGetCompanyAlerts_SeverityFilter(t *testing.T) {
	store := &fakeStore{
		events: []models.Event{
			{DeviceID: "ELV-001", MessageType: "alert", TimestampMs: 1000, AlertType: "a", Severity: "warning"},
			{DeviceID: "ELV-001", MessageType: "alert", TimestampMs: 2000, AlertType: "b", Severity: "critical"},
		},
	}
	h := newRouter(store, map[string]models.Device{"ELV-001": elvDevice()})

	rr := get(t, h, "/alerts?severity=warning", "Authorization", "Bearer test-token")

	var resp []models.EventResponse
	decodeJSON(t, rr, &resp)
	require.Len(t, resp, 1)
	assert.Equal(t, "warning", resp[0].Severity)
}

func TestGetCompanyAlerts_UsesDeviceTimezone(t *testing.T) {
	// ELV-001 is America/New_York; timestamp 1_770_805_800_000 = 05:30 local in Feb
	store := &fakeStore{
		events: []models.Event{{
			DeviceID: "ELV-001", MessageType: "alert", TimestampMs: 1_770_805_800_000,
			AlertType: "door_fault", Severity: "warning",
		}},
	}
	h := newRouter(store, map[string]models.Device{"ELV-001": elvDevice()})

	rr := get(t, h, "/alerts", "Authorization", "Bearer test-token")

	var resp []models.EventResponse
	decodeJSON(t, rr, &resp)
	require.Len(t, resp, 1)
	assert.Contains(t, resp[0].Timestamp, "-05:00")
}
