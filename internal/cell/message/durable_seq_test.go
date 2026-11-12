package message

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/pulse-workspace/pulse/internal/cell"
)

// TestNextSeq_DBSuccess verifies that nextSeq returns the sequence from DB
// and updates the local in-memory cache.
func TestNextSeq_DBSuccess(t *testing.T) {
	ctx := context.Background()
	convID := "c_test_seq_db"
	var expectedSeq int64 = 42

	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, args []any) pgx.Row {
			require.Contains(t, sql, "max_seq")
			require.Contains(t, sql, "conversations")
			require.Equal(t, convID, args[0])
			return &cannedRow{values: []any{expectedSeq}}
		},
	}

	s := svcWithFake(fake)
	seq := s.nextSeq(ctx, convID)
	require.Equal(t, expectedSeq, seq)

	// In-memory cache should reflect the DB value
	s.mu.Lock()
	cachedSeq := s.convSeq[convID]
	s.mu.Unlock()
	require.Equal(t, expectedSeq, cachedSeq)
}

// TestNextSeq_DBFallbackToMemory verifies that when DB is unavailable or returns an error,
// nextSeq falls back to the in-memory counter without panicking or failing.
func TestNextSeq_DBFallbackToMemory(t *testing.T) {
	ctx := context.Background()
	convID := "c_test_seq_fallback"

	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, _ string, _ []any) pgx.Row {
			return &cannedRowErr{err: errors.New("db connection failure")}
		},
	}

	s := svcWithFake(fake)
	seq1 := s.nextSeq(ctx, convID)
	require.Equal(t, int64(1), seq1)

	seq2 := s.nextSeq(ctx, convID)
	require.Equal(t, int64(2), seq2)
}

// TestNextSeq_NilDB verifies that when s.db is nil, nextSeq operates purely in-memory.
func TestNextSeq_NilDB(t *testing.T) {
	ctx := context.Background()
	convID := "c_test_nil_db"

	s := &Service{
		cell:    &cell.Cell{Log: zerolog.Nop()},
		log:     zerolog.Nop(),
		db:      nil,
		convSeq: make(map[string]int64),
	}

	for i := int64(1); i <= 10; i++ {
		seq := s.nextSeq(ctx, convID)
		require.Equal(t, i, seq)
	}
}

// TestNextSeq_ConcurrencyRaceFree verifies that concurrent nextSeq calls are thread-safe.
func TestNextSeq_ConcurrencyRaceFree(t *testing.T) {
	ctx := context.Background()
	convID := "c_test_seq_concurrent"

	s := &Service{
		cell:    &cell.Cell{Log: zerolog.Nop()},
		log:     zerolog.Nop(),
		db:      nil,
		convSeq: make(map[string]int64),
	}

	var wg sync.WaitGroup
	count := 100
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			_ = s.nextSeq(ctx, convID)
		}()
	}

	wg.Wait()

	s.mu.Lock()
	finalSeq := s.convSeq[convID]
	s.mu.Unlock()
	require.Equal(t, int64(count), finalSeq)
}

type cannedRowErr struct {
	err error
}

func (r *cannedRowErr) Scan(_ ...any) error {
	return r.err
}

