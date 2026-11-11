# Configuration Reference

Pulse can be configured using YAML files located in `configs/` or via environment variables. Environment variables take precedence over YAML configuration values.

---

## Configuration Files

- `configs/dev.yaml`: Used for local development with default ports and local service endpoints.
- `configs/dev-localhost.yaml`: Tuned for single-machine local running outside Docker.
- `configs/prod.yaml`: Production profile with hardened defaults and TLS/replica support.

---

## Key Configuration Sections

### 1. Database (`db`)

```yaml
postgres:
  host: localhost
  port: 32432              # Local dev port (32432 prevents collision with host Postgres on 5432/15432)
  user: pulse
  password: pulse_password
  database: pulse
  ssl_mode: disable
  max_conns: 50
  min_conns: 2
```

- **Environment Override**: `PULSE_DB_HOST`, `PULSE_DB_PORT`, `PULSE_DB_USER`, `PULSE_DB_PASSWORD`, `PULSE_DB_NAME` or `DATABASE_URL`.

### 2. NATS JetStream (`nats`)

```yaml
nats:
  url: nats://localhost:32222  # Local dev port (32222 prevents collision with default 4222)
  cluster_id: pulse-cluster
  cell_id: cell-0
  stream_prefix: pulse
```

- **Environment Override**: `PULSE_NATS_URL`, `PULSE_NATS_CLUSTER_ID`.

### 3. Gateway & API Server

```yaml
server:
  api_port: 32011          # REST API HTTP port
  gateway_port: 32001      # WebSocket Gateway port
  metrics_port: 9090      # Prometheus /metrics endpoint
  read_timeout: 15s
  write_timeout: 15s
```

- **Environment Override**: `PULSE_API_PORT`, `PULSE_GATEWAY_PORT`.

### 4. Authentication (`auth`)

```yaml
auth:
  jwt_secret: "your-at-least-32-chars-long-secure-jwt-secret"
  token_expiry: 24h
  refresh_expiry: 720h
```

- **Environment Override**: `PULSE_JWT_SECRET`, `PULSE_TOKEN_EXPIRY`.

### 5. Media & Object Storage (`object_storage`)

Pulse connects to any S3-compatible backend (AWS S3, MinIO, Cloudflare R2, RustFS) via the S3 driver. In local development, the included `rustfs` container provides the S3 server on port 32900 (avoiding collisions with default port 9000).

```yaml
object_storage:
  backend: s3                  # s3 | rustfs | minio | gcs | azure
  endpoint: localhost:32900    # S3 server host:port (rustfs:9000 in Docker)
  region: us-east-1
  bucket: pulse-media
  access_key: pulse
  secret_key: pulsepulse
  public_url: http://localhost:32900/pulse-media
  secure: false                # true for HTTPS / production AWS S3
```

- **Environment Override**: `PULSE_S3_ENDPOINT`, `PULSE_S3_BUCKET`, `PULSE_S3_ACCESS_KEY`, `PULSE_S3_SECRET_KEY`, `PULSE_S3_PUBLIC_URL`.

