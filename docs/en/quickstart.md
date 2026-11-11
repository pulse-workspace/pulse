# Quickstart Guide

This guide walks you through setting up Pulse locally, compiling the binary, running database migrations, and executing a quick end-to-end smoke test.

---

## Prerequisites

- **Go**: 1.22 or higher
- **Docker & Docker Compose**: v2+
- **Make**: Standard GNU Make
- **wscat** or any WebSocket client tool (optional, for WebSocket smoke tests)

---

## 1. Start Infrastructure Dependencies

Pulse requires PostgreSQL 16 and NATS JetStream. You can start both using the provided `docker-compose.yaml`:

```bash
docker compose up -d postgres nats rustfs
```

Verify that the services are healthy:
```bash
docker compose ps
```

---

## 2. Build the Pulse Binary

Download Go dependencies and build the unified binary:

```bash
make deps
make build
```

The compiled binary will be placed in `./bin/pulse`.

---

## 3. Run Database Migrations

Apply database schema migrations to PostgreSQL:

```bash
make migrate-up
```

---

## 4. Launch Pulse in All-in-One Dev Mode

Start both the Domain Cell and WebSocket Gateway inside a single dev process:

```bash
./bin/pulse dev --config configs/dev-localhost.yaml
```

Check the health status:
```bash
curl http://localhost:32011/healthz
```

Expected response:
```json
{"status":"ok"}
```

---

## 5. End-to-End Smoke Test

### A. Bootstrap Admin Credentials
```bash
ADMIN_TOKEN=$(curl -s -X POST http://localhost:32011/api/v1/admin/init \
  -H "Content-Type: application/json" \
  -d '{"secret":"dev-secret-change-me-in-production-32chars","adminUserID":"admin","adminPassword":"Pulse@Admin1"}' \
  | jq -r .data.adminToken)
```

### B. Register a New User
```bash
curl -s -X POST http://localhost:32011/api/v1/user/register \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"userID":"alice","nickname":"Alice","email":"alice@example.com"}'
```

### C. Issue a Client JWT Token
```bash
USER_TOKEN=$(curl -s -X POST http://localhost:32011/api/v1/auth/userToken \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"userID":"alice","platform":"WEB"}' \
  | jq -r .data.token)
```

### D. Connect to WebSocket Gateway
Connect to the WebSocket endpoint using `pulse.protocol.v1.msgpack` subprotocol:

```bash
wscat -c ws://localhost:32001 -s pulse.protocol.v1.msgpack \
  -H "Authorization: Bearer $USER_TOKEN"
```

