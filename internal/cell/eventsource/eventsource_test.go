package eventsource

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulse-im/pulse/pkg/shared/events"
)

func TestSubjectForStream(t *testing.T) {
	tests := []struct {
		name    string
		stream  string
		want    string
		wantErr bool
	}{
		{"userColon", "user:abc", "user.abc", false},
		{"groupColon", "group:g_01H", "group.g_01H", false},
		{"messageColon", "message:msg_01H", "message.msg_01H", false},
		{"convColon", "conv:conv_01H", "conv.conv_01H", false},
		{"presenceColon", "presence:u1", "presence.u1", false},
		{"emptyErrors", "", "", true},
		{"noColonErrors", "noseparator", "", true},
		{"colonAtStart", ":abc", ".abc", false},
		{"colonAtEnd", "user:", "user.", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := subjectForStream(tc.stream)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestSubjectForStream_ErrorMessages(t *testing.T) {
	_, err := subjectForStream("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")

	_, err = subjectForStream("badstream")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid stream format")
}

// stubProjector is a real Projector implementation for white-box tests that
// need to assert the interface contract end-to-end without bringing in the
// full compliance/rbac stack (which depends on the live DB).
type stubProjector struct {
	handled []events.Event
}

func (s *stubProjector) Name() string                              { return "stub" }
func (s *stubProjector) Stream() string                            { return "events_stub" }
func (s *stubProjector) Subject() string                           { return "stub.>" }
func (s *stubProjector) Handle(_ context.Context, e events.Event) error {
	s.handled = append(s.handled, e)
	return nil
}

// compile-time check
var _ Projector = (*stubProjector)(nil)

func TestStubProjector_ImplementsInterface(t *testing.T) {
	sp := &stubProjector{}
	var p Projector = sp
	require.Equal(t, "stub", p.Name())
	require.Equal(t, "events_stub", p.Stream())
	require.Equal(t, "stub.>", p.Subject())
	require.NoError(t, p.Handle(context.Background(), events.Event{Type: "x"}))
	require.Len(t, sp.handled, 1)
	require.Equal(t, "x", sp.handled[0].Type)
}

func TestStubProjector_Handle_ReturnsUnderlyingError(t *testing.T) {
	// Make sure Handle errors propagate. Wrap in a sentinel to verify errors.Is.
	want := errors.New("boom")
	sp := &errProjector{err: want}
	var p Projector = sp
	err := p.Handle(context.Background(), events.Event{Type: "x"})
	require.Error(t, err)
	require.True(t, errors.Is(err, want))
}

type errProjector struct{ err error }

func (e *errProjector) Name() string                              { return "err" }
func (e *errProjector) Stream() string                            { return "events_err" }
func (e *errProjector) Subject() string                           { return "err.>" }
func (e *errProjector) Handle(_ context.Context, _ events.Event) error { return e.err }
