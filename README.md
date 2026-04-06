# Knaq IoT Sensor Data Pipeline

A Go service that ingests IoT sensor data from elevators, escalators, and compressors, stores it in PostgreSQL, and exposes a REST API for querying readings, daily statistics, and alert events.

---

## System Design

```
┌─────────────────────────────────────────────────────────────────┐
│                         Startup Sequence                        │
│                                                                 │
│  devices.json ──► DeviceRegistry (in-memory map)                │
│  sensor_messages.json ──► Ingest ──► Validate ──► Store         │
└─────────────────────────────────────────────────────────────────┘

                    ┌──────────────┐
  sensor_messages   │              │  valid readings
       .json ──────►│   Ingestor   │──────────────────────────────┐
                    │              │  valid alerts/recoveries     │
                    └──────┬───────┘                              │
                           │ malformed / rejected                 │
                           ▼                                      │
                        [logger]                                  │
                                                                  ▼
                    ┌──────────────┐                    ┌──────────────────┐
  devices.json ────►│  Validator   │◄── device meta     │                  │
                    │              │                    │   PostgreSQL DB  │
                    └──────┬───────┘                    │                  │
                           │ flagged / deduplicated     │  devices         │
                           │                            │  readings        │
                           └───────────────────────────►│  reading_inputs  │
                                                        │  events          │
                                                        └────────┬─────────┘
                                                                 │
                    ┌──────────────┐                             │
  HTTP clients ────►│   REST API   │◄────────────────────────────┘
                    │  (chi router)│
                    └──────────────┘

  Endpoints:
    GET /devices/{id}/readings   — time-range query, local-tz timestamps
    GET /devices/{id}/stats      — daily aggregates per reading type
    GET /devices/{id}/alerts     — alerts/recoveries, optional severity filter
    GET /alerts                  — company-scoped alerts, bearer token auth
```

---

## Tech Stack

| Technology | Version | Rationale |
|---|---|---|
| **Go** | 1.26 | Type-safe, excellent stdlib HTTP + JSON support, fast startup |
| **chi** | v5 | Lightweight idiomatic router; URL params, middleware, no magic |
| **PostgreSQL** | 15 | Native `AT TIME ZONE` and `date_trunc` make device-local time aggregates trivial; relational joins handle company scoping cleanly |
| **lib/pq** | v1.12 | Mature, dependency-light Postgres driver; no ORM overhead |
| **golang-migrate** | v4 | SQL migration files with explicit versioning; `up`/`down` per migration |
| **testify** | v1.11 | Assertions without bloat; used across ingest, validate, storage, and API test suites |
| **Docker + docker-compose** | — | Reproducible environment; single command brings up DB and server |

---

## Quick Start

**Prerequisites:** Docker and Docker Compose.

```bash
docker-compose up --build
```

That's it. On first startup the server will:
1. Apply database migrations
2. Load the 10 devices from `devices.json` into the DB
3. Parse and ingest all ~800 messages from `sensor_messages.json`
4. Start the HTTP server on port `8080`

On subsequent startups ingestion is skipped — the server detects existing data and goes straight to serving.

---

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | Yes | — | PostgreSQL connection string, e.g. `postgres://user:pass@host:5432/dbname?sslmode=disable` |
| `PORT` | No | `8080` | HTTP server port |
| `COMPANY_TOKENS` | No | `""` | Comma-separated `token:company` pairs for the authenticated `/alerts` endpoint. Example: `abc123:Brookfield Properties,def456:Hines` |

The `docker-compose.yml` pre-configures all three with working defaults:

```
DATABASE_URL  = postgres://knaq:knaq@db:5432/knaq?sslmode=disable
PORT          = 8080
COMPANY_TOKENS = brookfield-token:Brookfield Properties,hines-token:Hines,mitsui-token:Mitsui Fudosan
```

---

## API Reference

All endpoints return `application/json`. Timestamps in responses use RFC3339 format with the **device's local UTC offset** (e.g. `2026-02-11T05:30:00-05:00` for a New York device).

