# Hướng dẫn Đóng góp Mã nguồn

Cảm ơn bạn đã quan tâm và muốn đóng góp cho dự án Pulse! Chúng tôi luôn hoan nghênh các đóng góp từ cộng đồng: từ báo cáo lỗi (issues), cải thiện tài liệu, sửa lỗi nhỏ (bug fixes) đến phát triển tính năng mới.

---

## Bộ Quy tắc Ứng xử (Code of Conduct)

Chúng tôi cam kết tạo ra một môi trường cởi mở, thân thiện và tôn trọng lẫn nhau cho tất cả mọi người. Vui lòng giữ thái độ lịch sự, chuyên nghiệp và có tính xây dựng trong mọi thảo luận.

---

## Thiết lập Môi trường Phát triển

1. Fork và clone repository về máy của bạn.
2. Khởi chạy các dịch vụ phụ thuộc bằng Docker:
   ```bash
   docker compose up -d postgres nats rustfs
   ```
3. Chạy migration cơ sở dữ liệu:
   ```bash
   make migrate-up
   ```
4. Chạy kiểm thử tự động:
   ```bash
   make test-unit
   ```

---

## Tiêu chuẩn Viết Mã (Code Standards)

- **Định dạng code**: Luôn format mã nguồn bằng lệnh `go fmt` hoặc `make fmt`.
- **Kiểm tra linter**: Đảm bảo mã nguồn vượt qua kiểm tra của `golangci-lint run ./...` (`make lint`).
- **Chú thích (Comments)**:
  - Viết Go doc comment rõ ràng cho tất cả các struct, interface và hàm public (exported).
  - Comment cần tập trung mô tả **chức năng** của hàm, logic nghiệp vụ, dữ liệu đầu vào (inputs) và kết quả trả về (outputs).
  - Tuyệt đối không nhắc đến các thông tin quản trị dự án nội bộ (như số phase, sprint ticket hay mục tiêu nội bộ) trong comment.
- **Kiểm thử (Testing)**:
  - Bổ sung unit test cho bất kỳ logic nghiệp vụ mới nào.
  - Các bài kiểm thử yêu cầu hạ tầng thật phải có build tag rõ ràng (ví dụ `//go:build integration` hoặc `//go:build contract`).

---

## Quy trình Gửi Pull Request

1. Tạo nhánh tính năng mới: `git checkout -b feature/ten-tinh-nang`.
2. Commit các thay đổi với thông điệp rõ ràng theo chuẩn Semantic:
   - `feat: them bo loc phat tin broadcast presence`
   - `fix: xu ly tranh chap trong bo dem sequence counter`
   - `docs: cap nhat bang ma frame websocket`
3. Push nhánh lên repo fork của bạn và mở Pull Request hướng về nhánh `main`.

