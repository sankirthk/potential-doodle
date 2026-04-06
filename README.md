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

Returns sensor readings for a device within a time range, paginated.

**Query params**

| Param | Required | Format | Description |
|---|---|---|---|
| `start` | Yes | RFC3339 or `YYYY-MM-DD` | Start of range, interpreted in device's local timezone |
| `end` | Yes | RFC3339 or `YYYY-MM-DD` | End of range, interpreted in device's local timezone |
| `limit` | No | integer (1–500) | Page size. Default: 100 |
| `after` | No | epoch ms integer | Cursor from previous page's `next_cursor`. Returns readings with `timestamp_ms > after` |

**Example**

```bash
# First page
curl "http://localhost:8080/devices/ELV-001/readings?start=2026-02-01&end=2026-02-28&limit=2"

# Next page using cursor from response
curl "http://localhost:8080/devices/ELV-001/readings?start=2026-02-01&end=2026-02-28&limit=2&after=1770737458000"
```

```json
{
  "data": [
    {
      "device_id": "ELV-001",
      "timestamp": "2026-02-10T01:45:43-05:00",
      "inputs": [
        { "name": "current",   "value": 123.69 },
        { "name": "frequency", "value": 60.18  }
      ]
    }
  ],
  "next_cursor": 1770737458000,
  "has_more": true
}
```

`next_cursor` is the `timestamp_ms` of the last returned reading. Pass it as `?after=` to fetch the next page. It is omitted when `has_more` is `false`.

**Errors**

| Status | Reason |
|---|---|
| `400` | Missing or unparseable `start` / `end`, or invalid `limit` / `after` |
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

## Design Decisions & Trade-offs

### Storage: PostgreSQL vs. alternatives

| Option | Why considered | Why rejected |
|---|---|---|
| **SQLite** | Zero setup, single file, great for small datasets | No native `AT TIME ZONE` — device-local daily grouping would require pulling all rows into Go and grouping in application code |
| **TimescaleDB** | Built for time-series, excellent compression and retention | Over-engineered for ~800 messages; adds operational complexity (extension, chunking config) with no benefit at this scale |
| **Flat file / in-memory** | Simplest possible implementation | No query language; stats and filtering require full scans with custom code |
| **PostgreSQL** ✓ | `date_trunc` + `AT TIME ZONE` make device-local daily stats a one-liner; relational joins handle company scoping cleanly; `ON CONFLICT DO NOTHING` gives safe idempotent inserts | Requires a running server (Docker adds a setup step vs. SQLite) |

The deciding factor was the stats endpoint. `date_trunc('day', to_timestamp(ts/1000.0) AT TIME ZONE device_tz)` is a single SQL expression. In SQLite, the equivalent requires fetching every row, converting timestamps in Go, and grouping manually — a significant amount of logic that PostgreSQL handles in the query itself.

---

### Schema: 4-table design

```
devices        — device metadata and alert thresholds (JSONB)
readings       — one row per reading message, deduped on (device_id, timestamp_ms)
reading_inputs — one row per sensor value; motor_status stored here as 0.0/1.0
events         — device-sent and pipeline-derived alerts/recoveries
```

**Readings split from reading_inputs** — Each device type reports a different set of inputs (elevators: `current`, `frequency`, `motor_status`; compressors: `current`, `temperature`, `pressure`). A wide table with one column per input type would have many NULLs per row and require a schema migration whenever a new device type is added. One row per input keeps the schema device-agnostic and makes `AVG/MIN/MAX GROUP BY input_name` trivial.

**motor_status in reading_inputs, not its own table** — `motor_status` arrives inside the same message as numeric readings. Storing it as `0.0`/`1.0` alongside other inputs avoids an extra join on queries like "what was the motor doing at time T?". A CHECK constraint enforces only `0` and `1` are valid for that input name.

**Threshold events in events, not flags on reading_inputs** — Threshold breaches are derived state computed by the pipeline, not raw data sent by the device. Keeping them separate means: (1) raw readings stay immutable — if a threshold config changes, derived events can be dropped and recomputed without touching `readings`; (2) device-sent alerts (`door_fault`) and pipeline-derived ones (`current_low`) are returned from a single query in the same shape. The nullable `threshold`/`reading_value`/`reading_name` columns are only populated for derived events.

---

### Timestamps: BIGINT epoch ms vs. TIMESTAMPTZ

