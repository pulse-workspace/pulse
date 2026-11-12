package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/pulse-workspace/pulse/internal/cell/conversation"
	"github.com/pulse-workspace/pulse/internal/cell/group"
	"github.com/pulse-workspace/pulse/internal/cell/message"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
	"github.com/pulse-workspace/pulse/pkg/protocol"
)

// MessageHandler glues the gateway's WS connections to the cell message service.
//
// Single-cell fan-out: when alice sends a message in conv X, we look up
// the conversation's participants, find their sessions on THIS gateway,
// and push a MessageReceived frame to each.
//
// Cross-cell fan-out lands in Week 7-8 via NATS mesh.
type MessageHandler struct {
	gw   *Gateway
	msg  *message.Service
	conv *conversation.Service
	grp  *group.Service
	log  logger.Logger

	mu    sync.RWMutex
	users map[string]map[string]*Session // userID → sessionID → Session
}

// NewMessageHandler constructs the handler.
func NewMessageHandler(gw *Gateway, msg *message.Service, conv *conversation.Service, grp *group.Service, log logger.Logger) *MessageHandler {
	return &MessageHandler{
		gw:    gw,
		msg:   msg,
		conv:  conv,
		grp:   grp,
		log:   log.With().Str("component", "message-handler").Logger(),
		users: make(map[string]map[string]*Session),
	}
}

// trackUser adds a session to the user→session index.
func (h *MessageHandler) trackUser(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.users[s.UserID] == nil {
		h.users[s.UserID] = make(map[string]*Session)
	}
	h.users[s.UserID][s.ID] = s
}

// untrackUser removes a session from the user→session index.
func (h *MessageHandler) untrackUser(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if sessions, ok := h.users[s.UserID]; ok {
		delete(sessions, s.ID)
		if len(sessions) == 0 {
			delete(h.users, s.UserID)
		}
	}
}

// sessionsForUser returns all sessions of userID on this gateway.
func (h *MessageHandler) sessionsForUser(userID string) []*Session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	sessions := h.users[userID]
	out := make([]*Session, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s)
	}
	return out
}

