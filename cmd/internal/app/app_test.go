package app

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/pipeline"
)

const (
	modelX  = "claude-x"
	wrapper = "claude-zai"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func openAICompatConfig() *config.Config {
	return &config.Config{LLMBackend: llm.BackendOpenAICompat, OpenAIKey: "k"}
}

func TestBuildCompleterPicksTheBackend(t *testing.T) {
	tests := []struct {
		name     string
		cfg      config.Config
		model    string
		wantName string
	}{
		{"auto without keys is the claude-cli wrapper", config.Config{LLMCommand: "claude-ds"}, "", "claude-cli:claude-ds"},
		{
			"auto prefers the Anthropic key",
			config.Config{AnthropicAPIKey: "k", AnthropicModel: modelX},
			"", "anthropic:claude-x",
		},
		{
			"a global model beats the Anthropic default",
			config.Config{AnthropicAPIKey: "k", AnthropicModel: modelX, LLMModel: "claude-y"},
			"", "anthropic:claude-y",
		},
		{
			"a per-stage model beats both",
			config.Config{AnthropicAPIKey: "k", AnthropicModel: modelX, LLMModel: "claude-y"},
			"claude-z", "anthropic:claude-z",
		},
		{
			"auto falls back to an OpenAI-compatible key",
			config.Config{OpenAIKey: "k", LLMModel: "m1"},
			"", "openai-compat:m1",
		},
		{
			"an explicit backend wins over the keys",
			config.Config{LLMBackend: llm.BackendClaudeCLI, AnthropicAPIKey: "k", LLMCommand: wrapper},
			"", "claude-cli:claude-zai",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.LLMTimeout = time.Minute

			c, err := BuildCompleter(&tt.cfg, quietLog(), tt.model)
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, c.Name())
		})
	}
}

func TestBuildCompleterOpenAICompatNeedsAModel(t *testing.T) {
	_, err := BuildCompleter(openAICompatConfig(), quietLog(), "")
	require.ErrorContains(t, err, "LLM_MODEL")

	c, err := BuildCompleter(openAICompatConfig(), quietLog(), "stage-model")
	require.NoError(t, err, "a per-stage model is enough")
	assert.Equal(t, "openai-compat:stage-model", c.Name())
}

// The critic and the judge default to one backend; any per-stage override
// builds the judge separately (cheap critic + strong judge).
func TestBuildClientsSplitsCriticAndJudge(t *testing.T) {
	cfg := config.Config{LLMCommand: wrapper, LLMTimeout: time.Minute}

	same, err := BuildClients(&cfg, quietLog(), pipeline.Options{})
	require.NoError(t, err)
	assert.Equal(t, same.LLM.Name(), same.LLMSynthesis.Name())
	assert.NotNil(t, same.Citations)
	assert.NotNil(t, same.Works)
	assert.NotNil(t, same.PDFs)
	assert.NotNil(t, same.PDFText)

	cfg.LLMCommandCritique = "claude-zai-flash"
	cfg.LLMModelSynthesis = "strong-model"

	split, err := BuildClients(&cfg, quietLog(), pipeline.Options{})
	require.NoError(t, err)
	assert.Equal(t, "claude-cli:claude-zai-flash", split.LLM.Name(), "the critic uses the cheap wrapper")
	assert.Equal(t, "claude-cli:claude-zai", split.LLMSynthesis.Name(), "the judge keeps the default wrapper")

	// per-run overrides beat the environment
	run, err := BuildClients(&cfg, quietLog(), pipeline.Options{LLMCommand: "claude-ds", ModelSynthesis: "judge"})
	require.NoError(t, err)
	assert.Equal(t, "claude-cli:claude-zai-flash", run.LLM.Name(), "a critique wrapper from the environment still applies")
	assert.Equal(t, "claude-cli:claude-ds", run.LLMSynthesis.Name())
}

func TestBuildClientsPropagatesBackendErrors(t *testing.T) {
	_, err := BuildClients(openAICompatConfig(), quietLog(), pipeline.Options{})
	require.Error(t, err)

	_, err = BuildPipeline(openAICompatConfig(), quietLog(), pipeline.Options{})
	require.Error(t, err)
}

func TestPipelineSettings(t *testing.T) {
	got := PipelineSettings(&config.Config{
		MaxPapers: 15, Concurrency: 4, LLMConcurrency: 3, LLMChunk: 5, ConsensusPages: 2,
		TextMode: pipeline.TextFull, HybridPivotalMax: 6, EnrichCacheTTLDays: 30, CritiqueCacheTTLDays: 0,
	})

	assert.Equal(t, pipeline.Settings{
		MaxPapers: 15, Concurrency: 4, LLMConcurrency: 3, LLMChunk: 5, ConsensusPages: 2,
		TextMode: pipeline.TextFull, HybridPivotalMax: 6,
		EnrichCacheTTL: 30 * 24 * time.Hour, CritiqueCacheTTL: 0,
	}, got, "cache TTLs in days become durations; zero stays zero (cache off)")
}

func TestBuildPipeline(t *testing.T) {
	cfg := config.Config{LLMCommand: wrapper, LLMTimeout: time.Minute}

	p, err := BuildPipeline(&cfg, quietLog(), pipeline.Options{})
	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestVersionFallsBackToBuildInfo(t *testing.T) {
	// under `go test` there is no VCS stamp for the test binary: dev
	assert.NotEmpty(t, Version(""))
	assert.NotEmpty(t, Version(DevVersion))
}
