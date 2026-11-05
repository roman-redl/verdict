package app

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/roman-redl/verdict/internal/config"
)

func TestNewLoggerFormatAndLevel(t *testing.T) {
	var text bytes.Buffer

	log := NewLogger(&text, &config.Config{LogLevel: slog.LevelWarn, LogFormat: config.LogText})
	log.Info("hidden")
	log.Warn("shown", "k", "v")

	assert.NotContains(t, text.String(), "hidden", "records below LOG_LEVEL are dropped")
	assert.Contains(t, text.String(), "level=WARN")
	assert.Contains(t, text.String(), "k=v")

	var js bytes.Buffer

	NewLogger(&js, &config.Config{LogFormat: config.LogJSON}).Info("hello", "k", "v")

	assert.Contains(t, js.String(), `"msg":"hello"`)
	assert.Contains(t, js.String(), `"k":"v"`)
}
