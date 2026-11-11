// Package gateway terminates WebSocket connections from SDK clients and
// translates frames into events on the appropriate cell stream.
//
// Key capability: Pulse gateways support session handoff
// across gateways (no sticky session required) and use msgpack for high-efficiency
// binary wire encoding.
package gateway

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/pulse-im/pulse/pkg/shared/authn"
	"github.com/pulse-im/pulse/pkg/shared/config"
	"github.com/pulse-im/pulse/pkg/shared/logger"
	"github.com/pulse-im/pulse/pkg/shared/nats"
)

// Frame types (see pkg/protocol/frames.go for the canonical list).
const (
	FrameHello        uint16 = 1
	FrameWelcome      uint16 = 2
	FrameSendMessage  uint16 = 10
	FrameMessageAck   uint16 = 11
	FrameMessageRecv  uint16 = 12
	FrameMarkRead     uint16 = 14
	FrameGetHistory   uint16 = 15
	FramePing         uint16 = 80
	FramePong         uint16 = 81
	FrameError        uint16 = 100
	FrameHandoffReq   uint16 = 102
	FrameHandoffReady uint16 = 103
	FrameHandoffDone  uint16 = 104
)

// Frame wraps any outbound payload.
type Frame struct {
	Type    uint16 `msgpack:"t"`
	Payload []byte `msgpack:"p,omitempty"`
}

// SessionRegistrar allows the gateway to register/unregister user session locations in the distributed directory.
type SessionRegistrar interface {
	Register(ctx context.Context, userID, sessionID, cellID string) error
	Unregister(ctx context.Context, userID, sessionID string) error
}

// Gateway is the WS endpoint.
type Gateway struct {
	cfg    *config.Config
	log    logger.Logger
	issuer *authn.Issuer
	nats   *nats.Client
	msgs   *MessageHandler
	dir    SessionRegistrar

	mu    sync.RWMutex
	conns map[string]*Session // sessionID → Session
}

// Session represents a single WebSocket connection.
type Session struct {
	ID        string
	UserID    string
	Platform  string
	DeviceID  string
	Conn      *websocket.Conn
	cellID    string
	createdAt time.Time
	lastPing  time.Time
}

// New constructs a Gateway.
func New(cfg *config.Config, log logger.Logger, issuer *authn.Issuer, n *nats.Client) *Gateway {
	return &Gateway{
		cfg:    cfg,
		log:    log.With().Str("component", "gateway").Logger(),
		issuer: issuer,
		nats:   n,
		conns:  make(map[string]*Session),
	}
}

// AttachMessageHandler wires a MessageHandler to the gateway.
// Must be called before Listen().
func (g *Gateway) AttachMessageHandler(m *MessageHandler) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.msgs = m
}

// Listen starts the HTTP server with WS upgrade handler.
func (g *Gateway) Listen(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", g.handleWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	srv := &http.Server{
		Addr:              g.cfg.GW.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	g.log.Info().Str("addr", srv.Addr).Msg("gateway listening")

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// upgrader is the gorilla/websocket upgrader configured for Pulse.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	Subprotocols:    []string{"pulse.protocol.v1.msgpack"},
	CheckOrigin:     checkOrigin,
}

// checkOrigin validates the Origin header against an allowlist.
//
// In dev mode (PULSE_DEV_MODE=1 or absence of PULSE_ALLOWED_ORIGINS),
// it allows all origins (preserves the existing local-dev ergonomics).
//
// In prod, set PULSE_ALLOWED_ORIGINS to a comma-separated list of
// exact origins (scheme + host + optional port). Examples:
//
//	PULSE_ALLOWED_ORIGINS=https://app.pulse.example.com,https://admin.pulse.example.com
//
// WebSocket connections without an Origin header (e.g. native clients)
// are allowed by default — they don't suffer from the same CSRF class.
var allowedOrigins = parseAllowedOrigins()

func parseAllowedOrigins() map[string]struct{} {
	v := os.Getenv("PULSE_ALLOWED_ORIGINS")
	if v == "" {
		return nil
	}
	out := make(map[string]struct{})
	for _, o := range strings.Split(v, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			out[strings.ToLower(o)] = struct{}{}
		}
	}
	return out
}

func checkOrigin(r *http.Request) bool {
	if os.Getenv("PULSE_DEV_MODE") == "1" {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // native client, no Origin header
	}
	if len(allowedOrigins) == 0 {
		// Prod mode but no allowlist configured → strict default deny
		// to avoid silent open-proxy behaviour.
		return false
	}
	_, ok := allowedOrigins[strings.ToLower(origin)]
	return ok
}

// handleWS upgrades the HTTP request and starts a session loop.
func (g *Gateway) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		g.log.Warn().Err(err).Msg("ws upgrade failed")
		return
	}

	// First frame must be Hello with auth token.
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	helloType, payload, err := readFrame(conn)
	if err != nil {
		g.log.Warn().Err(err).Msg("read hello failed")
		_ = conn.Close()
		return
	}
	if helloType != FrameHello {
		g.log.Warn().Uint16("type", helloType).Msg("expected hello")
		_ = conn.Close()
		return
	}

	claims, err := g.parseHello(payload)
	if err != nil {
		g.log.Warn().Err(err).Msg("hello parse")
		_ = writeError(conn, 1001, "invalid token")
		_ = conn.Close()
		return
	}

	sess := &Session{
		ID:        claims.SessionID(),
		UserID:    claims.UserID,
		Platform:  claims.Platform,
		DeviceID:  claims.DeviceID,
		Conn:      conn,
		cellID:    g.cfg.Cell.ID,
		createdAt: time.Now().UTC(),
		lastPing:  time.Now().UTC(),
	}
	g.register(sess)

	// Send Welcome
	if err := writeFrame(conn, FrameWelcome, mustMsgpack(map[string]any{
		"session_id":  sess.ID,
		"user_id":     sess.UserID,
		"cell_id":     sess.cellID,
		"server_time": time.Now().UTC(),
	})); err != nil {
		g.log.Warn().Err(err).Msg("welcome write")
		g.unregister(sess)
		_ = conn.Close()
		return
	}

	// Track in the message-handler's user index for fan-out.
	if g.msgs != nil {
		g.msgs.trackUser(sess)
	}

	go g.readLoop(sess)
}

