package main

import (
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Under `go test` stdout is not a terminal, so answers pass through raw.
func TestRenderAnswerPassesThroughWhenPiped(t *testing.T) {
	assert.Equal(t, "# Title\n\n- point", renderAnswer("# Title\n\n- point"))
}

// The terminal renderer (glamour on goldmark) must keep working across
// dependency bumps: this is what `verdict ask` shows in a real terminal.
func TestTerminalRendererRendersMarkdown(t *testing.T) {
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle("ascii"), glamour.WithWordWrap(answerWrap))
	require.NoError(t, err)

	out, err := r.Render("# Verdict\n\n- **moderate** evidence, see [the trial](https://doi.org/10.1/a)\n")
	require.NoError(t, err)

	assert.Contains(t, out, "Verdict")
	assert.Contains(t, out, "moderate")
	assert.Contains(t, out, "the trial")
}

func TestShortenAndOrDash(t *testing.T) {
	assert.Equal(t, "abc", shorten("abc", 5))
	assert.Equal(t, "abc…", shorten("abcdef", 4))
	assert.Equal(t, "é…", shorten("éééé", 2), "the cut counts characters, not bytes")
	assert.Equal(t, "—", orDash(""))
	assert.Equal(t, "x", orDash("x"))
}
