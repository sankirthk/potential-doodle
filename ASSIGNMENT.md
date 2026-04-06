# Knaq IoT Sensor Pipeline — Take-Home Assignment

## Overview

Knaq, Inc. monitors industrial equipment (elevators, escalators, compressors) using IoT sensor devices deployed across multiple locations worldwide. Each device periodically pushes messages to the cloud — sensor readings, motor status updates, alerts, and recoveries.

Your task is to build a **data ingestion and query pipeline** that processes raw device messages, validates and stores them, and exposes the data through a REST API.

**Language:** Your choice. Use whatever you're most productive in.

---

## What You're Given

### 1. `devices.json` — Device Registry

A list of 10 IoT devices with metadata:

| Field | Description |
|---|---|
| `device_id` | Unique identifier (e.g., `ELV-001`, `CMP-002`) |
| `type` | Equipment type: `elevator`, `escalator`, or `compressor` |
| `company` | The company that owns this device (e.g., `"Brookfield Properties"`) |
| `timezone` | IANA timezone where the device is physically deployed (e.g., `America/New_York`) |
| `reading_types` | What this device measures (e.g., `["current", "frequency", "motor_status"]`) |
| `alert_thresholds` | Twin-configured thresholds that the device uses to trigger alerts (e.g., `current_high`, `temperature_low`) |
| Other fields | `name`, `location`, `installed_date`, `floor_count` (elevators only) |

**Equipment types and their readings:**
- **Elevators:** current, frequency, motor_status
- **Escalators:** current, frequency, motor_status
- **Compressors:** current, frequency, temperature, motor_status

### 2. `sensor_messages.json` — Raw Device Messages

~800 messages pushed by devices over a 3-day window. There is **no message ID** — devices push raw payloads. All timestamps are in **epoch milliseconds (UTC)**.

**Message types:**

**Sensor Reading** — periodic measurements:
```json
{
  "device_id": "ELV-001",
  "message_type": "reading",
  "timestamp": 1770737458000,
  "inputs": [
    { "input_name": "current", "input_value": 58.94 },
    { "input_name": "frequency", "input_value": 61.99 }
  ]
}
```

**Motor Status** — motor start/stop events. These arrive as readings with a special input:
```json
{
  "device_id": "ESC-001",
  "message_type": "reading",
  "timestamp": 1770706542000,
  "inputs": [
    { "input_name": "motor_status", "input_value": 1 }
  ]
}
```
`1` = motor started, `0` = motor stopped.

**Alert** — device-detected threshold breach. Includes the threshold and the reading that triggered it:
```json
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
```

Some alerts are not threshold-based (e.g., `door_fault`, `vibration_anomaly`, `chain_tension`) and won't include `threshold`, `reading_value`, or `reading_name`.

**Recovery** — device reports condition has returned to normal. Includes the threshold and the reading that cleared the condition:
```json
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
```

---

## What You Build

### Stage 1 — Ingest & Parse

Read all messages from `sensor_messages.json`. Your system should:

- Distinguish between the different message types (reading, alert, recovery)
- Handle malformed or incomplete messages gracefully — log them, don't crash
- The data is not perfectly clean. Part of the task is dealing with that.

### Stage 2 — Validate

- Validate sensor readings against the device's `alert_thresholds` from `devices.json`. Readings that breach a threshold should be flagged — stored separately, not discarded silently.
- Detect and handle **duplicate messages** (since there is no message ID, you'll need to decide what constitutes a duplicate)
- Validate that readings match the device's expected `reading_types`
- Validate alert and recovery messages for required fields (`device_id`, `timestamp`, `alert_type`, `severity`, `message_type`)

### Stage 3 — Store

Persist validated data to storage. **You choose the storage technology and schema design.** This is a deliberate part of the evaluation — be prepared to explain your choices.

Consider:
- The different message types have different shapes and access patterns
- Think about how the data will be queried (see Stage 4)

### Stage 4 — Query API

Build a REST API with the following endpoints:

#### `GET /devices/:id/readings`
- Returns sensor readings for a specific device
- **Required:** `start` and `end` query parameters for time range filtering
- Time parameters are in the **device's local timezone** (e.g., if the device is in `America/New_York`, passing `start=2026-02-10T09:00:00` means 9:00 AM Eastern)
- Response timestamps should also be in the **device's local timezone**

#### `GET /devices/:id/stats`
- Returns daily aggregate statistics per reading type
- Fields: `date`, `reading_type`, `avg`, `min`, `max`, `count`
- A "day" is midnight-to-midnight in the **device's local timezone**

#### `GET /devices/:id/alerts`
- Returns alerts and recoveries for a specific device
- Optional filtering by `severity` (e.g., `?severity=critical`)
- Response timestamps in device's local timezone

#### `GET /alerts`
- Returns alerts and recoveries across devices
- **Scoped to the requesting company** — assume the company identity comes from a bearer token. For the purpose of this exercise, you can implement this however you'd like (hardcoded, config, middleware, etc.) — just show that the filtering works.
- Optional filtering by `severity`
- Response timestamps in each device's respective local timezone

---

## What We're Looking For

| Area | What matters |
|---|---|
| **Correctness** | Does it work? Do the endpoints return correct data? |
| **Data handling** | How do you deal with messy data, duplicates, validation failures? |
| **Storage design** | Is the schema sensible? Do your storage choices fit the data? |
| **Timezone handling** | Are conversions correct? Does time filtering work properly? |
| **Code quality** | Readable, organized, reasonable error handling |
| **Documentation** | README with setup instructions, design decisions, trade-offs |

### Bonus (not required)
- Dockerized submission (`docker-compose up` and it runs)
- Pagination on list endpoints
- Basic anomaly flagging (readings within valid range but unusual compared to recent history)
- Handling out-of-order messages
- Tests

---

## Submission

Send your completed work as a **zip file**. Include a **README.md** with:
- How to set up and run
- Your storage choice and why
- Design decisions and trade-offs
- What you'd improve with more time

If anything is unclear, reply to this email and we'll clarify.

Good luck!
