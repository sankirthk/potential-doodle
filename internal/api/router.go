package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sankirthkalahasti/knaq-take-home/internal/models"
	"github.com/sankirthkalahasti/knaq-take-home/internal/storage"
)

// NewRouter builds and returns the chi router with all routes registered.
func NewRouter(store storage.Store, devices map[string]models.Device, tokens map[string]string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	h := &handler{store: store, devices: devices, tokens: tokens}

	r.Get("/devices/{id}/readings", h.getReadings)
	r.Get("/devices/{id}/stats", h.getStats)
	r.Get("/devices/{id}/alerts", h.getDeviceAlerts)

	r.Group(func(r chi.Router) {
		r.Use(h.authMiddleware)
		r.Get("/alerts", h.getCompanyAlerts)
	})

	return r
}
