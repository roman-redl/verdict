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
// vLLM, Together, etc. Migrating to another provider is a change of a few env
// variables, no code. Providers with a personal paid key (OpenAI, Groq) are
// reached only through the proxy, see ProxyRequired.
type OpenAICompat struct {
	base  string
	key   string
	model string
	http  *httpx.Client
}

// NewOpenAICompat returns a client for the endpoint. A non-empty proxy URL
// (https://user:pass@host:443) routes every request through it; it is never
// echoed in errors, it carries the credentials. Whether a proxy is mandatory
// for this endpoint is the caller's check (ProxyRequired).
func NewOpenAICompat(base, key, model, proxy string) (*OpenAICompat, error) {
	if base == "" {
		base = openAIDefaultBase
	}

	// config.Validate checks this at startup; the constructor refuses too, so
	// no other caller can send a paid personal key out directly
	required, err := ProxyRequired(base)
	if err != nil {
		return nil, err
	}

	if required && proxy == "" {
		return nil, ErrProxyRequired
	}

	var opts []httpx.Option

	if proxy != "" {
		rt, err := ProxyTransport(proxy)
		if err != nil {
			return nil, err
		}

		opts = append(opts, httpx.WithTransport(rt))
	}

	return &OpenAICompat{base: base, key: key, model: model, http: httpx.New("openai-compat", 2, opts...)}, nil
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
