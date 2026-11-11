# Giao thức WebSocket Pulse

Các ứng dụng client kết nối vào WebSocket Gateway tại cổng `32001` để trao đổi tin nhắn thời gian thực hai chiều. Giao thức sử dụng định dạng nhị phân **MsgPack** nhằm tối ưu hóa băng thông truyền tải và độ trễ xử lý.

---

## Bắt tay Kết nối (Handshake Sequence)

- **Địa chỉ kết nối**: `ws://<HOST>:32001` (hoặc `wss://...` qua TLS)
- **Subprotocol**: `pulse.protocol.v1.msgpack`
- **Xác thực**: Đính kèm JWT token qua HTTP header `Authorization: Bearer <userToken>` khi upgrade kết nối.

```
Client                                                  Gateway
  │                                                        │
  │─── Nâng cấp kết nối WS (Bearer <userToken>) ──────────▶│
  │◀── 101 Switching Protocols ────────────────────────────│
  │                                                        │
  │─── FrameHello (1) ────────────────────────────────────▶│
  │◀── FrameWelcome (2) [session_id, cell_id] ─────────────│
  │                                                        │
  │─── FrameSendMessage (10) ─────────────────────────────▶│
  │◀── FrameMessageAck (11) [serverMsgID, seq] ────────────│
  │◀── FrameMessageRecv (12) [đẩy tới người nhận] ─────────│
  │                                                        │
  │─── FramePing (80) ────────────────────────────────────▶│
  │◀── FramePong (81) ─────────────────────────────────────│
```

---

## Danh mục Mã Frame

Các mã định dạng frame được định nghĩa trong [`pkg/protocol/frames.go`](file:///Users/seang/Documents/pulse-workspace/pulse/pkg/protocol/frames.go):

| Mã | Tên Frame | Hướng | Mô tả |
| :--- | :--- | :--- | :--- |
| `1` | `FrameHello` | C → S | Frame chào mở đầu gửi ngay sau khi kết nối thành công |
| `2` | `FrameWelcome` | S → C | Gateway phản hồi phiên làm việc với `sessionID` và `cellID` |
| `3` | `FrameSubscribe` | C → S | Đăng ký theo dõi luồng sự kiện (hội thoại, presence) |
| `4` | `FrameUnsubscribe` | C → S | Huỷ đăng ký luồng sự kiện |
| `10` | `FrameSendMessage` | C → S | Gửi tin nhắn mới vào một cuộc hội thoại |
| `11` | `FrameMessageAck` | S → C | Xác nhận từ Gateway rằng tin nhắn đã ghi nhận với `serverMsgID` và `seq` |
| `12` | `FrameMessageRecv` | S → C | Đẩy tin nhắn mới đến các client người nhận |
| `13` | `FrameMessageRead` | S → C | Thông báo tin nhắn đã được đối phương đọc |
| `14` | `FrameMarkRead` | C → S | Đánh dấu đã đọc tới vị trí tin nhắn có `seq` cụ thể |
| `20` | `FrameTypingStart` | C → S | Bắt đầu nhập văn bản trong cuộc hội thoại |
| `21` | `FrameTypingStop` | C → S | Ngừng nhập văn bản |
| `30` | `FrameReactionAdd` | C → S | Thả biểu cảm cảm xúc (reaction) vào tin nhắn |
| `31` | `FrameReactionDel` | C → S | Gỡ bỏ biểu cảm cảm xúc đã thả |
| `32` | `FrameReactionUpd` | S → C | Thông báo cập nhật biểu cảm đến mọi người trong hội thoại |
| `40` | `FramePresenceUpd` | S → C | Trạng thái online/offline của bạn bè hoặc thành viên nhóm |
| `50` | `FrameConvUpd` | S → C | Cập nhật thông tin hội thoại |
| `60` | `FrameGroupUpd` | S → C | Cập nhật thông tin hoặc cài đặt nhóm |
| `61` | `FrameGroupMember` | S → C | Thay đổi thành viên (tham gia / rời đi / bổ nhiệm) |
| `80` | `FramePing` | C → S | Gói tin giữ kết nối (khuyến nghị chu kỳ 10s - 30s) |
| `81` | `FramePong` | S → C | Phản hồi pong từ Gateway |
| `100` | `FrameError` | S → C | Thông báo lỗi nghiệp vụ với mã lỗi và nội dung chi tiết |
| `101` | `FrameResync` | S → C | Yêu cầu client đồng bộ lại toàn bộ trạng thái sau ngắt kết nối |
| `102` | `FrameHandoffReq` | C → S | Yêu cầu tiếp quản phiên khi kết nối tới node Gateway mới |
| `103` | `FrameHandoffReady` | S → C | Bản chụp trạng thái từ Gateway cũ đã sẵn sàng |
| `104` | `FrameHandoffDone` | Cả hai | Quá trình bàn giao phiên hoàn tất không mất tin nhắn |

