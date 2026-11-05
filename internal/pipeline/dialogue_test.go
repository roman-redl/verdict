package pipeline

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDialogueCarriesHistory(t *testing.T) {
	c := &echoCompleter{replies: []string{"first answer", "second answer"}}
	d := NewDialogue(c, "SYSTEM")

	a, u, err := d.Ask(t.Context(), "first?")
	require.NoError(t, err)
	assert.Equal(t, "first answer", a)
	assert.Positive(t, u.InputTokens)

	_, _, err = d.Ask(t.Context(), "second?")
	require.NoError(t, err)

	require.Len(t, c.prompts, 2)
	assert.Contains(t, c.prompts[1], "Question: first?", "the history is resent")
	assert.Contains(t, c.prompts[1], "Answer: first answer")
	assert.True(t, strings.HasSuffix(c.prompts[1], "Question: second?"))
}

// A failed call leaves the history alone: the question can be repeated.
func TestDialogueSurvivesAFailedCall(t *testing.T) {
	c := &echoCompleter{replies: []string{"", "recovered"}, failAt: 1}
	d := NewDialogue(c, "S")

	_, _, err := d.Ask(t.Context(), "q1")
	require.ErrorContains(t, err, "backend down")

	a, _, err := d.Ask(t.Context(), "q1 again")
	require.NoError(t, err)
	assert.Equal(t, "recovered", a)
	assert.NotContains(t, c.prompts[1], "Question: q1\n", "the failed question left no trace")
}

// The history is capped on a character boundary.
func TestDialogueCapsHistory(t *testing.T) {
	long := strings.Repeat("é", maxTranscriptBytes) // 2 bytes per rune
	c := &echoCompleter{replies: []string{long, "ok"}}
	d := NewDialogue(c, "S")

	_, _, err := d.Ask(t.Context(), "q")
	require.NoError(t, err)

	_, _, err = d.Ask(t.Context(), "q2")
	require.NoError(t, err)

	assert.LessOrEqual(t, len(d.transcript), maxTranscriptBytes+len("\n\nQuestion: q2\nAnswer: ok"))
	assert.True(t, utf8.ValidString(c.prompts[1]), "the cut must not split a character")
}

func TestAskOnce(t *testing.T) {
	c := &echoCompleter{replies: []string{"42"}}

	a, _, err := AskOnce(t.Context(), c, "S", "the question")
	require.NoError(t, err)
	assert.Equal(t, "42", a)
	assert.Equal(t, []string{"the question"}, c.prompts, "a single question carries no transcript framing")

	_, _, err = AskOnce(t.Context(), &echoCompleter{failAt: 1}, "S", "q")
	require.Error(t, err)
}

func TestTail(t *testing.T) {
	assert.Equal(t, "abc", tail("abc", 10))
	assert.Equal(t, "cde", tail("abcde", 3))
	assert.Equal(t, "é", tail("aé", 2), "a cut inside a character drops the partial bytes")
}
