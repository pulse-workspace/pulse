# Danh mục REST API

Pulse cung cấp giao diện REST API (JSON qua HTTP) để quản lý xác thực, tài khoản người dùng, nhóm chat, hội thoại, đồ thị quan hệ bạn bè và cấu hình quản trị cụm. Mọi endpoint đều có tiền tố `/api/v1`.

---

## Địa chỉ Cơ sở (Base URL)

- **Môi trường phát triển cục bộ**: `http://localhost:32011/api/v1`
- **Kiểm tra trạng thái (Health Check)**: `http://localhost:32011/healthz`

---

## Cơ chế Xác thực

Mọi yêu cầu gửi đến API (ngoại trừ `/admin/init` và `/healthz`) phải đính kèm JWT Bearer token trong header `Authorization`:

```http
Authorization: Bearer <TOKEN>
```

- **Admin Token**: Được cấp qua `/admin/init` hoặc `/admin/login`, có toàn quyền quản trị hệ thống.
- **User Token**: Được cấp qua `/auth/userToken`, giới hạn quyền theo `userID` của người dùng.

---

## Danh mục Endpoints

### 1. Quản trị & Xác thực (Admin & Auth)

| Phương thức | Đường dẫn | Mô tả | Quyền hạn |
| :--- | :--- | :--- | :--- |
| `GET` | `/healthz` | Kiểm tra trạng thái hoạt động của hệ thống | Công khai |
| `GET` | `/metrics` | Thu thập metrics định dạng Prometheus | Công khai |
| `POST` | `/api/v1/admin/init` | Khởi tạo tài khoản quản trị viên gốc | Cần khoá bí mật (Secret key) |
| `POST` | `/api/v1/admin/login` | Đăng nhập tài khoản admin để nhận token | Công khai |
| `GET` | `/api/v1/admin/health` | Kiểm tra trạng thái admin | Admin |
| `POST` | `/api/v1/auth/userToken` | Cấp phát token cho người dùng đăng nhập client | Admin |
| `POST` | `/api/v1/auth/login` | Đăng nhập người dùng | Công khai |
| `POST` | `/api/v1/auth/logout` | Thu hồi và huỷ bỏ phiên làm việc | User |

### 2. Quản lý Người dùng (Users)

| Phương thức | Đường dẫn | Mô tả | Quyền hạn |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/user/register` | Đăng ký người dùng mới | Admin |
| `GET` | `/api/v1/user/self` | Lấy thông tin tài khoản hiện tại | User |
| `GET` | `/api/v1/user/get?userID=...` | Tra cứu thông tin người dùng theo ID | User |

### 3. Hội thoại (Conversations)

| Phương thức | Đường dẫn | Mô tả | Quyền hạn |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/conversation/create` | Tạo hoặc lấy hội thoại 1-1 hoặc nhóm | User |
| `GET` | `/api/v1/conversation/list` | Lấy danh sách hội thoại của người dùng | User |

### 4. Nhóm Trò chuyện (Groups)

| Phương thức | Đường dẫn | Mô tả | Quyền hạn |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/group/create` | Tạo một nhóm trò chuyện mới | User |
| `GET` | `/api/v1/group/{groupID}` | Lấy chi tiết thông tin nhóm | User |
| `GET` | `/api/v1/group/{groupID}/members` | Danh sách thành viên trong nhóm | User |
| `POST` | `/api/v1/group/{groupID}/invite` | Mời thêm thành viên vào nhóm | User |
| `POST` | `/api/v1/group/{groupID}/join` | Tham gia vào nhóm công khai | User |
| `POST` | `/api/v1/group/{groupID}/kick` | Xoá thành viên khỏi nhóm | Quản trị nhóm |
| `POST` | `/api/v1/group/{groupID}/quit` | Tự rời khỏi nhóm | User |
| `POST` | `/api/v1/group/{groupID}/dismiss` | Giải tán nhóm trò chuyện | Chủ nhóm |

### 5. Quan hệ Bạn bè (Friends)

| Phương thức | Đường dẫn | Mô tả | Quyền hạn |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/friend/request` | Gửi lời mời kết bạn | User |
| `POST` | `/api/v1/friend/accept` | Chấp nhận lời mời kết bạn | User |
| `POST` | `/api/v1/friend/decline` | Từ chối lời mời kết bạn | User |
| `GET` | `/api/v1/friend/` | Danh sách bạn bè hiện tại | User |
| `GET` | `/api/v1/friend/requests/incoming` | Danh sách lời mời kết bạn gửi đến | User |
| `GET` | `/api/v1/friend/requests/outgoing` | Danh sách lời mời kết bạn đã gửi | User |
| `DELETE` | `/api/v1/friend/{friendID}` | Huỷ kết bạn | User |

### 6. Đính kèm Đa phương tiện (Media)

| Phương thức | Đường dẫn | Mô tả | Quyền hạn |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/media/upload` | Tải lên tệp đính kèm (multipart) | User |
| `GET` | `/api/v1/media/` | Danh sách tệp đính kèm của tôi | User |
| `GET` | `/api/v1/media/{attachmentID}` | Lấy thông tin tệp / link tải pre-signed | User |
| `DELETE` | `/api/v1/media/{attachmentID}` | Xoá tệp đính kèm | User |

### 7. Tìm kiếm (Search)

| Phương thức | Đường dẫn | Mô tả | Quyền hạn |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/search/messages?q=...` | Tìm kiếm toàn văn trong lịch sử tin nhắn | User |

