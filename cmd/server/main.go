package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/sankirthkalahasti/knaq-take-home/internal/api"
	"github.com/sankirthkalahasti/knaq-take-home/internal/config"
	"github.com/sankirthkalahasti/knaq-take-home/internal/ingest"
	"github.com/sankirthkalahasti/knaq-take-home/internal/models"
	"github.com/sankirthkalahasti/knaq-take-home/internal/storage"
	"github.com/sankirthkalahasti/knaq-take-home/internal/validate"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// --- Config ---
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}

	// --- Database ---
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		slog.Error("db open", "error", err)
		os.Exit(1)
	}
	defer func(db *sql.DB) {
		err := db.Close()
		if err != nil {

		}
	}(db)

	if err := db.Ping(); err != nil {
		slog.Error("db ping", "error", err)
		os.Exit(1)
	}

	// --- Migrations ---
	migrationsPath := filepath.Join("migrations")
	if err := storage.RunMigrations(db, migrationsPath); err != nil {
		slog.Error("migrations", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations applied")

	store := storage.New(db)

	// --- Device registry ---
	devices, err := loadDevices("devices.json")
	if err != nil {
		slog.Error("load devices", "error", err)
		os.Exit(1)
	}
	if err := store.UpsertDevices(devices); err != nil {
		slog.Error("upsert devices", "error", err)
		os.Exit(1)
	}

	registry := make(map[string]models.Device, len(devices))
	for _, d := range devices {
		registry[d.DeviceID] = d
	}
	slog.Info("device registry loaded", "count", len(registry))

	// --- Ingest (only if DB is empty) ---
	hasData, err := store.HasData()
	if err != nil {
		slog.Error("check existing data", "error", err)
		os.Exit(1)
	}

	if hasData {
		slog.Info("data already present — skipping ingestion")
	} else {
		slog.Info("no data found — running ingestion")
		runIngestion("sensor_messages.json", registry, store)
	}

	// --- HTTP server ---
	router := api.NewRouter(store, registry, cfg.CompanyTokens)
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	go func() {
		slog.Info("server listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// --- Graceful shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}

// loadDevices reads and decodes devices.json.
func loadDevices(path string) ([]models.Device, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var devices []models.Device
	if err := json.Unmarshal(data, &devices); err != nil {
		return nil, err
	}
	return devices, nil
}

// runIngestion parses, validates, and stores all messages from the given file.
func runIngestion(path string, registry map[string]models.Device, store storage.Store) {
	msgs, parseErrs := ingest.Parse(path)
	slog.Info("ingest complete", "parsed", len(msgs), "errors", len(parseErrs))

	batch, validErrs := validate.New(registry).Process(msgs)
	slog.Info("validate complete",
		"readings", len(batch.Readings),
		"events", len(batch.Events),
		"rejected", len(validErrs),
	)

	var readingErrs, eventErrs int
	for _, r := range batch.Readings {
		if err := store.InsertReading(r); err != nil {
			slog.Warn("insert reading failed", "device_id", r.DeviceID, "error", err)
			readingErrs++
		}
	}
	for _, e := range batch.Events {
		if err := store.InsertEvent(e); err != nil {
			slog.Warn("insert event failed", "device_id", e.DeviceID, "error", err)
			eventErrs++
		}
	}

	slog.Info("ingestion stored",
		"readings_stored", len(batch.Readings)-readingErrs,
		"events_stored", len(batch.Events)-eventErrs,
		"store_errors", readingErrs+eventErrs,
	)
}
