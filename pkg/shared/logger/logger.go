// Package logger provides a unified structured logger built on rs/zerolog.
//
// We expose zerolog.Logger directly via a type alias to avoid wrapping
// complications with pointer vs value receivers.
package logger

import (
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Logger is the logger used throughout Pulse. Type alias for zerolog.Logger.
type Logger = zerolog.Logger

// Config configures the logger.
type Config struct {
	Level      string // debug | info | warn | error
	Format     string // json | text (default: json)
	TimeFormat string // default: RFC3339
	Service    string // service name tag
	Cell       string // cell id tag
}

// New creates a new logger.
func New(cfg Config) Logger {
	level := parseLevel(cfg.Level)
	zerolog.SetGlobalLevel(level)

	if cfg.TimeFormat == "" {
		cfg.TimeFormat = time.RFC3339
	}
	zerolog.TimeFieldFormat = cfg.TimeFormat

	format := strings.ToLower(cfg.Format)
	if format == "" {
		format = "json"
	}

	var base zerolog.Logger
	if format == "text" || isTerminal() {
		base = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: cfg.TimeFormat}).
			With().Timestamp().Logger()
	} else {
		base = zerolog.New(os.Stderr).With().Timestamp().Logger()
	}

	if cfg.Service != "" {
		base = base.With().Str("service", cfg.Service).Logger()
	}
	if cfg.Cell != "" {
		base = base.With().Str("cell", cfg.Cell).Logger()
	}

	return base
}

func parseLevel(s string) zerolog.Level {
	switch strings.ToLower(s) {
	case "trace":
		return zerolog.TraceLevel
	case "debug":
		return zerolog.DebugLevel
	case "info", "":
		return zerolog.InfoLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	default:
		return zerolog.InfoLevel
	}
}

func isTerminal() bool {
	stat, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}
