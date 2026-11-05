package llm

import (
	"context"
	"errors"
	"net/http"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.Completer = (*OpenAICompat)(nil)

// openAIDefaultBase is used when OPENAI_BASE_URL is empty.
const openAIDefaultBase = "https://api.openai.com/v1"

// completionTemperature keeps the machine-to-machine JSON calls steady.
const completionTemperature = 0.2

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
		base = openAIDefaultBase
	}

	return &OpenAICompat{base: base, key: key, model: model, http: httpx.New("openai-compat", 2)}
}

func (o *OpenAICompat) Name() string { return "openai-compat:" + o.model }

func (o *OpenAICompat) Complete(
	ctx context.Context, system, user string, opts pipeline.CompleteOptions,
) (string, pipeline.Usage, error) {
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	req := map[string]any{
		"model":       o.model,
		"max_tokens":  maxTokens,
		"temperature": completionTemperature,
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
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	endpoint, err := httpx.URL(o.base, nil, "chat", "completions")
	if err != nil {
		return "", pipeline.Usage{}, err
	}

	headers := map[string]string{"Authorization": "Bearer " + o.key}
	if err := o.http.DoJSON(ctx, http.MethodPost, endpoint, req, &resp, headers); err != nil {
		return "", pipeline.Usage{}, err
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return "", pipeline.Usage{}, errors.New("the endpoint returned an empty completion")
	}

	usage := pipeline.Usage{Calls: 1, InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens}

	return resp.Choices[0].Message.Content, usage, nil
}