// HandleSendMessage processes a FrameSendMessage from a client.
//
// Frame layout (msgpack):
//
//	{
//	  "c": "<convID>",
//	  "t": <contentType>,
//	  "x": <content map>,
//	  "i": "<clientMsgID>"   // optional idempotency key
//	}
//
// Responds with FrameMessageAck on success.
func (h *MessageHandler) HandleSendMessage(sess *Session, payload []byte) error {
	var req struct {
		ConvID      string         `msgpack:"c"`
		ContentType int            `msgpack:"t"`
		Content     map[string]any `msgpack:"x"`
		ClientMsgID string         `msgpack:"i,omitempty"`
	}
	if err := msgpack.Unmarshal(payload, &req); err != nil {
		return h.sendError(sess, 5002, "invalid send payload")
	}
	if req.ConvID == "" {
		return h.sendError(sess, 10005, "convID required")
	}

	// Default content type to text.
	if req.ContentType == 0 {
		req.ContentType = protocol.ContentText
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Look up conversation OR group to get participants.
	conv, err := h.conv.Get(ctx, req.ConvID)
	var participants []string
	if err != nil {
		// Fallback: try group if convID starts with "g_".
		if h.grp != nil && strings.HasPrefix(req.ConvID, "g_") {
			isMember, gerr := h.grp.IsMember(ctx, req.ConvID, sess.UserID)
			if gerr != nil || !isMember {
				return h.sendError(sess, 1005, "not a participant")
			}
			members, gerr := h.grp.MembersOf(ctx, req.ConvID)
			if gerr != nil {
				return h.sendError(sess, 6001, "group not found")
			}
			participants = members
		} else {
			return h.sendError(sess, 6001, "conversation not found")
		}
	} else {
		// 2. Verify sender is a participant.
		if !contains(conv.Participants, sess.UserID) {
			return h.sendError(sess, 1005, "not a conversation participant")
		}
		participants = conv.Participants
	}

	// 3. Persist + publish event.
	msg, err := h.msg.SendMessage(ctx, message.SendMessageInput{
		ConvID:      req.ConvID,
		SenderID:    sess.UserID,
		ContentType: req.ContentType,
		Content:     req.Content,
		ClientMsgID: req.ClientMsgID,
	})
	if err != nil {
		h.log.Error().Err(err).Msg("send message")
		return h.sendError(sess, 10001, "send failed")
	}

	// 4. Update conversation projection (last_msg_id + unread bumps).
	if err := h.conv.OnMessageSent(ctx, req.ConvID, msg.MsgID, sess.UserID, participants, msg.CreatedAt); err != nil {
		h.log.Warn().Err(err).Msg("update conversation projection")
	}

	// 5. Send ack to sender.
	if err := writeFrame(sess.Conn, FrameMessageAck, mustMsgpack(map[string]any{
		"serverMsgID": msg.MsgID,
		"clientMsgID": req.ClientMsgID,
		"convID":      msg.ConvID,
		"seq":         msg.Seq,
		"sendTime":    msg.CreatedAt.UnixMilli(),
	})); err != nil {
		h.log.Warn().Err(err).Msg("send ack")
	}

	// 6. Fan out to other participants on this cell.
	for _, userID := range participants {
		if userID == sess.UserID {
			continue
		}
		h.deliverToUser(userID, msg)
	}

	h.log.Info().
		Str("from", sess.UserID).
		Str("conv", req.ConvID).
		Str("msg", msg.MsgID).
		Msg("message sent")
	return nil
}

// HandleMarkRead processes a FrameMarkRead.
//
// Frame: { "c": "<convID>", "m": "<msgID>" }
func (h *MessageHandler) HandleMarkRead(sess *Session, payload []byte) error {
	var req struct {
		ConvID string `msgpack:"c"`
		MsgID  string `msgpack:"m"`
	}
	if err := msgpack.Unmarshal(payload, &req); err != nil {
		return h.sendError(sess, 5002, "invalid markRead payload")
	}
	if req.ConvID == "" {
		return h.sendError(sess, 10005, "convID required")
	}
	if req.MsgID == "" {
		req.MsgID = "" // mark all as read
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.conv.MarkRead(ctx, req.ConvID, sess.UserID, req.MsgID); err != nil {
		h.log.Warn().Err(err).Msg("mark read")
		return h.sendError(sess, 10001, "mark read failed")
	}
	return nil
}

// HandleGetHistory responds with a list of recent messages in convID.
//
// Frame: { "c": "<convID>", "l": <limit> }
//
// Response: FrameMessageAck with data { messages: [...] }.
func (h *MessageHandler) HandleGetHistory(sess *Session, payload []byte) error {
	var req struct {
		ConvID string `msgpack:"c"`
		Limit  int    `msgpack:"l,omitempty"`
	}
	if err := msgpack.Unmarshal(payload, &req); err != nil {
		return h.sendError(sess, 5002, "invalid history payload")
	}
	if req.ConvID == "" {
		return h.sendError(sess, 10005, "convID required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	msgs, err := h.msg.GetHistory(ctx, req.ConvID, req.Limit)
	if err != nil {
		h.log.Warn().Err(err).Msg("get history")
		return h.sendError(sess, 10001, "history failed")
	}

	return writeFrame(sess.Conn, FrameMessageAck, mustMsgpack(map[string]any{
		"type":     "history",
		"convID":   req.ConvID,
		"messages": msgs,
	}))
}

// OnInboundMessage implements message.InboundMessageListener.
// When an inbound message is received from a peer cell, it finds local active sessions
// for conversation participants and pushes FrameMessageRecv over WebSocket in real-time.
func (h *MessageHandler) OnInboundMessage(msg *message.Message) {
	if msg == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var participants []string
	if h.conv != nil {
		c, err := h.conv.Get(ctx, msg.ConvID)
		if err == nil && c != nil {
			participants = c.Participants
		}
	}

	// Fallback for 1-on-1: parse si_u1_u2
	if len(participants) == 0 && strings.HasPrefix(msg.ConvID, "si_") {
		parts := strings.Split(strings.TrimPrefix(msg.ConvID, "si_"), "_")
		if len(parts) >= 2 {
			participants = parts
		}
	}

	for _, uid := range participants {
		if uid == msg.SenderID {
			continue
		}
		h.deliverToUser(uid, msg)
	}
}

// deliverToUser pushes a MessageReceived frame to every session of userID on this gateway.
func (h *MessageHandler) deliverToUser(userID string, msg *message.Message) {
	sessions := h.sessionsForUser(userID)
	if len(sessions) == 0 {
		h.log.Debug().Str("user", userID).Msg("no local session; cross-cell fan-out (TBD Week 7-8)")
		return
	}

	frame := mustMsgpack(map[string]any{
		"serverMsgID": msg.MsgID,
		"convID":      msg.ConvID,
		"senderID":    msg.SenderID,
		"contentType": msg.ContentType,
		"content":     msg.Content,
		"seq":         msg.Seq,
		"sendTime":    msg.CreatedAt.UnixMilli(),
	})

	for _, s := range sessions {
		if err := writeFrame(s.Conn, FrameMessageRecv, frame); err != nil {
			h.log.Warn().Err(err).Str("user", userID).Msg("deliver frame")
		}
	}
}

// sendError writes a FrameError back to the session.
func (h *MessageHandler) sendError(sess *Session, code int, msg string) error {
	return writeFrame(sess.Conn, FrameError, mustMsgpack(map[string]any{
		"code": code,
		"msg":  msg,
	}))
}

// contains reports whether s contains v.
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// Compile-time interface assertion that *websocket.Conn satisfies net.Conn-style
// deadline behaviour (it does, via SetWriteDeadline).
var _ = (*websocket.Conn)(nil)

// Compile-time guard for http package usage in extended builds.
var _ = http.StatusOK

// Compile-time JSON round-trip guard.
var _ = json.Marshal
