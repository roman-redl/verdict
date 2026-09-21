package llm

import (
	"context"
	"fmt"
	"net/http"

	"github.com/roman-redl/verdict/internal/httpx"
)

// OpenAICompat is any /chat/completions endpoint: OpenRouter, DeepSeek,
// vLLM, Together, etc. When corporate tariff access disappears, migrating to
// cheap personal subscriptions is a change of three env variables, no code.
type OpenAICompat struct {
	base  string
	key   string
	model string
	http  *httpx.Client
}

func NewOpenAICompat(base, key, model string) *OpenAICompat {
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return &OpenAICompat{base: base, key: key, model: model, http: httpx.New("openai-compat", 2)}
}

func (o *OpenAICompat) Name() string { return "openai-compat:" + o.model }

func (o *OpenAICompat) Complete(ctx context.Context, system, user string, opts Options) (string, error) {
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	req := map[string]any{
		"model":       o.model,
		"max_tokens":  maxTokens,
		"temperature": 0.2,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	headers := map[string]string{"Authorization": "Bearer " + o.key}
	if err := o.http.DoJSON(ctx, http.MethodPost, o.base+"/chat/completions", req, &resp, headers); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("%s: empty response", o.Name())
	}
	return resp.Choices[0].Message.Content, nil
}
