package typing

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIndicatorFields(t *testing.T) {
	now := time.Now().UTC()
	i := Indicator{
		ConvID:    "si_alice_bob",
		UserID:    "alice",
		ExpiresAt: now.Add(5 * time.Second),
	}
	require.Equal(t, "si_alice_bob", i.ConvID)
	require.Equal(t, "alice", i.UserID)
	require.True(t, i.ExpiresAt.After(now))
}

func TestServiceName(t *testing.T) {
	var s *Service
	require.Equal(t, "typing", s.Name())
}

// TestStartStop_IsTyping exercises the basic in-memory state machine.
func TestStartStop_IsTyping(t *testing.T) {
	updates := make(chan [2]string, 16)
	s := NewService(nil, func(convID, userID string, isTyping bool) {
		updates <- [2]string{convID, userID}
	})
	defer func() { _ = s.Stop(t.Context()) }()

	s.StartTyping("si_alice_bob", "alice")
	require.True(t, s.IsTyping("si_alice_bob", "alice"))
	require.False(t, s.IsTyping("si_alice_bob", "bob"))

	// Drain the callback.
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("expected onUpdate callback")
	}

	s.StopTyping("si_alice_bob", "alice")
	require.False(t, s.IsTyping("si_alice_bob", "alice"))
}

// TestTypingNow_ExcludesExpired makes sure the sweep logic drops stale entries
// immediately, not just when the janitor runs.
func TestTypingNow_ExcludesExpired(t *testing.T) {
	s := NewService(nil, nil)
	defer func() { _ = s.Stop(t.Context()) }()

	s.StartTyping("c1", "alice")
	// Manually backdate the expiry.
	s.mu.Lock()
	s.state["c1"]["alice"] = time.Now().Add(-1 * time.Minute)
	s.mu.Unlock()

	require.False(t, s.IsTyping("c1", "alice"))
	require.Empty(t, s.TypingNow("c1"))
}

// =====================================================================
// Additional coverage tests below.
// =====================================================================

// TestTypingNow_ReturnsMultipleTypists confirms the multi-user case:
// TypingNow returns every active typist in the conv, in unspecified
// order. The state machine is conv-scoped — entries in other convs
// must not leak into the result.
func TestTypingNow_ReturnsMultipleTypists(t *testing.T) {
	s := NewService(nil, nil)
	defer func() { _ = s.Stop(t.Context()) }()

	s.StartTyping("c1", "alice")
	s.StartTyping("c1", "bob")
	s.StartTyping("c2", "carol") // different conv — must not appear

	got := s.TypingNow("c1")
	require.Len(t, got, 2)
	require.ElementsMatch(t, []string{"alice", "bob"}, got)

	require.Equal(t, []string{"carol"}, s.TypingNow("c2"))
}

// TestStopTyping_RemovesEmptyConvMap confirms the GC behaviour: when
// the last typist in a conv stops, the conv entry is removed from the
// state map. This matters for memory bounds under high churn.
func TestStopTyping_RemovesEmptyConvMap(t *testing.T) {
	s := NewService(nil, nil)
	defer func() { _ = s.Stop(t.Context()) }()

	s.StartTyping("c1", "alice")
	require.Contains(t, s.state, "c1")

	s.StopTyping("c1", "alice")
	require.NotContains(t, s.state, "c1",
		"empty conv entry must be GC'd from state map")
}

// TestStopTyping_NotTypingIsNoop documents the current behaviour:
// StopTyping on a user who never started typing DOES fire the
// isTyping=false callback. This is intentional — the fan-out lets
// clients learn that the user stopped typing even if they missed
// the start. The test locks the contract so a future change is
// conscious.
func TestStopTyping_NotTypingIsNoop(t *testing.T) {
	var got [2]string
	var mu sync.Mutex
	s := NewService(nil, func(convID, userID string, _ bool) {
		mu.Lock()
		defer mu.Unlock()
		got = [2]string{convID, userID}
	})
	defer func() { _ = s.Stop(t.Context()) }()

	s.StopTyping("ghost-conv", "ghost-user")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, [2]string{"ghost-conv", "ghost-user"}, got,
		"StopTyping must fire the fan-out so subscribers learn the user stopped")
}

// TestJanitor_SweepsExpiredEntries confirms the background ticker
// path: Start spawns the janitor which, after the entry's TTL, removes
// stale rows from the state map and fires the onUpdate(isTyping=false)
// callback for each expired entry.
//
// We hand-crank the entry's expiry to be already-past, then wait one
// janitor tick (1s) for the sweep to fire.
func TestJanitor_SweepsExpiredEntries(t *testing.T) {
	type update struct {
		convID, userID string
		isTyping       bool
	}
	updates := make(chan update, 4)
	s := NewService(nil, func(c, u string, isTyping bool) {
		updates <- update{c, u, isTyping}
	})
	defer func() { _ = s.Stop(t.Context()) }()

	require.NoError(t, s.Start(t.Context()))

	s.StartTyping("c1", "alice")
	// Drain the initial isTyping=true callback so the channel only
	// carries the sweep event.
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("did not receive initial isTyping=true callback")
	}

	// Backdate so the next sweep removes the entry.
	s.mu.Lock()
	s.state["c1"]["alice"] = time.Now().Add(-1 * time.Second)
	s.mu.Unlock()

	// Wait up to 2s for the janitor (1s tick) to sweep.
	select {
	case u := <-updates:
		require.Equal(t, "c1", u.convID)
		require.Equal(t, "alice", u.userID)
		require.False(t, u.isTyping, "sweep must fire isTyping=false")
	case <-time.After(2 * time.Second):
		t.Fatal("janitor did not sweep expired entry within 2s")
	}
	require.False(t, s.IsTyping("c1", "alice"))
}

// TestStop_Idempotent confirms Stop can be called twice without
// panicking on the second close(stop) call. The first Stop wins; the
// second must be a clean no-op.
func TestStop_Idempotent(t *testing.T) {
	s := NewService(nil, nil)
	require.NoError(t, s.Stop(t.Context()))
	require.NoError(t, s.Stop(t.Context()))
}

// TestStartTyping_IdempotentResetsExpiry confirms that calling
// StartTyping twice in quick succession extends the TTL (Start is
// idempotent — the entry's expiry is refreshed to now+5s each call).
func TestStartTyping_IdempotentResetsExpiry(t *testing.T) {
	s := NewService(nil, nil)
	defer func() { _ = s.Stop(t.Context()) }()

	s.StartTyping("c1", "alice")
	first := s.state["c1"]["alice"]

	time.Sleep(20 * time.Millisecond)
	s.StartTyping("c1", "alice")
	second := s.state["c1"]["alice"]

	require.True(t, second.After(first),
		"second StartTyping must reset the expiry, got first=%v second=%v", first, second)
}
