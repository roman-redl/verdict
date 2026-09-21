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

// Completer is the common backend contract.
type Completer interface {
	Name() string
	Complete(ctx context.Context, system, user string, opts Options) (string, error)
}

const jsonInstruct = "\n\nRespond with a single valid JSON object and nothing else."

// AskJSON is Complete with a hard JSON requirement; when the answer is not
// valid JSON (wrapped in a markdown fence or broken syntax), it retries once.
func AskJSON(ctx context.Context, c Completer, system, user string, out any, maxTokens int) error {
	opts := Options{MaxTokens: maxTokens}
	text, err := c.Complete(ctx, system, user+jsonInstruct, opts)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(stripFences(text), out); err == nil {
		return nil
	}
	text, err = c.Complete(ctx, system,
		"Your previous answer was not valid JSON. Return ONLY the corrected JSON object, nothing else.",
		opts)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(stripFences(text), out); err != nil {
		return fmt.Errorf("%s: invalid JSON after retry: %w", c.Name(), err)
	}
	return nil
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