### GET /devices/{id}/readings

Returns all sensor readings for a device within a time range.

**Query params**

| Param | Required | Format | Description |
|---|---|---|---|
| `start` | Yes | RFC3339 or `YYYY-MM-DD` | Start of range, interpreted in device's local timezone |
| `end` | Yes | RFC3339 or `YYYY-MM-DD` | End of range, interpreted in device's local timezone |

**Example**

```bash
curl "http://localhost:8080/devices/ELV-001/readings?start=2026-02-01&end=2026-02-28"
```

```json
[
  {
    "device_id": "ELV-001",
    "timestamp": "2026-02-10T01:45:43-05:00",
    "inputs": [
      { "name": "current",   "value": 123.69 },
      { "name": "frequency", "value": 60.18  }
    ]
  }
]
```

**Errors**

| Status | Reason |
|---|---|
| `400` | Missing or unparseable `start` / `end` |
| `404` | Unknown device ID |

---

### GET /devices/{id}/stats

Returns daily aggregates (`avg`, `min`, `max`, `count`) per input type, grouped by calendar day in the device's local timezone. `motor_status` is excluded from aggregation.

**Example**

```bash
curl "http://localhost:8080/devices/CMP-001/stats"
```

```json
[
  {
    "date": "2026-02-10",
    "reading_type": "current",
    "avg": 55.3,
    "min": 12.1,
    "max": 94.7,
    "count": 18
  },
  {
    "date": "2026-02-10",
    "reading_type": "temperature",
    "avg": 88.4,
    "min": 61.2,
    "max": 131.8,
    "count": 18
  }
]
```

**Errors**

| Status | Reason |
|---|---|
| `404` | Unknown device ID |

---

### GET /devices/{id}/alerts

Returns all alert and recovery events for a device.

**Query params**

| Param | Required | Values | Description |
|---|---|---|---|
| `severity` | No | `critical`, `warning` | Filter by severity |

**Example**

```bash
# All alerts
curl "http://localhost:8080/devices/ELV-001/alerts"

# Critical only
curl "http://localhost:8080/devices/ELV-001/alerts?severity=critical"
```

```json
[
  {
    "device_id":    "ELV-001",
    "message_type": "alert",
    "timestamp":    "2026-02-10T15:15:00-05:00",
    "alert_type":   "current_low",
    "severity":     "warning",
    "threshold":    5,
    "reading_value": -5.2,
    "reading_name": "current"
  }
]
```

`threshold`, `reading_value`, and `reading_name` are present only for threshold-derived events. They are omitted for device-sent events like `door_fault` or `vibration_anomaly`.

**Errors**

| Status | Reason |
|---|---|
| `404` | Unknown device ID |

---

### GET /alerts

Returns alert and recovery events scoped to the authenticated company. Requires a Bearer token.

**Headers**

```
Authorization: Bearer <token>
```

**Query params**

| Param | Required | Values | Description |
|---|---|---|---|
| `severity` | No | `critical`, `warning` | Filter by severity |

**Example**

```bash
curl -H "Authorization: Bearer brookfield-token" \
     "http://localhost:8080/alerts"

curl -H "Authorization: Bearer hines-token" \
     "http://localhost:8080/alerts?severity=critical"
```

Response shape is identical to `GET /devices/{id}/alerts`.

**Errors**

| Status | Reason |
|---|---|
| `401` | Missing, malformed, or unrecognized token |

---

## Running Tests

```bash
# Unit tests (no DB required)
go test ./internal/ingest/... ./internal/validate/... ./internal/api/...

# Integration tests (requires a running PostgreSQL)
DATABASE_URL="postgres://knaq:knaq@localhost:5433/knaq?sslmode=disable" \
  go test ./internal/storage/...

# All tests at once (DB must be running)
DATABASE_URL="postgres://knaq:knaq@localhost:5433/knaq?sslmode=disable" \
  go test ./...
```

To start just the database for local development:

