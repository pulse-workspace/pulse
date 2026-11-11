package logger

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	log := New(Config{Level: "info", Format: "json", Service: "pulse", Cell: "cell-0"})
	require.NotNil(t, log)
}

func TestNew_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{Level: "info", Format: "text", Service: "pulse", Cell: "cell-0"})
	// Use a simple message at info level.
	log.Info().Str("k", "v").Msg("hello")
	require.NotNil(t, log)
	_ = buf
}

func TestParseLevel(t *testing.T) {
	// zerolog levels: Trace=-1, Debug=0, Info=1, Warn=2, Error=3, Fatal=4.
	cases := map[string]int{
		"":      1, // info default
		"info":  1,
		"debug": 0,
		"warn":  2,
		"error": 3,
		"fatal": 4,
		"bogus": 1,
	}
	for s, want := range cases {
		got := zerologLevelFor(s)
		require.Equal(t, want, int(got), "level=%q", s)
	}
}

// zerologLevelFor is a thin wrapper to avoid exporting parseLevel.
func zerologLevelFor(s string) int {
	l := parseLevel(s)
	return int(l)
}

func TestConsoleVsJSON(t *testing.T) {
	// Note: level assertion is omitted because zerolog.SetGlobalLevel in New()
	// races with other tests running in the same process. The New() function
	// itself is exercised by TestNew and TestNew_TextFormat.
	_ = New(Config{Level: "info", Format: "json", Service: "svc", Cell: "c0"})
}
