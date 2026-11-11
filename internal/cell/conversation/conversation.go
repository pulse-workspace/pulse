// Package conversation is the conversation domain service.
//
// Conversations are virtual aggregations: they don't have their own table
// other than the cached projection in the `conversations` table.
// On every MessageSent, we upsert the projection with last_msg_id, last_msg_at.
// On MessageRead, we update conversation_unread per user.
package conversation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pulse-im/pulse/internal/cell"
	"github.com/pulse-im/pulse/internal/cell/message"
	"github.com/pulse-im/pulse/pkg/shared/db"
	"github.com/pulse-im/pulse/pkg/shared/logger"
	"github.com/pulse-im/pulse/pkg/shared/nats"
)

// Conversation is the projection for a conversation in a user's list.
type Conversation struct {
	ConvID       string     `json:"convID"`
	ConvType     int        `json:"convType"` // 1=single, 2=group
	Participants []string   `json:"participants"`
	LastMsgID    string     `json:"lastMsgID"`
	LastMsgAt    *time.Time `json:"lastMsgAt,omitempty"`
	UnreadCount  int        `json:"unreadCount"`
	Pinned       bool       `json:"pinned"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// Service manages conversations.
type Service struct {
	cell *cell.Cell
	log  logger.Logger
	db   *db.DB
	nats *nats.Client
	msg  *message.Service
}

// NewService creates the conversation service.
func NewService(c *cell.Cell, msg *message.Service) *Service {
	return &Service{
		cell: c,
		log:  c.Log.With().Str("service", "conversation").Logger(),
		db:   c.DB,
		nats: c.NATS,
		msg:  msg,
	}
}

// Name implements cell.Service.
func (s *Service) Name() string { return "conversation" }

// Start ensures the conversation stream exists.
func (s *Service) Start(ctx context.Context) error {
	if err := s.nats.EnsureStream(ctx, "pulse_conversations", []string{"conv.>"}); err != nil {
		return fmt.Errorf("ensure conv stream: %w", err)
	}
	s.log.Info().Msg("conversation service started")
	return nil
}

// Stop shuts down.
func (s *Service) Stop(ctx context.Context) error { return nil }

// ErrNotFound is returned when a conversation does not exist.
var ErrNotFound = errors.New("conversation not found")

// GetOrCreate returns the conversation for a list of participants, creating
// it if it doesn't exist (upsert).
func (s *Service) GetOrCreate(ctx context.Context, participants []string, convType int) (*Conversation, error) {
	convID := message.MakeConvID(participants, "")
	if convID == "" {
		return nil, errors.New("invalid participants")
	}

	// Try to fetch existing.
	c, err := s.Get(ctx, convID)
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	// Insert.
	now := time.Now().UTC()
	if err := s.db.Exec(ctx,
		`INSERT INTO conversations (conv_id, conv_type, participants, last_msg_at, created_at, updated_at)
		 VALUES ($1, $2, $3, NULL, $4, $4)`,
		convID, convType, participants, now); err != nil {
		// Race: another writer may have inserted; retry read.
		return s.Get(ctx, convID)
	}
	return s.Get(ctx, convID)
}

// Get returns the conversation projection.
func (s *Service) Get(ctx context.Context, convID string) (*Conversation, error) {
	c := &Conversation{}
	var lastMsgID *string
	err := s.db.QueryRow(ctx,
		`SELECT conv_id, conv_type, participants, last_msg_id, last_msg_at, pinned, updated_at
		 FROM conversations WHERE conv_id = $1`, convID).
		Scan(&c.ConvID, &c.ConvType, &c.Participants, &lastMsgID, &c.LastMsgAt, &c.Pinned, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if lastMsgID != nil {
		c.LastMsgID = *lastMsgID
	}
	// Unread count (caller's). For now returns the caller's specific count.
	return c, nil
}

// List returns conversations for a user, newest first by last message.
func (s *Service) List(ctx context.Context, userID string, limit int) ([]*Conversation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx,
		`SELECT conv_id, conv_type, participants, COALESCE(last_msg_id, ''), last_msg_at, pinned, updated_at
		 FROM conversations
		 WHERE $1 = ANY(participants)
		 ORDER BY COALESCE(last_msg_at, updated_at) DESC
		 LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Conversation
	for rows.Next() {
		c := &Conversation{}
		if err := rows.Scan(&c.ConvID, &c.ConvType, &c.Participants, &c.LastMsgID, &c.LastMsgAt, &c.Pinned, &c.UpdatedAt); err != nil {
			return nil, err
		}
		// Fetch unread for this user.
		var unread int
		_ = s.db.QueryRow(ctx,
			`SELECT count FROM conversation_unread WHERE conv_id = $1 AND user_id = $2`,
			c.ConvID, userID).Scan(&unread)
		c.UnreadCount = unread
		out = append(out, c)
	}
	return out, rows.Err()
}

// OnMessageSent updates the conversation projection after a message is sent.
// Called from the gateway after a successful send.
func (s *Service) OnMessageSent(ctx context.Context, convID, msgID, senderID string, participants []string, sentAt time.Time) error {
	// Upsert conversation projection.
	if err := s.db.Exec(ctx,
		`INSERT INTO conversations (conv_id, conv_type, participants, last_msg_id, last_msg_at, created_at, updated_at)
		 VALUES ($1, 1, $2, $3, $4, $4, $4)
		 ON CONFLICT (conv_id) DO UPDATE
		 SET last_msg_id = EXCLUDED.last_msg_id,
		     last_msg_at = EXCLUDED.last_msg_at,
		     updated_at = EXCLUDED.updated_at`,
		convID, participants, msgID, sentAt); err != nil {
		return err
	}

	// Bump unread for every participant except sender.
	for _, p := range participants {
		if p == senderID {
			continue
		}
		if err := s.db.Exec(ctx,
			`INSERT INTO conversation_unread (conv_id, user_id, count, updated_at)
			 VALUES ($1, $2, 1, $3)
			 ON CONFLICT (conv_id, user_id) DO UPDATE
			 SET count = conversation_unread.count + 1,
			     updated_at = EXCLUDED.updated_at`,
			convID, p, sentAt); err != nil {
			s.log.Warn().Err(err).Str("user", p).Msg("bump unread")
		}
	}
	return nil
}

// MarkRead marks all messages up to msgID as read by userID and resets unread.
func (s *Service) MarkRead(ctx context.Context, convID, userID, msgID string) error {
	err := s.db.Exec(ctx,
		`INSERT INTO conversation_unread (conv_id, user_id, count, last_read_msg_id, updated_at)
		 VALUES ($1, $2, 0, $3, NOW())
		 ON CONFLICT (conv_id, user_id) DO UPDATE
		 SET count = 0,
		     last_read_msg_id = EXCLUDED.last_read_msg_id,
		     updated_at = EXCLUDED.updated_at`,
		convID, userID, msgID)
	return err
}

// Subscribe starts a NATS subscription that updates projections on incoming events.
// For Week 5-6 this is wired by the gateway explicitly; the consumer pattern
// lands in Week 7-8 with full async projections.
func (s *Service) Subscribe(ctx context.Context) error {
	return nil
}
