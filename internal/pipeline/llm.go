package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// CompleteOptions are call parameters; a backend without the matching knob
// ignores them.
type CompleteOptions struct {
	MaxTokens int
}

// Usage is the token accounting of one call, when the backend reports it.
// InputTokens counts everything the model ingested — fresh input plus cached
// reads — which is the honest "what did this call cost" number. Calls counts
// the Complete invocations behind this Usage (an AskJSON retry adds up to 2).
// Zero fields mean the backend did not report usage.
type Usage struct {
	Calls        int `json:"calls,omitempty"`
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// Add returns the element-wise sum (for accumulating a stage).
func (u Usage) Add(o Usage) Usage {
	return Usage{
		Calls:        u.Calls + o.Calls,
		InputTokens:  u.InputTokens + o.InputTokens,
		OutputTokens: u.OutputTokens + o.OutputTokens,
	}
}

// Completer is the LLM port: one stateless model call. The backends live in
// internal/llm.
type Completer interface {
	Name() string
	Complete(ctx context.Context, system, user string, opts CompleteOptions) (string, Usage, error)
}

// ErrLLMStop marks a Complete failure after which the backend cannot take
// more calls in this run: an exhausted subscription quota or a failed login.
// Retrying or falling back to other calls only burns more of the quota, so
// the stages stop on it (errors.Is) instead of degrading per study.
var ErrLLMStop = errors.New("the LLM backend cannot take more calls in this run")

// errLLMStopped is what the user sees when ErrLLMStop ended a stage.
func errLLMStopped() *UserError {
	return &UserError{Msg: "the LLM backend refused further calls (usage limit or login): " +
		"finished critiques are cached — rerun with --resume after the limit resets", Cause: ErrLLMStop}
}

const jsonInstruct = "\n\nRespond with a single valid JSON object and nothing else."

// retryJSONEchoChars caps how much of a broken answer is quoted back.
const retryJSONEchoChars = 4000

// AskJSON is Complete with a hard JSON requirement; when the answer is not
// valid JSON (wrapped in a markdown fence or broken syntax), it retries once.
// A failed call is not retried here: backend errors, ErrLLMStop included,
// come back wrapped with %w.
// The retry resends the original task together with the broken answer: the
// backends are stateless, so a bare "fix your previous reply" reaches a model
// that has never seen it.
func AskJSON(ctx context.Context, c Completer, system, user string, out any, maxTokens int) (Usage, error) {
	return AskJSONChecked(ctx, c, system, user, out, maxTokens, nil)
}

// AskJSONChecked is AskJSON with a check of the decoded answer: check (nil for
// none) runs on out after a successful decode, and an answer it rejects is
// retried once like a broken one, the retry telling the model what was wrong.
// out is cleared before the retry's decode of a rejected answer, so a field of
// it cannot survive into the second one. When the retry's answer is valid JSON
// but is rejected again, the error wraps the check's error and out holds that
// answer: the caller may fall back on it. The same holds when the retry fails
// altogether (a call error or broken JSON): out then holds the first answer,
// which decoded fine, and the error wraps the first check's error alongside
// the retry's own, so the caller may fall back on the first answer.
func AskJSONChecked(ctx context.Context, c Completer, system, user string, out any, maxTokens int,
	check func() error,
) (Usage, error) {
	opts := CompleteOptions{MaxTokens: maxTokens}

	text, usage, err := c.Complete(ctx, system, user+jsonInstruct, opts)
	if err != nil {
		return usage, fmt.Errorf("calling %s: %w", c.Name(), err)
	}

	decodeErr, checkErr := decodeAnswer(text, out, check)
	if decodeErr == nil && checkErr == nil {
		return usage, nil
	}

	problem := "not valid JSON"
	if decodeErr == nil {
		problem = fmt.Sprintf("rejected: %v", checkErr)
	}

	retry := user + jsonInstruct +
		"\n\nYour previous answer was " + problem + ":\n" + capText(text, retryJSONEchoChars) +
		"\n\nReturn ONLY the corrected JSON object, nothing else."
	// the rejected first answer decoded fine and filled out: keep it, so that a
	// retry that fails altogether still leaves the caller something to fall back on
	var firstAnswer any

	firstCheckErr := checkErr

	if checkErr != nil {
		firstAnswer = copyTarget(out)
	}

	keepFirst := func(retryErr error) error {
		if firstAnswer == nil {
			return retryErr
		}

		restoreTarget(out, firstAnswer)

		return fmt.Errorf("%w; the first answer is kept: %w", retryErr, firstCheckErr)
	}

	text, u2, err := c.Complete(ctx, system, retry, opts)
	usage = usage.Add(u2)

	if err != nil {
		return usage, keepFirst(fmt.Errorf("calling %s: %w", c.Name(), err))
	}

	// a first answer that failed to decode may still have filled some fields
	// (encoding/json goes on after a wrong-typed value), so the retry decodes
	// into a clean target either way; the snapshot above is for check rejections
	clearTarget(out)

	decodeErr, checkErr = decodeAnswer(text, out, check)
	if decodeErr != nil {
		return usage, keepFirst(fmt.Errorf("%s answered with invalid JSON after a retry: %w", c.Name(), decodeErr))
	}

	if checkErr != nil {
		return usage, fmt.Errorf("%s gave a rejected answer after a retry: %w", c.Name(), checkErr)
	}

	return usage, nil
}

// clearTarget zeroes what out points to, so that a decode into it starts clean.
func clearTarget(out any) {
	if v := reflect.ValueOf(out); v.Kind() == reflect.Pointer && !v.IsNil() {
		v.Elem().SetZero()
	}
}

// copyTarget is a shallow copy of what out points to (nil when out is not a
// non-nil pointer). Shallow is enough: a decode into the zeroed target
// allocates its own maps, slices and pointers and leaves the copy's alone.
func copyTarget(out any) any {
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return nil
	}

	c := reflect.New(v.Elem().Type())
	c.Elem().Set(v.Elem())

	return c.Interface()
}

// restoreTarget puts a copyTarget copy back into out.
func restoreTarget(out, saved any) {
	reflect.ValueOf(out).Elem().Set(reflect.ValueOf(saved).Elem())
}

// decodeAnswer decodes the answer into out and runs the check; the two
// failures come back apart (a broken answer is not checked).
func decodeAnswer(text string, out any, check func() error) (decodeErr, checkErr error) {
	if err := json.Unmarshal(extractJSON(text), out); err != nil {
		return fmt.Errorf("decoding the answer: %w", err), nil
	}

	if check != nil {
		return nil, check()
	}

	return nil, nil
}

// capText hard-caps a string at n runes.
func capText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[:n]) + "…"
}

// extractJSON pulls the JSON object out of a model answer: it strips a
// markdown fence and, when prose still surrounds the object ("Here is the
// result: {...}"), keeps the span from the first "{" to the last "}". Text
// without braces is returned as is — the caller's Unmarshal reports it.
func extractJSON(s string) []byte {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}

		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}

		s = strings.TrimSpace(s)
	}

	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		return []byte(s)
	}

	first, last := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}')
	if first >= 0 && last > first {
		s = s[first : last+1]
	}

	return []byte(s)
}
