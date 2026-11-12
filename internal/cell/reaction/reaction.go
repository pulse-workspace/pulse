// Package reaction implements emoji reactions on messages.
package reaction

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pulse-workspace/pulse/internal/cell"
	"github.com/pulse-workspace/pulse/pkg/shared/db"
	"github.com/pulse-workspace/pulse/pkg/shared/events"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
	"github.com/pulse-workspace/pulse/pkg/shared/nats"
)

// Reaction is a single emoji-on-a-message row.
type Reaction struct {
	MsgID     string    `json:"msg_id"`
	UserID    string    `json:"user_id"`
	Type      string    `json:"type"`
	ConvID    string    `json:"conv_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ErrNotFound is returned when a reaction does not exist.
var ErrNotFound = errors.New("reaction not found")

// crossCellPublisher is the subset of routing.Service that
// reaction uses for cross-cell fan-out. Defined locally to avoid
// an import cycle (routing imports domains; domains must NOT
// import routing).
type crossCellPublisher interface {
	PublishCrossCell(ctx context.Context, peerCell, kind string, payload any) error
	KnownPeers() []string
}

// Service is the reaction service.
type Service struct {
	cell   *cell.Cell
	log    logger.Logger
	db     db.DBTX
	nats   *nats.Client
	router crossCellPublisher // optional; nil-safe
}

// NewService constructs the service.
func NewService(c *cell.Cell) *Service {
	return &Service{
		cell: c,
		log:  c.Log.With().Str("service", "reaction").Logger(),
		db:   c.DB,
		nats: c.NATS,
	}
}

// SetRouter configures the cross-cell routing publisher to broadcast reaction updates across cells.
func (s *Service) SetRouter(r crossCellPublisher) {
	s.router = r
}

// Name implements cell.Service.
func (s *Service) Name() string { return "reaction" }

// Start implements cell.Service.
func (s *Service) Start(ctx context.Context) error {
	return s.nats.EnsureStream(ctx, "events_reactions", []string{"reaction.>"})
}

// Stop implements cell.Service.
func (s *Service) Stop(ctx context.Context) error { return nil }

// Add idempotently inserts a reaction.
func (s *Service) Add(ctx context.Context, convID, msgID, userID, reactionType string) error {
	_, err := s.db.Query(ctx,
		`INSERT INTO reactions (msg_id, user_id, reaction_type)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (msg_id, user_id, reaction_type) DO NOTHING`,
		msgID, userID, reactionType)
	if err != nil {
		return err
	}
	evt := events.NewEvent("message:"+msgID, events.TypeReactionAdded, userID, nil)
	evt.Data = mustJSON(map[string]any{
		"conv_id": convID, "msg_id": msgID, "user_id": userID, "type": reactionType,
	})
	_ = s.nats.Publish(ctx, "reaction."+msgID, mustJSON(evt))
	s.fanoutCrossCell(msgID, userID, reactionType, true)
	return nil
}

// Remove deletes a reaction.
func (s *Service) Remove(ctx context.Context, convID, msgID, userID, reactionType string) error {
	err := s.db.Exec(ctx,
		`DELETE FROM reactions WHERE msg_id = $1 AND user_id = $2 AND reaction_type = $3`,
		msgID, userID, reactionType)
	if err != nil {
		return err
	}
	evt := events.NewEvent("message:"+msgID, events.TypeReactionRemoved, userID, nil)
	evt.Data = mustJSON(map[string]any{
		"conv_id": convID, "msg_id": msgID, "user_id": userID, "type": reactionType,
	})
	_ = s.nats.Publish(ctx, "reaction."+msgID, mustJSON(evt))
	s.fanoutCrossCell(msgID, userID, reactionType, false)
	return nil
}

// fanoutCrossCell publishes a reaction add/remove to every known peer.
// add=true emits the reaction; add=false emits a removal.
func (s *Service) fanoutCrossCell(msgID, userID, reactionType string, add bool) {
	if s.router == nil {
		return
	}
	peers := s.router.KnownPeers()
	if len(peers) == 0 {
		return
	}
	payload := map[string]any{
		"msg_id":   msgID,
		"user_id":  userID,
		"reaction": reactionType,
		"add":      add,
		"cell_id":  s.cell.Cfg.Cell.ID,
	}
	for _, peer := range peers {
		if err := s.router.PublishCrossCell(context.Background(), peer, "reaction", payload); err != nil {
			s.log.Warn().Err(err).Str("peer", peer).Str("msg_id", msgID).Msg("cross-cell reaction publish failed")
		}
	}
}

// ListForMessage returns all reactions on a message.
func (s *Service) ListForMessage(ctx context.Context, msgID string) ([]Reaction, error) {
	rows, err := s.db.Query(ctx,
		`SELECT msg_id, user_id, reaction_type, created_at
		 FROM reactions WHERE msg_id = $1 ORDER BY created_at`, msgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Reaction{}
	for rows.Next() {
		var r Reaction
		if err := rows.Scan(&r.MsgID, &r.UserID, &r.Type, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountByType returns a map of reaction_type → count for a message.
func (s *Service) CountByType(ctx context.Context, msgID string) (map[string]int, error) {
	rows, err := s.db.Query(ctx,
		`SELECT reaction_type, COUNT(*) FROM reactions WHERE msg_id = $1 GROUP BY reaction_type`, msgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var t string
		var c int
		if err := rows.Scan(&t, &c); err != nil {
			return nil, err
		}
		out[t] = c
	}
	return out, rows.Err()
}

// Compile-time guard.
var _ = pgx.ErrNoRows

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
