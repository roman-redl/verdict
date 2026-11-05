package main

import (
	"os"

	"github.com/charmbracelet/glamour"
)

// answerWrap is the column the terminal renderer wraps markdown at.
const answerWrap = 100

// renderAnswer pretty-prints markdown answers in a terminal; falls back to
// the raw text when stdout is piped or the renderer is unavailable.
func renderAnswer(s string) string {
	fi, err := os.Stdout.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return s
	}

	r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(answerWrap))
	if err != nil {
		return s
	}

	out, err := r.Render(s)
	if err != nil {
		return s
	}

	return out
}