// HelloPayload is the first frame after upgrade.
type HelloPayload struct {
	UserToken string `msgpack:"t"`
	Platform  string `msgpack:"p,omitempty"`
	DeviceID  string `msgpack:"d,omitempty"`
}

func (g *Gateway) parseHello(payload []byte) (*authn.Claims, error) {
	var h HelloPayload
	if err := msgpack.Unmarshal(payload, &h); err != nil {
		return nil, fmt.Errorf("decode hello: %w", err)
	}
	// WS handshake has no request context; use Background with reasonable timeout.
	verifyCtx, cancelVerify := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelVerify()
	claims, err := g.issuer.Verify(verifyCtx, h.UserToken)
	if err != nil {
		return nil, fmt.Errorf("verify token: %w", err)
	}
	if claims.Platform == "" {
		claims.Platform = h.Platform
	}
	if claims.DeviceID == "" {
		claims.DeviceID = h.DeviceID
	}
	return claims, nil
}

// readLoop drains the connection until error or close.
func (g *Gateway) readLoop(sess *Session) {
	defer g.unregister(sess)
	if g.msgs != nil {
		defer g.msgs.untrackUser(sess)
	}
	defer sess.Conn.Close()

	for {
		frameType, payload, err := readFrame(sess.Conn)
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				g.log.Debug().Err(err).Str("user", sess.UserID).Msg("read frame")
			}
			return
		}
		sess.lastPing = time.Now()

		switch frameType {
		case FramePing:
			// Pong carries server time so clients can detect skew.
			_ = writeFrame(sess.Conn, FramePong, mustMsgpack(map[string]any{
				"ts": time.Now().UnixMilli(),
			}))
		case FrameSendMessage:
			if g.msgs != nil {
				if err := g.msgs.HandleSendMessage(sess, payload); err != nil {
					g.log.Warn().Err(err).Msg("send message")
				}
			} else {
				g.log.Warn().Msg("message handler not attached")
			}
		case FrameMarkRead:
			if g.msgs != nil {
				if err := g.msgs.HandleMarkRead(sess, payload); err != nil {
					g.log.Warn().Err(err).Msg("mark read")
				}
			}
		case FrameGetHistory:
			if g.msgs != nil {
				if err := g.msgs.HandleGetHistory(sess, payload); err != nil {
					g.log.Warn().Err(err).Msg("get history")
				}
			}
		default:
			g.log.Warn().Uint16("type", frameType).Msg("unknown frame")
		}
	}
}

// (handleSendMessage / handleMarkRead moved to MessageHandler in Week 5-6.)

// SetDirectory configures the distributed session directory for the gateway.
func (g *Gateway) SetDirectory(dir SessionRegistrar) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.dir = dir
}

func (g *Gateway) register(s *Session) {
	g.mu.Lock()
	g.conns[s.ID] = s
	dir := g.dir
	g.mu.Unlock()

	if dir != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := dir.Register(ctx, s.UserID, s.ID, s.cellID); err != nil {
			g.log.Warn().Err(err).Str("user", s.UserID).Msg("directory session register failed")
		}
	}
	g.log.Info().Str("session", s.ID).Str("user", s.UserID).Str("platform", s.Platform).Msg("session opened")
}

func (g *Gateway) unregister(s *Session) {
	g.mu.Lock()
	delete(g.conns, s.ID)
	dir := g.dir
	g.mu.Unlock()

	if dir != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := dir.Unregister(ctx, s.UserID, s.ID); err != nil {
			g.log.Warn().Err(err).Str("user", s.UserID).Msg("directory session unregister failed")
		}
	}
	g.log.Info().Str("session", s.ID).Str("user", s.UserID).Msg("session closed")
}

// ActiveSessionCount returns the number of open connections (for /metrics).
func (g *Gateway) ActiveSessionCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.conns)
}

// --- frame codec helpers ---

func readFrame(conn *websocket.Conn) (uint16, []byte, error) {
	_, data, err := conn.ReadMessage()
	if err != nil {
		return 0, nil, err
	}
	if len(data) < 6 {
		return 0, nil, errors.New("frame too short")
	}
	frameType := binary.BigEndian.Uint16(data[0:2])
	length := binary.BigEndian.Uint32(data[2:6])
	if len(data) < int(6+length) {
		return 0, nil, errors.New("frame length mismatch")
	}
	return frameType, data[6 : 6+length], nil
}

func writeFrame(conn *websocket.Conn, frameType uint16, payload []byte) error {
	buf := make([]byte, 6+len(payload))
	binary.BigEndian.PutUint16(buf[0:2], frameType)
	binary.BigEndian.PutUint32(buf[2:6], uint32(len(payload)))
	copy(buf[6:], payload)
	return conn.WriteMessage(websocket.BinaryMessage, buf)
}

func writeError(conn *websocket.Conn, code uint16, msg string) error {
	return writeFrame(conn, FrameError, mustMsgpack(map[string]any{
		"code": code,
		"msg":  msg,
	}))
}

func mustMsgpack(v any) []byte {
	b, err := msgpack.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