All timestamps are stored as `BIGINT` epoch milliseconds in UTC. Timezone conversion happens only at the API layer via Go's `time.LoadLocation`.

The alternative — `TIMESTAMPTZ` — sounds more natural but creates a subtle problem: `AT TIME ZONE` in PostgreSQL returns `TIMESTAMP WITHOUT TIME ZONE`, which means the Go scanner receives a value with no timezone attached. You end up managing timezone context in two places. BIGINT + a single `time.UnixMilli(ms).In(loc)` call in Go is simpler, keeps UTC as the unambiguous source of truth, and avoids PostgreSQL timezone-aware column edge cases entirely.

Trade-off: epoch ms is less human-readable in the DB. Any direct SQL inspection requires `to_timestamp(ts / 1000.0)` to see meaningful dates.

---

### Deduplication: two layers

- **Application layer**: an in-memory `map[string]struct{}` keyed on `device_id:timestamp_ms:message_type` prevents redundant DB writes within a single ingest run — no DB round-trip required.
- **Database layer**: unique indexes on `readings(device_id, timestamp_ms)` and `events(device_id, timestamp_ms, message_type)` with `ON CONFLICT DO NOTHING` guarantee correctness across restarts.

The app-layer map handles the common case cheaply. The DB index is the safety net — if the pipeline is restarted mid-run or run twice, the DB rejects duplicates without any application code change. Both layers are needed: the map avoids redundant I/O in the hot path; the index is the authoritative guarantee.

---

### Pagination: keyset vs. OFFSET

`GET /devices/{id}/readings` uses keyset pagination on `timestamp_ms` (`?after=<cursor>`).

`OFFSET N` scans and discards the first N rows every time. At 10,000 readings, page 100 with `OFFSET 9900` still reads 9,900 rows to find the start. Keyset pagination uses the index on `(device_id, timestamp_ms)` to jump directly to the cursor position — no rows are discarded.

Trade-off: keyset does not support random page access ("jump to page 5"). For a sensor timeline this is not a problem — clients walk forward through time chronologically, which is exactly the access pattern keyset is optimised for.

---

### Auth: static token map

The `GET /alerts` endpoint is scoped to a company via `Authorization: Bearer <token>`. The token-to-company mapping is loaded from `COMPANY_TOKENS` at startup.

This demonstrates multi-tenant query scoping without requiring a user/auth database. The trade-off is operability: tokens cannot be rotated without a restart and carry no expiry. The upgrade path is JWT tokens (stateless, carry expiry and company claims) and eventually per-device certificates so that spoofed `device_id` values in ingested data can be rejected at the ingestion boundary.

---

### Ingestion: one-shot with skip-if-exists

The pipeline runs once at startup. A `SELECT EXISTS(SELECT 1 FROM readings)` check skips ingestion on subsequent restarts, avoiding redundant parsing and insert attempts on every deploy.

Trade-off: updating `sensor_messages.json` after the initial run has no effect without truncating the tables manually. The idempotent `ON CONFLICT DO NOTHING` inserts mean a re-run would be safe — a `--force-reingest` flag is the natural next step.

---

## What Would Be Improved With More Time

- **Anomaly flagging**: static `_high`/`_low` thresholds per device miss gradual drift. A rolling statistical baseline (e.g. flag readings >2 standard deviations from a 24-hour window) would be more useful. PostgreSQL window functions (`AVG() OVER`, `STDDEV() OVER`) make this feasible as a background job that writes anomaly events without touching raw readings.

- **Authentication**: JWT tokens (stateless, carry expiry and company claims); token rotation and revocation via a `tokens` table with `expires_at`; per-device certificates so rogue data cannot be injected by spoofing a `device_id`.

- **Rate limiting**: the chi middleware ecosystem includes `httprate` (token bucket, a few lines to wire in). Per-token limiting is the right boundary for the authenticated endpoint.

- **Re-ingestion**: a `--force-reingest` CLI flag or `POST /admin/ingest` endpoint to handle updates to `sensor_messages.json` without manual DB truncation.

- **Threshold severity**: pipeline-generated threshold events are always stored as `"warning"`. Adding a `severity` field per threshold in `devices.json` (e.g. `current_high_severity: "critical"`) would fix this without a schema change.

- **Connection pool tuning**: `database/sql` defaults are fine at this scale but need `SetMaxOpenConns`, `SetMaxIdleConns`, and a statement timeout under real load.
