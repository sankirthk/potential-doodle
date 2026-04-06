package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sankirthkalahasti/knaq-take-home/internal/models"
	"github.com/sankirthkalahasti/knaq-take-home/internal/storage"
)

type handler struct {
	store   storage.Store
	devices map[string]models.Device
	tokens  map[string]string
}

const (
	defaultPageSize = 100
	maxPageSize     = 500
)

// GET /devices/{id}/readings?start=<RFC3339>&end=<RFC3339>&limit=<N>&after=<cursor>
func (h *handler) getReadings(w http.ResponseWriter, r *http.Request) {
	device, ok := h.lookupDevice(w, r)
	if !ok {
		return
	}

	from, to, ok := parseTimeRange(w, r, device.Timezone)
	if !ok {
		return
	}

	limit, after, ok := parsePagination(w, r)
	if !ok {
		return
	}

	// Fetch limit+1 to detect whether another page exists.
	readings, err := h.store.GetReadings(device.DeviceID, from, to, after, limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch readings")
		return
	}

	hasMore := len(readings) > limit
	if hasMore {
		readings = readings[:limit]
	}

	loc, _ := time.LoadLocation(device.Timezone)
	data := make([]models.ReadingResponse, 0, len(readings))
	for _, rr := range readings {
		inputs := make([]models.InputResponse, len(rr.Inputs))
		for i, inp := range rr.Inputs {
			inputs[i] = models.InputResponse{Name: inp.InputName, Value: inp.InputValue}
		}
		data = append(data, models.ReadingResponse{
			DeviceID:  rr.DeviceID,
			Timestamp: msToLocalRFC3339(rr.TimestampMs, loc),
			Inputs:    inputs,
		})
	}

	page := models.ReadingsPage{Data: data, HasMore: hasMore}
	if hasMore && len(readings) > 0 {
		cursor := readings[len(readings)-1].TimestampMs
		page.NextCursor = &cursor
	}
	writeJSON(w, http.StatusOK, page)
}

// GET /devices/{id}/stats
func (h *handler) getStats(w http.ResponseWriter, r *http.Request) {
	device, ok := h.lookupDevice(w, r)
	if !ok {
		return
	}

	stats, err := h.store.GetStats(device.DeviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch stats")
		return
	}

	if stats == nil {
		stats = []models.StatsResponse{}
	}
	writeJSON(w, http.StatusOK, stats)
}

// GET /devices/{id}/alerts?severity=<critical|warning>
func (h *handler) getDeviceAlerts(w http.ResponseWriter, r *http.Request) {
	device, ok := h.lookupDevice(w, r)
	if !ok {
		return
	}

	severity := r.URL.Query().Get("severity")
	events, err := h.store.GetDeviceEvents(device.DeviceID, severity)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch alerts")
		return
	}

	loc, _ := time.LoadLocation(device.Timezone)
	writeJSON(w, http.StatusOK, eventsToResponse(events, loc))
}

// GET /alerts?severity=<critical|warning>  (auth-protected)
func (h *handler) getCompanyAlerts(w http.ResponseWriter, r *http.Request) {
	company := companyFromContext(r.Context())

	severity := r.URL.Query().Get("severity")
	events, err := h.store.GetCompanyEvents(company, severity)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch alerts")
		return
	}

	// Company events span multiple devices — look up each device's timezone.
	resp := make([]models.EventResponse, 0, len(events))
	for _, e := range events {
		loc := deviceLoc(h.devices, e.DeviceID)
		resp = append(resp, eventToResponse(e, loc))
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- helpers ---

// lookupDevice resolves the {id} URL param to a Device, writing 404 if not found.
func (h *handler) lookupDevice(w http.ResponseWriter, r *http.Request) (models.Device, bool) {
	id := chi.URLParam(r, "id")
	device, ok := h.devices[id]
	if !ok {
		writeError(w, http.StatusNotFound, "device not found: "+id)
		return models.Device{}, false
	}
	return device, true
}

// parseTimeRange parses start/end query params as RFC3339 in the device's timezone,
// returning epoch-ms bounds. Writes 400 and returns false on any parse failure.
func parseTimeRange(w http.ResponseWriter, r *http.Request, timezone string) (from, to int64, ok bool) {
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	if startStr == "" || endStr == "" {
		writeError(w, http.StatusBadRequest, "start and end query params are required")
		return 0, 0, false
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid device timezone")
		return 0, 0, false
	}

	start, err := parseLocalTime(startStr, loc)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid start: "+err.Error())
		return 0, 0, false
	}

	end, err := parseLocalTime(endStr, loc)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid end: "+err.Error())
		return 0, 0, false
	}

	return start.UnixMilli(), end.UnixMilli(), true
}

// parsePagination parses optional ?limit= and ?after= query params.
// limit defaults to defaultPageSize, capped at maxPageSize.
// after is an epoch-ms cursor (0 means start from beginning).
func parsePagination(w http.ResponseWriter, r *http.Request) (limit int, after int64, ok bool) {
	limit = defaultPageSize
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return 0, 0, false
		}
		if n > maxPageSize {
			n = maxPageSize
		}
		limit = n
	}

	if s := r.URL.Query().Get("after"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "after must be a non-negative epoch ms integer")
			return 0, 0, false
		}
		after = n
	}

	return limit, after, true
}

// parseLocalTime tries several common timestamp formats, interpreting the result
// in the given location if the format has no explicit offset.
func parseLocalTime(s string, loc *time.Location) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("unrecognised time format (use RFC3339 or YYYY-MM-DD)")
}

// msToLocalRFC3339 converts epoch milliseconds to an RFC3339 string with the
// device's UTC offset (e.g. "2026-02-11T05:30:00-05:00").
func msToLocalRFC3339(ms int64, loc *time.Location) string {
	return time.UnixMilli(ms).In(loc).Format(time.RFC3339)
}

// deviceLoc returns the *time.Location for a device, falling back to UTC.
func deviceLoc(devices map[string]models.Device, deviceID string) *time.Location {
	if d, ok := devices[deviceID]; ok {
		if loc, err := time.LoadLocation(d.Timezone); err == nil {
			return loc
		}
	}
	return time.UTC
}

// eventsToResponse converts a slice of Events to EventResponse using the given location.
func eventsToResponse(events []models.Event, loc *time.Location) []models.EventResponse {
	resp := make([]models.EventResponse, 0, len(events))
	for _, e := range events {
		resp = append(resp, eventToResponse(e, loc))
	}
	return resp
}

// eventToResponse converts a single Event to its API response shape.
func eventToResponse(e models.Event, loc *time.Location) models.EventResponse {
	return models.EventResponse{
		DeviceID:     e.DeviceID,
		MessageType:  e.MessageType,
		Timestamp:    msToLocalRFC3339(e.TimestampMs, loc),
		AlertType:    e.AlertType,
		Severity:     e.Severity,
		Threshold:    e.Threshold,
		ReadingValue: e.ReadingValue,
		ReadingName:  e.ReadingName,
	}
}

// writeJSON serializes v as JSON and writes it to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes a consistent {"error": "..."} JSON response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
