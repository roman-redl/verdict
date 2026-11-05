// Package llm holds the LLM backends of the pipeline; they implement
// pipeline.Completer. The same prompt contract runs over three backend types:
//   - ClaudeCLI    — reuses existing Claude Code subscriptions through a
//     wrapper (claude-max by default), no separate tokens to buy;
//   - Anthropic    — direct Messages API (a personal key, only through an
//     HTTPS proxy, only when LLM_BACKEND=anthropic asks for it);
//   - OpenAICompat — any /chat/completions endpoint (OpenRouter, DeepSeek,
//     vLLM, ...) — the migration path to cheap personal subscriptions.
package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.Completer = (*Anthropic)(nil)

const (
	// anthropicBase is the pinned API address: the personal key must never
	// follow an environment variable to another gateway. Tests point the
	// client elsewhere through newAnthropic.
	anthropicBase = "https://api.anthropic.com"
	anthropicPath = "/v1/messages"
	anthropicVer  = "2023-06-01"

	defaultMaxTokens = 4096
	// anthropicTimeout covers the longest answer (synthesis, up to 8000
	// tokens), which takes minutes; the shared default of 90 seconds would
	// cut it off.
	anthropicTimeout = 8 * time.Minute
)

// Anthropic is the direct Messages API with a personal key. Every request
// goes through an HTTPS proxy: a direct call from the owner's own address is
// forbidden (risk to the account), so there is no constructor without one.
type Anthropic struct {
	base  string
	http  *httpx.Client
	key   string
	model string
}

// NewAnthropic returns a client for the pinned API address that reaches it
// through the proxy URL (https://user:pass@host:port). The proxy URL is never
// echoed in errors: it carries the credentials.
func NewAnthropic(key, model, proxy string) (*Anthropic, error) {
	rt, err := ProxyTransport(proxy)
	if err != nil {
		return nil, err
	}

	return newAnthropic(anthropicBase, key, model, rt), nil
}

func newAnthropic(base, key, model string, rt http.RoundTripper) *Anthropic {
	hc := httpx.New("anthropic", 2, httpx.WithTimeout(anthropicTimeout), httpx.WithTransport(rt))

	return &Anthropic{base: base, http: hc, key: key, model: model}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []message `json:"messages"`
}

type response struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (a *Anthropic) Name() string { return "anthropic:" + a.model }

// Complete performs one model call; returns the concatenated text blocks.
func (a *Anthropic) Complete(
	ctx context.Context, system, user string, opts pipeline.CompleteOptions,
) (string, pipeline.Usage, error) {
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	req := request{
		Model:     a.model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []message{{Role: "user", Content: user}},
	}

	var resp response

	endpoint, err := httpx.URL(a.base, nil, anthropicPath)
	if err != nil {
		return "", pipeline.Usage{}, err
	}

	headers := map[string]string{
		"x-api-key":         a.key,
		"anthropic-version": anthropicVer,
	}
	if err := a.http.DoJSON(ctx, http.MethodPost, endpoint, req, &resp, headers); err != nil {
		return "", pipeline.Usage{}, err
	}

	var sb strings.Builder

	for _, b := range resp.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}

	if sb.Len() == 0 {
		return "", pipeline.Usage{}, fmt.Errorf("the Anthropic API returned no text (stop_reason=%s)", resp.StopReason)
	}

	usage := pipeline.Usage{Calls: 1, InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens}

	return sb.String(), usage, nil
}
