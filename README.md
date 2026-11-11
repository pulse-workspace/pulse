# Pulse

> **Ultra-fast, Distributed, Cloud-Native Instant Messaging Platform**
> Single binary, multi-role, cell-based sharding with event sourcing.

[![Go Version](https://img.shields.io/badge/go-1.22+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-Apache--2.0-green.svg)](LICENSE)
[![NATS JetStream](https://img.shields.io/badge/messaging-NATS%20JetStream-0080FF.svg)](https://nats.io)
[![PostgreSQL](https://img.shields.io/badge/database-PostgreSQL%2016-336791.svg)](https://www.postgresql.org)

Pulse is an enterprise-ready, open-source instant messaging engine designed for high throughput, predictable microsecond latencies, and horizontal cell-based scalability. Built from the ground up in Go, Pulse eliminates microservice spaghetti by packaging domain logic, WebSocket termination, and stream processing into a unified, modular architecture.

---

## Key Highlights

- **Cell-Based Sharding**: Scale horizontally by partitioning traffic into autonomous cells, eliminating cluster-wide failure domains.
- **CQRS + Event Sourcing**: Append-only event log backed by NATS JetStream with durable projections persisted in PostgreSQL 16.
- **Strict Gapless Ordering**: Atomic DB-backed sequence counters guaranteeing strictly monotonic `convSeq` ordering per conversation without gaps.
- **Targeted Distributed Routing**: Distributed user directory (`pulse_directory`) using NATS KeyValue store to route messages directly to active nodes—zero cross-cell broadcast amplification.
- **Seamless Session Handoff**: Stateless WebSocket gateways allow clients to reconnect to any node with zero message loss and instant catch-up sync.
- **Single Binary, Multi-Role**: Deploy as an all-in-one process (`pulse dev`) or independently scale specific roles (`cell`, `gateway`).
- **Clean Extensibility**: Modular extension system (`pkg/extension`) allowing commercial/enterprise plugins to mount cleanly without polluting the open-source core.

---

## Quick Start

### Prerequisites

- Go 1.22+
- Docker & Docker Compose v2
- Make

### 1. Start Infrastructure Dependencies

```bash
# Start PostgreSQL 16, NATS JetStream, and RustFS (S3 storage)
docker compose up -d postgres nats rustfs
```

### 2. Build the Pulse Binary

```bash
make deps
make build
```

### 3. Apply Schema Migrations

Pulse consolidates all schema definitions into a single, clean migration file:

```bash
make migrate-up
```

### 4. Run Pulse in Dev Mode

```bash
# Starts both cell (domain engine) and gateway (WebSocket termination)
./bin/pulse dev --config configs/dev-localhost.yaml
```

Verify service health:
```bash
curl http://localhost:32011/healthz
```

---

## Quick Smoke Test

```bash
# 1. Initialize Admin Account
TOKEN=$(curl -s -X POST http://localhost:32011/api/v1/admin/init \
  -H "Content-Type: application/json" \
  -d '{"secret":"dev-secret-change-me-in-production-32chars","adminUserID":"admin","adminPassword":"Pulse@Admin1"}' \
  | jq -r .data.adminToken)

# 2. Register a User
curl -X POST http://localhost:32011/api/v1/user/register \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"userID":"alice","nickname":"Alice","email":"alice@example.com"}'

# 3. Mint Client Token
USER_TOKEN=$(curl -s -X POST http://localhost:32011/api/v1/auth/userToken \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"userID":"alice","platform":"WEB"}' | jq -r .data.token)

# 4. Connect via WebSocket (using wscat or any client)
wscat -c ws://localhost:32001 -s pulse.protocol.v1.msgpack \
  -H "Authorization: Bearer $USER_TOKEN"
```

---

## Architecture Overview

Pulse replaces brittle legacy microservice topologies with a unified cell architecture:

```
┌────────────────────────────────────────────────────────┐
│                      PULSE CLUSTER                     │
│                                                        │
│  Cell-0 Node                                           │
│  ┌──────────────┐   ┌──────────────┐   ┌────────────┐  │
│  │ Gateway Role │   │ Domain Cell  │   │ Projection │  │
│  │ (WebSocket)  │   │ (Msg/Conv/Grp)   │ Runner     │  │
│  └──────┬───────┘   └──────┬───────┘   └─────┬──────┘  │
│         │                  │                 │         │
│         ▼                  ▼                 ▼         │
│  ┌──────────────────────────────────────────────────┐  │
│  │         NATS JetStream (Events + KV Directory)   │  │
│  └─────────────────────────┬────────────────────────┘  │
│                            │ (async projections)       │
│                            ▼                           │
│  ┌──────────────────────────────────────────────────┐  │
│  │               PostgreSQL 16 (Durable DB)         │  │
│  └──────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────┘
```

1. **Write Path (Command)**: Gateway accepts incoming WebSocket binary frame → validates token → writes event to NATS JetStream `events.messages`.
2. **Targeted Routing**: Directory lookup (`usr.<userID>`) determines which cells host active recipients and dispatches to specific NATS subjects.
3. **Read Path (Query)**: Async Projection Runner consumes NATS events and updates the PostgreSQL read model (`messages`, `conversation_unread`). Monotonic sequence counters are guaranteed via atomic row locking.

---

## Repository Layout

```
pulse/
├── cmd/pulse/            # Pure OSS single binary entrypoint
├── internal/
│   ├── cell/             # Domain engines (user, conversation, group, message, presence, friend, search, media)
│   ├── gateway/          # WebSocket termination and session lifecycle
│   └── api/              # REST server and standard handlers
├── pkg/
│   ├── app/              # Standard runtime application runner
│   ├── core/             # Public Core contracts (Cell, Service, User)
│   ├── extension/        # Plugin & extension registration interfaces
│   ├── protocol/         # Binary frame codecs & event types
│   └── shared/           # Config, DB, NATS, Auth, Logger, Metrics
├── migrations/           # Single consolidated migration (0001_init.up.sql)
├── configs/              # Dev and production YAML configuration profiles
├── docker-compose.yaml   # Local dev dependencies (Postgres, NATS, RustFS)
└── docs/                 # Documentation (English & Tiếng Việt)
```

---

## Documentation

Comprehensive guides and architecture documentation are available in `docs/`:

- 🇬🇧 **[English Documentation](./docs/en/index.md)** (Default):
  - [Architecture](./docs/en/architecture.md)
  - [Quickstart Guide](./docs/en/quickstart.md)
  - [REST API Reference](./docs/en/api-reference.md)
  - [WebSocket Protocol](./docs/en/websocket-protocol.md)
  - [Configuration](./docs/en/configuration.md)
  - [Contributing](./docs/en/contributing.md)
- 🇻🇳 **[Tài liệu Tiếng Việt](./docs/vi/index.md)**

Production deployment configurations and Helm charts have been extracted into the companion repository [`pulse-deploy`](https://github.com/pulse-im/pulse-deploy).

---

## Development & Testing

```bash
# Run unit and integration tests
make test

# Format and lint code
make fmt lint

# Build production binary
make build
```

---

## License

This repository is licensed under the [Apache License, Version 2.0](LICENSE).
Commercial enterprise extensions (SSO, Compliance, RBAC, Automated Backup) are available in the `pulse-enterprise` repository under the Polyform Commercial License.
