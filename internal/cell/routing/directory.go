package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
	"github.com/pulse-workspace/pulse/pkg/shared/nats"
)

const (
	// DirectoryBucketName is the NATS KV bucket holding active user session locations.
	DirectoryBucketName = "pulse_directory"
	// DirectoryTTL is the TTL for session location keys (5 minutes).
	// Refreshed by gateway heartbeats and active socket connection.
	DirectoryTTL = 5 * time.Minute
)

// sanitizeKeyRegex matches characters safe for NATS KV keys ([a-zA-Z0-9_\-\.]).
var sanitizeKeyRegex = regexp.MustCompile(`[^a-zA-Z0-9_\-\.]`)

func keyForUser(userID string) string {
	clean := sanitizeKeyRegex.ReplaceAllString(userID, "_")
	return "usr." + clean
}

// UserLocationRecord stores active session mappings for a user.
type UserLocationRecord struct {
	UserID    string            `json:"user_id"`
	Sessions  map[string]string `json:"sessions"` // session_id -> cell_id
	UpdatedAt time.Time         `json:"updated_at"`
}

// CellIDs returns a deduplicated, sorted list of cell IDs where this user has active sessions.
func (r *UserLocationRecord) CellIDs() []string {
	if r == nil || len(r.Sessions) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, cellID := range r.Sessions {
		if cellID != "" && !seen[cellID] {
			seen[cellID] = true
			out = append(out, cellID)
		}
	}
	sort.Strings(out)
	return out
}

// Directory provides distributed registration and lookup for active user sessions across cells.
type Directory interface {
	// Register associates sessionID on cellID with userID.
	Register(ctx context.Context, userID, sessionID, cellID string) error
	// Unregister removes sessionID for userID.
	Unregister(ctx context.Context, userID, sessionID string) error
	// Lookup returns the cell IDs where userID has active sessions.
	Lookup(ctx context.Context, userID string) ([]string, error)
	// LookupParticipants returns a deduplicated list of cell IDs hosting any of the given users.
	LookupParticipants(ctx context.Context, userIDs []string) ([]string, error)
}

// NewDirectory creates a NATS-backed Directory when NATS is available,
// falling back gracefully to an in-memory Directory for standalone/test environments.
func NewDirectory(ctx context.Context, natsClient *nats.Client, log logger.Logger) Directory {
	if natsClient != nil {
		kv, err := natsClient.EnsureKeyValue(ctx, DirectoryBucketName, DirectoryTTL)
		if err == nil {
			log.Info().Str("bucket", DirectoryBucketName).Msg("nats session directory initialized")
			return NewNatsDirectory(kv, log)
		}
		log.Warn().Err(err).Msg("failed to initialize nats kv directory, falling back to in-memory directory")
	}
	return NewMemoryDirectory()
}

// --- NATS KeyValue Directory Implementation ---------------------------------

type natsDirectory struct {
	kv  jetstream.KeyValue
	log logger.Logger
}

// NewNatsDirectory creates a Directory backed by NATS JetStream KeyValue store.
func NewNatsDirectory(kv jetstream.KeyValue, log logger.Logger) Directory {
	return &natsDirectory{
		kv:  kv,
		log: log.With().Str("component", "nats-directory").Logger(),
	}
}

func (d *natsDirectory) Register(ctx context.Context, userID, sessionID, cellID string) error {
	if userID == "" || sessionID == "" || cellID == "" {
		return nil
	}
	key := keyForUser(userID)

	// Fetch existing or initialize
	record := &UserLocationRecord{
		UserID:   userID,
		Sessions: make(map[string]string),
	}

	entry, err := d.kv.Get(ctx, key)
	if err == nil && entry != nil {
		_ = json.Unmarshal(entry.Value(), record)
		if record.Sessions == nil {
			record.Sessions = make(map[string]string)
		}
	}

	record.Sessions[sessionID] = cellID
	record.UpdatedAt = time.Now().UTC()

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal user location: %w", err)
	}

	_, err = d.kv.Put(ctx, key, data)
	return err
}

func (d *natsDirectory) Unregister(ctx context.Context, userID, sessionID string) error {
	if userID == "" || sessionID == "" {
		return nil
	}
	key := keyForUser(userID)

	entry, err := d.kv.Get(ctx, key)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil
		}
		return err
	}

	var record UserLocationRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil
	}

	delete(record.Sessions, sessionID)
	if len(record.Sessions) == 0 {
		return d.kv.Delete(ctx, key)
	}

	record.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	_, err = d.kv.Put(ctx, key, data)
	return err
}

func (d *natsDirectory) Lookup(ctx context.Context, userID string) ([]string, error) {
	if userID == "" {
		return nil, nil
	}
	key := keyForUser(userID)

	entry, err := d.kv.Get(ctx, key)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, nil
		}
		return nil, err
	}

	var record UserLocationRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, fmt.Errorf("unmarshal user location: %w", err)
	}
	return record.CellIDs(), nil
}

func (d *natsDirectory) LookupParticipants(ctx context.Context, userIDs []string) ([]string, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}

	cellSet := make(map[string]bool)
	for _, uid := range userIDs {
		cells, err := d.Lookup(ctx, uid)
		if err != nil {
			d.log.Warn().Err(err).Str("user_id", uid).Msg("participant directory lookup error")
			continue
		}
		for _, c := range cells {
			cellSet[c] = true
		}
	}

	var out []string
	for c := range cellSet {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}

// --- In-Memory Directory Implementation (Testing & Fallback) ----------------

type memoryDirectory struct {
	mu   sync.RWMutex
	data map[string]map[string]string // userID -> sessionID -> cellID
}

// NewMemoryDirectory returns an in-memory thread-safe Directory.
func NewMemoryDirectory() Directory {
	return &memoryDirectory{
		data: make(map[string]map[string]string),
	}
}

func (m *memoryDirectory) Register(ctx context.Context, userID, sessionID, cellID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[userID] == nil {
		m.data[userID] = make(map[string]string)
	}
	m.data[userID][sessionID] = cellID
	return nil
}

func (m *memoryDirectory) Unregister(ctx context.Context, userID, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.data[userID]; ok {
		delete(sess, sessionID)
		if len(sess) == 0 {
			delete(m.data, userID)
		}
	}
	return nil
}

func (m *memoryDirectory) Lookup(ctx context.Context, userID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.data[userID]
	if !ok || len(sess) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, c := range sess {
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *memoryDirectory) LookupParticipants(ctx context.Context, userIDs []string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cellSet := make(map[string]bool)
	for _, uid := range userIDs {
		if sess, ok := m.data[uid]; ok {
			for _, c := range sess {
				if c != "" {
					cellSet[c] = true
				}
			}
		}
	}
	var out []string
	for c := range cellSet {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}

