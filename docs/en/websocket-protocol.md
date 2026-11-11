# WebSocket Protocol Reference

Pulse clients connect to the WebSocket Gateway on port `32001` for bidirectional real-time communications. The protocol uses **MsgPack** binary serialization to minimize network overhead and latency.

---

## Connection Handshake

- **Endpoint**: `ws://<HOST>:32001` (or `wss://...` over TLS)
- **Subprotocol**: `pulse.protocol.v1.msgpack`
- **Authentication**: Provided via HTTP `Authorization: Bearer <userToken>` during upgrade.

```
Client                                                  Gateway
  │                                                        │
  │─── WS Upgrade (Bearer <userToken>) ───────────────────▶│
  │◀── 101 Switching Protocols ────────────────────────────│
  │                                                        │
  │─── FrameHello (1) ────────────────────────────────────▶│
  │◀── FrameWelcome (2) [session_id, cell_id] ─────────────│
  │                                                        │
  │─── FrameSendMessage (10) ─────────────────────────────▶│
  │◀── FrameMessageAck (11) [serverMsgID, seq] ────────────│
  │◀── FrameMessageRecv (12) [pushed to recipients] ───────│
  │                                                        │
  │─── FramePing (80) ────────────────────────────────────▶│
  │◀── FramePong (81) ─────────────────────────────────────│
```

---

## Wire Frame Types

Frame codes are defined in [`pkg/protocol/frames.go`](file:///Users/seang/Documents/pulse-workspace/pulse/pkg/protocol/frames.go):

| Code | Name | Direction | Description |
| :--- | :--- | :--- | :--- |
| `1` | `FrameHello` | C → S | Initial client hello frame sent after upgrade |
| `2` | `FrameWelcome` | S → C | Gateway welcome response carrying `sessionID` and assigned `cellID` |
| `3` | `FrameSubscribe` | C → S | Subscribe to event stream (conversation, presence) |
| `4` | `FrameUnsubscribe` | C → S | Unsubscribe from event stream |
| `10` | `FrameSendMessage` | C → S | Dispatch an outbound message to a conversation |
| `11` | `FrameMessageAck` | S → C | Gateway confirmation of message persistence with `serverMsgID` and `seq` |
| `12` | `FrameMessageRecv` | S → C | Real-time inbound message dispatch to recipients |
| `13` | `FrameMessageRead` | S → C | Notification that a recipient marked messages as read |
| `14` | `FrameMarkRead` | C → S | Client signals read status up to a specific `seq` |
| `20` | `FrameTypingStart` | C → S | Client begins typing in a conversation |
| `21` | `FrameTypingStop` | C → S | Client stops typing |
| `30` | `FrameReactionAdd` | C → S | Add reaction emoji to an existing message |
| `31` | `FrameReactionDel` | C → S | Remove previously added reaction |
| `32` | `FrameReactionUpd` | S → C | Broadcast reaction update to conversation participants |
| `40` | `FramePresenceUpd` | S → C | Online/offline status updates for friends/members |
| `50` | `FrameConvUpd` | S → C | Conversation metadata update |
| `60` | `FrameGroupUpd` | S → C | Group details or settings update |
| `61` | `FrameGroupMember` | S → C | Group member join/kick/role update |
| `80` | `FramePing` | C → S | Client keepalive ping (recommended interval: 10s-30s) |
| `81` | `FramePong` | S → C | Gateway pong response |
| `100` | `FrameError` | S → C | Error frame with numeric error code and descriptive message |
| `101` | `FrameResync` | S → C | Signal requiring full state resynchronization |
| `102` | `FrameHandoffReq` | C → S | Request session takeover by new gateway instance |
| `103` | `FrameHandoffReady` | S → C | Prior gateway state snapshot prepared |
| `104` | `FrameHandoffDone` | Both | Session handoff completed with zero frame loss |

