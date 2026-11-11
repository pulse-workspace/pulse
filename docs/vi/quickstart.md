# Hướng dẫn Khởi chạy Nhanh

Tài liệu này hướng dẫn bạn cài đặt các thành phần phụ thuộc, biên dịch mã nguồn, chạy migration cơ sở dữ liệu và thực hiện kiểm thử nhanh (smoke test) hệ thống Pulse trên máy cục bộ.

---

## Yêu cầu Môi trường

- **Go**: Phiên bản 1.22 trở lên
- **Docker & Docker Compose**: v2+
- **Make**: GNU Make tiêu chuẩn
- **wscat** hoặc bất kỳ công cụ WebSocket client nào (tuỳ chọn, để kiểm thử kết nối)

---

## 1. Khởi chạy Hạ tầng Phụ thuộc

Pulse yêu cầu PostgreSQL 16 và NATS JetStream. Bạn có thể khởi động cả hai thông qua `docker-compose.yaml` ở thư mục gốc:

```bash
docker compose up -d postgres nats rustfs
```

Kiểm tra trạng thái sẵn sàng của các container:
```bash
docker compose ps
```

---

## 2. Biên dịch File Nhị phân Pulse

Tải các thư viện phụ thuộc của Go và biên dịch:

```bash
make deps
make build
```

File thực thi sau khi biên dịch sẽ nằm tại `./bin/pulse`.

---

## 3. Chạy Database Migrations

Khởi tạo cấu trúc bảng trên PostgreSQL:

```bash
make migrate-up
```

---

## 4. Chạy Pulse ở Chế độ Phát triển (Dev Mode)

Khởi động đồng thời Domain Cell và WebSocket Gateway trong một tiến trình duy nhất:

```bash
./bin/pulse dev --config configs/dev-localhost.yaml
```

Kiểm tra trạng thái hoạt động:
```bash
curl http://localhost:32011/healthz
```

Kết quả trả về:
```json
{"status":"ok"}
```

---

## 5. Kiểm thử Luồng Hoạt động (Smoke Test)

### A. Khởi tạo Tài khoản Quản trị viên
```bash
ADMIN_TOKEN=$(curl -s -X POST http://localhost:32011/api/v1/admin/init \
  -H "Content-Type: application/json" \
  -d '{"secret":"dev-secret-change-me-in-production-32chars","adminUserID":"admin","adminPassword":"Pulse@Admin1"}' \
  | jq -r .data.adminToken)
```

### B. Đăng ký Người dùng Mới
```bash
curl -s -X POST http://localhost:32011/api/v1/user/register \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"userID":"alice","nickname":"Alice","email":"alice@example.com"}'
```

### C. Cấp Token Đăng nhập cho Client
```bash
USER_TOKEN=$(curl -s -X POST http://localhost:32011/api/v1/auth/userToken \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"userID":"alice","platform":"WEB"}' \
  | jq -r .data.token)
```

### D. Kết nối vào WebSocket Gateway
Kết nối trực tiếp tới cổng WebSocket bằng subprotocol `pulse.protocol.v1.msgpack`:

```bash
wscat -c ws://localhost:32001 -s pulse.protocol.v1.msgpack \
  -H "Authorization: Bearer $USER_TOKEN"
```

