package app

import (
	"io"
	"log/slog"

	"github.com/roman-redl/verdict/internal/config"
)

// NewLogger builds the process logger from LOG_LEVEL and LOG_FORMAT: text for
// a terminal, JSON for a container.
func NewLogger(w io.Writer, cfg *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}

	if cfg.LogFormat == config.LogJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}

	return slog.New(slog.NewTextHandler(w, opts))
}
