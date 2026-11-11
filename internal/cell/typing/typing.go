// Package typing implements transient typing indicators.
//
// Typing indicators are NOT persisted — they live for ~5 seconds in Redis
// (or in-memory in single-node dev) and are pushed via NATS pub/sub to
// the conversation participants on this cell.
//
// For Week 7-8 we ship an in-memory implementation. Swap for Redis in
// production.
package typing

import (
	"context"
	"sync"
	"time"

	"github.com/pulse-im/pulse/internal/cell"
	"github.com/pulse-im/pulse/pkg/shared/logger"
)

// Indicator is one (convID, userID) typing state.
type Indicator struct {
	ConvID    string
	UserID    string
	ExpiresAt time.Time
}

// Service tracks typing indicators and fans them out via a callback.
type Service struct {
	cell     *cell.Cell
	log      logger.Logger
	mu       sync.Mutex
	state    map[string]map[string]time.Time // convID → userID → expiresAt
	onUpdate func(convID, userID string, isTyping bool)
	stop     chan struct{}
	stopped  bool
}

// NewService constructs the service.
func NewService(c *cell.Cell, onUpdate func(convID, userID string, isTyping bool)) *Service {
	var lg logger.Logger
	if c != nil {
		lg = c.Log.With().Str("service", "typing").Logger()
	} else {
		lg = logger.New(logger.Config{Level: "error"})
	}
	return &Service{
		cell:     c,
		log:      lg,
		state:    make(map[string]map[string]time.Time),
		onUpdate: onUpdate,
		stop:     make(chan struct{}),
	}
}

// Name implements cell.Service.
func (s *Service) Name() string { return "typing" }

// Start implements cell.Service. Spawns a janitor goroutine.
func (s *Service) Start(ctx context.Context) error {
	go s.janitor(ctx)
	return nil
}

// Stop implements cell.Service. Idempotent — safe to call multiple times.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	close(s.stop)
	s.mu.Unlock()
	return nil
}

// StartTyping marks userID as typing in convID. Idempotent.
func (s *Service) StartTyping(convID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state[convID] == nil {
		s.state[convID] = map[string]time.Time{}
	}
	s.state[convID][userID] = time.Now().Add(5 * time.Second)
	if s.onUpdate != nil {
		s.onUpdate(convID, userID, true)
	}
}

// StopTyping clears userID's typing state in convID.
func (s *Service) StopTyping(convID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.state[convID]; ok {
		delete(m, userID)
		if len(m) == 0 {
			delete(s.state, convID)
		}
	}
	if s.onUpdate != nil {
		s.onUpdate(convID, userID, false)
	}
}

// IsTyping returns true if userID is currently typing in convID.
func (s *Service) IsTyping(convID, userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.state[convID]
	if !ok {
		return false
	}
	t, ok := m[userID]
	if !ok {
		return false
	}
	return time.Now().Before(t)
}

// TypingNow returns all userIDs currently typing in convID.
func (s *Service) TypingNow(convID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.state[convID]
	if !ok {
		return nil
	}
	out := []string{}
	now := time.Now()
	for u, t := range m {
		if now.Before(t) {
			out = append(out, u)
		}
	}
	return out
}

func (s *Service) janitor(ctx context.Context) {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-t.C:
			s.sweep()
		}
	}
}

func (s *Service) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for convID, m := range s.state {
		for u, t := range m {
			if !now.Before(t) {
				delete(m, u)
				if s.onUpdate != nil {
					s.onUpdate(convID, u, false)
				}
			}
		}
		if len(m) == 0 {
			delete(s.state, convID)
		}
	}
}
