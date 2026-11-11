# Hướng dẫn Cấu hình Hệ thống

Pulse có thể được cấu hình linh hoạt thông qua các file định dạng YAML đặt trong thư mục `configs/` hoặc ghi đè thông qua các biến môi trường (Environment Variables). Biến môi trường luôn có độ ưu tiên cao hơn giá trị trong file YAML.

---

## Các File Cấu hình Chính

- `configs/dev.yaml`: Dùng cho môi trường phát triển với cổng và địa chỉ mặc định cục bộ.
- `configs/dev-localhost.yaml`: Tối ưu hóa để chạy trực tiếp trên máy không dùng Docker.
- `configs/prod.yaml`: Hồ sơ cấu hình chuẩn production với các thiết lập bảo mật và TLS.

---

## Các Khối Cấu hình Quan trọng

### 1. Cơ sở Dữ liệu (`postgres`)

```yaml
postgres:
  host: localhost
  port: 32432              # Cổng dev cục bộ (32432 tránh xung đột với Postgres mặc định cổng 5432/15432)
  database: pulse
  user: pulse
  password: pulse_password
  ssl_mode: disable
  max_conns: 50
  min_conns: 2
```

- **Biến môi trường tương ứng**: `PULSE_DB_HOST`, `PULSE_DB_PORT`, `PULSE_DB_USER`, `PULSE_DB_PASSWORD`, `PULSE_DB_NAME` hoặc `DATABASE_URL`.

### 2. NATS JetStream (`nats`)

```yaml
nats:
  url: nats://localhost:32222  # Cổng dev cục bộ (32222 tránh xung đột với cổng mặc định 4222)
  cluster_id: pulse-cluster
  cell_id: cell-0
  stream_prefix: pulse
```

- **Biến môi trường tương ứng**: `PULSE_NATS_URL`, `PULSE_NATS_CLUSTER_ID`.

### 3. Gateway & Cổng Dịch vụ

```yaml
server:
  api_port: 32011          # Cổng HTTP REST API
  gateway_port: 32001      # Cổng WebSocket Gateway
  metrics_port: 9090      # Endpoint Prometheus /metrics
  read_timeout: 15s
  write_timeout: 15s
```

- **Biến môi trường tương ứng**: `PULSE_API_PORT`, `PULSE_GATEWAY_PORT`.

### 4. Xác thực & JWT (`auth`)

```yaml
auth:
  jwt_secret: "chuoi-khoa-bi-mat-jwt-an-toan-toi-thieu-32-ky-tu"
  token_expiry: 24h
  refresh_expiry: 720h
```

- **Biến môi trường tương ứng**: `PULSE_JWT_SECRET`, `PULSE_TOKEN_EXPIRY`.

### 5. Lưu trữ Tệp & Đa phương tiện (`object_storage`)

Pulse giao tiếp với bất kỳ hạ tầng Object Storage tương thích chuẩn S3 nào (AWS S3, MinIO, Cloudflare R2, RustFS). Ở môi trường phát triển cục bộ, container `rustfs` đóng vai trò là S3 server tại cổng 32900 (tránh xung đột với cổng 9000 mặc định).

```yaml
object_storage:
  backend: s3                  # s3 | rustfs | minio | gcs | azure
  endpoint: localhost:32900    # Địa chỉ S3 server (hoặc rustfs:9000 khi chạy Docker)
  region: us-east-1
  bucket: pulse-media
  access_key: pulse
  secret_key: pulsepulse
  public_url: http://localhost:32900/pulse-media
  secure: false                # bật true nếu dùng HTTPS / AWS S3 thật
```

- **Biến môi trường tương ứng**: `PULSE_S3_ENDPOINT`, `PULSE_S3_BUCKET`, `PULSE_S3_ACCESS_KEY`, `PULSE_S3_SECRET_KEY`, `PULSE_S3_PUBLIC_URL`.

