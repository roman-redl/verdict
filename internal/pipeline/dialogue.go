package pipeline

import (
	"context"
	"fmt"
	"strings"
)

// maxTranscriptBytes caps the dialogue history carried into the next
// question so it does not grow forever.
const maxTranscriptBytes = 24000

// AskOnce puts one question to the analyst holding the runs' context.
func AskOnce(ctx context.Context, c Completer, system, question string) (string, Usage, error) {
	answer, usage, err := c.Complete(ctx, system, question, CompleteOptions{})
	if err != nil {
		return "", Usage{}, fmt.Errorf("asking the analyst: %w", err)
	}

	return answer, usage, nil
}

// Dialogue is a conversation with the analyst: the backends are stateless, so
// the history travels with every question, newest part kept.
type Dialogue struct {
	c          Completer
	system     string
	transcript string
}

func NewDialogue(c Completer, system string) *Dialogue {
	return &Dialogue{c: c, system: system}
}

// Ask sends the question with the history. A failed call leaves the history
// untouched, so the question can simply be repeated.
func (d *Dialogue) Ask(ctx context.Context, question string) (string, Usage, error) {
	user := d.transcript + "\n\nQuestion: " + question

	answer, usage, err := AskOnce(ctx, d.c, d.system, user)
	if err != nil {
		return "", Usage{}, err
	}

	d.transcript = tail(user+"\nAnswer: "+answer, maxTranscriptBytes)

	return answer, usage, nil
}

// tail keeps the last n bytes of s without cutting a multi-byte character.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return strings.ToValidUTF8(s[len(s)-n:], "")
}