```bash
docker-compose up -d db
# DB is now available at localhost:5433
# (5433 avoids collision with any local PostgreSQL on 5432)
```

---

## Project Structure

```
cmd/server/          — entry point: wires config, DB, ingest, HTTP
internal/
  config/            — environment variable loading
  models/            — shared domain types (no logic)
  ingest/            — parses sensor_messages.json, normalises timestamps
  validate/          — deduplication, field validation, threshold checking
  storage/           — PostgreSQL Store interface + implementation, migrations
  api/               — chi router, handlers, auth middleware
migrations/          — numbered SQL migration files (up + down)
docs/                — requirements, architecture, OpenAPI spec, todo
devices.json         — 10 IoT device definitions with thresholds
sensor_messages.json — ~800 sensor messages (readings, alerts, recoveries)
```

---

## Design Decisions

### Storage: PostgreSQL

PostgreSQL was chosen over SQLite, TimescaleDB, and flat-file alternatives because:
- `date_trunc` and `AT TIME ZONE` make device-local daily aggregates trivial to express in SQL
- Relational joins handle company-scoped alert queries cleanly
- At ~800 messages across 10 devices, operational simplicity matters more than write throughput

### Schema: 4 tables

```
devices        — device metadata and alert thresholds (JSONB)
readings       — one row per reading message, deduped on (device_id, timestamp_ms)
reading_inputs — one row per sensor value; motor_status stored here as 0.0/1.0
events         — device-sent and pipeline-derived alerts/recoveries
```

`motor_status` lives in `reading_inputs` rather than a separate table. It's just another named input — querying it is a `WHERE input_name = 'motor_status'` filter, and this avoids an extra join on every "what was the device doing at time T?" query.

Threshold breach events are written as rows in `events` (with `threshold`, `reading_value`, `reading_name` populated) rather than flags on `reading_inputs`. This keeps raw data separate from derived state, and means threshold rules can be re-evaluated by dropping and re-inserting event rows without touching the readings.

### Deduplication: two layers

- **Application layer**: an in-memory `map[string]struct{}` in the validator keyed on `device_id:timestamp_ms:message_type` prevents redundant DB writes during a single ingest run.
- **Database layer**: unique indexes on `readings(device_id, timestamp_ms)` and `events(device_id, timestamp_ms, message_type)` provide a hard guarantee if the pipeline is ever run again. A conflict is treated as a no-op.

### Timestamps: epoch ms in storage, RFC3339 at the API boundary

All timestamps are stored as `BIGINT` epoch milliseconds in UTC. Conversion to device-local time happens only at the API layer using Go's `time.LoadLocation` with the device's IANA timezone string. This avoids PostgreSQL timezone-aware column edge cases and keeps UTC as the single source of truth.

### Auth: static token map

`GET /alerts` is scoped to a company via `Authorization: Bearer <token>`. The token-to-company mapping is loaded from the `COMPANY_TOKENS` environment variable at startup. This demonstrates multi-tenant scoping without requiring a user/auth database. Replacing it with JWT validation is a one-function change in `middleware.go`.

### Ingestion: one-shot, skip if data exists

The pipeline runs once at startup and populates the database from the JSON files. On subsequent restarts, a `SELECT EXISTS(SELECT 1 FROM readings)` check skips ingestion entirely — avoiding redundant parsing and insert attempts on every deploy.

---

## Known Limitations

- **No re-ingestion mechanism**: if `sensor_messages.json` is updated, you need to truncate the tables and restart. A `POST /ingest` endpoint or a `--force-reingest` flag would address this.
- **Threshold severity is always `warning`**: pipeline-generated threshold events don't have severity info in the device config, so all are stored as `"warning"`. A `severity` field on device thresholds would fix this.
- **No pagination on readings**: `GET /devices/{id}/readings` returns all matching rows. At production scale this would need `limit`/`offset` or cursor-based pagination.
- **Single-node**: no connection pooling configuration, no read replicas. `database/sql`'s default pool is fine for this scale.
