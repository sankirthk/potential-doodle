# --- Stage 1: Build ---
FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o knaq-server ./cmd/server

# --- Stage 2: Runtime ---
FROM alpine:3.21

WORKDIR /app

# tzdata: IANA timezone database required for device-local time conversions
# ca-certificates: for any outbound TLS connections
RUN apk add --no-cache tzdata ca-certificates

COPY --from=builder /app/knaq-server .
COPY --from=builder /app/migrations ./migrations
COPY --from=builder /app/devices.json .
COPY --from=builder /app/sensor_messages.json .

EXPOSE 8080

CMD ["./knaq-server"]
