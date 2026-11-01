// Package llm provides pluggable LLM backends for the pipeline's analytical
// stages. The same prompt contract runs over three backend types:
//   - ClaudeCLI    — reuses existing Claude Code subscriptions (wrappers
//     from ~/.zshrc), no separate tokens to buy;
//   - Anthropic    — direct Messages API (personal keys, for the future);
//   - OpenAICompat — any /chat/completions endpoint (OpenRouter, DeepSeek,
//     vLLM, ...) — the migration path to cheap personal subscriptions.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Options are call parameters; a backend without the matching knob ignores them.
type Options struct {
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

// Completer is the common backend contract.
type Completer interface {
	Name() string
	Complete(ctx context.Context, system, user string, opts Options) (string, Usage, error)
}

const jsonInstruct = "\n\nRespond with a single valid JSON object and nothing else."

// AskJSON is Complete with a hard JSON requirement; when the answer is not
// valid JSON (wrapped in a markdown fence or broken syntax), it retries once.
// The retry resends the original task together with the broken answer: the
// backends are stateless, so a bare "fix your previous reply" reaches a model
// that has never seen it.
func AskJSON(ctx context.Context, c Completer, system, user string, out any, maxTokens int) (Usage, error) {
	opts := Options{MaxTokens: maxTokens}

	text, usage, err := c.Complete(ctx, system, user+jsonInstruct, opts)
	if err != nil {
		return usage, err
	}

	if err := json.Unmarshal(stripFences(text), out); err == nil {
		return usage, nil
	}

	retry := user + jsonInstruct +
		"\n\nYour previous answer was not valid JSON:\n" + capText(text, 4000) +
		"\n\nReturn ONLY the corrected JSON object, nothing else."
	text, u2, err := c.Complete(ctx, system, retry, opts)
	usage = usage.Add(u2)

	if err != nil {
		return usage, err
	}

	if err := json.Unmarshal(stripFences(text), out); err != nil {
		return usage, fmt.Errorf("%s: invalid JSON after retry: %w", c.Name(), err)
	}

	return usage, nil
}

// capText hard-caps a string at n runes.
func capText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[:n]) + "…"
}

// stripFences removes ```json ... ``` around the answer if the model added it.
func stripFences(s string) []byte {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}

		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
	}

	return []byte(s)
}
