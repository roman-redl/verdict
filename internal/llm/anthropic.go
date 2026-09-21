package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/roman-redl/verdict/internal/httpx"
)

const (
	defaultBase   = "https://api.anthropic.com"
	anthropicPath = "/v1/messages"
	anthropicVer  = "2023-06-01"
)

// Anthropic is the direct Messages API (personal key; the base URL can be
// pointed at any Anthropic-compatible proxy).
type Anthropic struct {
	base  string
	http  *httpx.Client
	key   string
	model string
}

func NewAnthropic(key, model string) *Anthropic {
	return &Anthropic{base: defaultBase, http: httpx.New("anthropic", 2), key: key, model: model}
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
}

func (a *Anthropic) Name() string { return "anthropic:" + a.model }

// Complete performs one model call; returns the concatenated text blocks.
func (a *Anthropic) Complete(ctx context.Context, system, user string, opts Options) (string, error) {
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	req := request{
		Model:     a.model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []message{{Role: "user", Content: user}},
	}
	var resp response
	headers := map[string]string{
		"x-api-key":         a.key,
		"anthropic-version": anthropicVer,
	}
	if err := a.http.DoJSON(ctx, http.MethodPost, a.base+anthropicPath, req, &resp, headers); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, b := range resp.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("anthropic: empty response (stop_reason=%s)", resp.StopReason)
	}
	return sb.String(), nil
}
