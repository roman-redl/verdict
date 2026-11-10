package config

import (
	"fmt"
	"testing"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

const (
	//nolint:gosec // a fake proxy URL with a fake password: the tests never touch the network
	testProxy    = "https://user:s3cret@vps.example.org:443"
	apiModel     = "claude-x"
	deepseekBase = "https://api.deepseek.com/v1"
)

// valid returns a configuration that passes Validate, as env.Parse would
// build it from an empty environment.
func valid() Config {
	return Config{
		LogFormat:      LogText,
		LLMTimeout:     10 * time.Minute,
		LLMConcurrency: 4, LLMChunk: 5,
		MaxPapers: 40, Concurrency: 4,
		HybridPivotalMax: 7, ConsensusPages: 3, ConsensusPageSize: 100,
		EnrichCacheTTLDays: 30, CritiqueCacheTTLDays: 30,
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "defaults pass", mutate: func(*Config) {}},
		{name: "cache disabled by zero TTL", mutate: func(c *Config) {
			c.EnrichCacheTTLDays, c.CritiqueCacheTTLDays = 0, 0
		}},
		{name: "anthropic needs a key", wantErr: "ANTHROPIC_API_KEY", mutate: func(c *Config) {
			c.LLMBackend = llm.BackendAnthropic
			c.AnthropicProxy, c.AnthropicModel = testProxy, apiModel
		}},
		{name: "anthropic needs a proxy", wantErr: "ANTHROPIC_PROXY", mutate: func(c *Config) {
			c.LLMBackend = llm.BackendAnthropic
			c.AnthropicAPIKey, c.AnthropicModel = "k", apiModel
		}},
		{name: "anthropic needs a model, there is no default", wantErr: "ANTHROPIC_MODEL", mutate: func(c *Config) {
			c.LLMBackend = llm.BackendAnthropic
			c.AnthropicAPIKey, c.AnthropicProxy = "k", testProxy
		}},
		{name: "anthropic with key, proxy and model", mutate: func(c *Config) {
			c.LLMBackend = llm.BackendAnthropic
			c.AnthropicAPIKey, c.AnthropicProxy, c.AnthropicModel = "k", testProxy, apiModel
		}},
		{name: "anthropic proxy must be a URL", wantErr: "ANTHROPIC_PROXY is not usable", mutate: func(c *Config) {
			c.LLMBackend = llm.BackendAnthropic
			c.AnthropicAPIKey, c.AnthropicProxy, c.AnthropicModel = "k", "vps.example.org:443", apiModel
		}},
		{name: "an Anthropic key alone does not switch the backend", mutate: func(c *Config) {
			c.AnthropicAPIKey = "k"
		}},
		{name: "a wrapper command passes", mutate: func(c *Config) { c.LLMCommand = "claude-zai-flash" }},
		{name: "the bare claude CLI is no wrapper", wantErr: "LLM_COMMAND must name a wrapper such as claude-max, " +
			"not the bare claude CLI", mutate: func(c *Config) {
			c.LLMCommand = "claude"
		}},
		{name: "a path to the bare claude CLI is no wrapper", wantErr: "not the bare claude CLI", mutate: func(c *Config) {
			c.LLMCommand = "/usr/local/bin/claude"
		}},
		{name: "openai-compat needs a key", wantErr: "OPENAI_API_KEY", mutate: func(c *Config) {
			c.LLMBackend, c.OpenAIProxy = llm.BackendOpenAICompat, testProxy
		}},
		{name: "openai default host needs the proxy", wantErr: "OPENAI_PROXY", mutate: func(c *Config) {
			c.LLMBackend, c.OpenAIKey = llm.BackendOpenAICompat, "k"
		}},
		{name: "explicit openai host needs the proxy", wantErr: "never go out directly", mutate: func(c *Config) {
			c.LLMBackend, c.OpenAIKey, c.OpenAIBaseURL = llm.BackendOpenAICompat, "k", "https://api.openai.com/v1"
		}},
		{name: "a groq subdomain needs the proxy", wantErr: "OPENAI_PROXY", mutate: func(c *Config) {
			c.LLMBackend, c.OpenAIKey, c.OpenAIBaseURL = llm.BackendOpenAICompat, "k", "https://eu.API.groq.com/openai/v1"
		}},
		{name: "openai with the proxy", mutate: func(c *Config) {
			c.LLMBackend, c.OpenAIKey, c.OpenAIProxy = llm.BackendOpenAICompat, "k", testProxy
		}},
		{name: "deepseek needs no proxy", mutate: func(c *Config) {
			c.LLMBackend, c.OpenAIKey, c.OpenAIBaseURL = llm.BackendOpenAICompat, "k", deepseekBase
		}},
		{name: "deepseek may use the proxy", mutate: func(c *Config) {
			c.LLMBackend, c.OpenAIKey, c.OpenAIBaseURL = llm.BackendOpenAICompat, "k", deepseekBase
			c.OpenAIProxy = testProxy
		}},
		{name: "a malformed base URL", wantErr: "OPENAI_BASE_URL is not usable", mutate: func(c *Config) {
			c.LLMBackend, c.OpenAIKey, c.OpenAIBaseURL = llm.BackendOpenAICompat, "k", "not a url"
		}},
		{name: "a malformed openai proxy", wantErr: "OPENAI_PROXY is not usable", mutate: func(c *Config) {
			c.LLMBackend, c.OpenAIKey, c.OpenAIBaseURL = llm.BackendOpenAICompat, "k", deepseekBase
			c.OpenAIProxy = "vps.example.org:443"
		}},
		{name: "an OpenAI key alone does not switch the backend", mutate: func(c *Config) {
			c.OpenAIKey = "k"
		}},
		{name: "json logs", mutate: func(c *Config) {
			c.LogFormat = LogJSON
		}},
		{name: "unknown log format", wantErr: "LOG_FORMAT", mutate: func(c *Config) {
			c.LogFormat = "xml"
		}},
		{name: "non-positive timeout", wantErr: "LLM_TIMEOUT", mutate: func(c *Config) {
			c.LLMTimeout = 0
		}},
		{name: "zero parallelism", wantErr: "LLM_CONCURRENCY", mutate: func(c *Config) {
			c.LLMConcurrency = 0
		}},
		{name: "zero papers", wantErr: "MAX_PAPERS", mutate: func(c *Config) {
			c.MaxPapers = 0
		}},
		{name: "the largest page size", mutate: func(c *Config) {
			c.ConsensusPageSize = 1000
		}},
		{name: "page size below 1", wantErr: "CONSENSUS_PAGE_SIZE", mutate: func(c *Config) {
			c.ConsensusPageSize = 0
		}},
		{name: "page size above the API bound", wantErr: "CONSENSUS_PAGE_SIZE", mutate: func(c *Config) {
			c.ConsensusPageSize = 1001
		}},
		{name: "negative TTL", wantErr: "ENRICH_CACHE_TTL_DAYS", mutate: func(c *Config) {
			c.EnrichCacheTTLDays = -1
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := valid()
			tt.mutate(&c)

			err := c.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// Every failed rule is reported at once, not one per run.
func TestValidateReportsAllProblems(t *testing.T) {
	t.Parallel()

	c := valid()
	c.LLMBackend = llm.BackendAnthropic
	c.MaxPapers = 0

	err := c.Validate()
	require.ErrorContains(t, err, "ANTHROPIC_API_KEY")
	require.ErrorContains(t, err, "ANTHROPIC_PROXY")
	require.ErrorContains(t, err, "ANTHROPIC_MODEL")
	require.ErrorContains(t, err, "MAX_PAPERS")
}

// The corpus and deepening caps default to what the measured discovery pools
// need: 12-44 on-topic papers per question, up to 7 studies with a full text.
func TestCapDefaults(t *testing.T) {
	t.Parallel()

	var c Config

	require.NoError(t, env.ParseWithOptions(&c, env.Options{Environment: map[string]string{}}))
	require.Equal(t, 40, c.MaxPapers)
	require.Equal(t, 7, c.HybridPivotalMax)
}

// The discovery switches come from the environment: the default is all three
// channels, a bad channel is a startup error.
func TestDiscoveryEnv(t *testing.T) {
	t.Parallel()

	parse := func(environment map[string]string) (Config, error) {
		var c Config

		if err := env.ParseWithOptions(&c, env.Options{Environment: environment}); err != nil {
			return c, fmt.Errorf("parsing the environment: %w", err)
		}

		return c, nil
	}

	c, err := parse(map[string]string{})
	require.NoError(t, err)
	require.Equal(t, pipeline.DiscoverySources{
		model.SourceConsensus, model.SourcePubMed, model.SourceReviewRefs,
	}, c.DiscoverySources)
	require.False(t, c.NoScreen)
	require.Empty(t, c.NCBIAPIKey)

	c, err = parse(map[string]string{
		"DISCOVERY_SOURCES": "pubmed, review_refs", "NO_SCREEN": "true", "NCBI_API_KEY": "k",
	})
	require.NoError(t, err)
	require.Equal(t, pipeline.DiscoverySources{model.SourcePubMed, model.SourceReviewRefs}, c.DiscoverySources)
	require.True(t, c.NoScreen)
	require.Equal(t, "k", c.NCBIAPIKey)

	_, err = parse(map[string]string{"DISCOVERY_SOURCES": "consensus,google"})
	require.ErrorContains(t, err, "unknown discovery source")
}

// The proxy URL carries credentials: an invalid one is never echoed.
func TestValidateDoesNotLeakTheProxy(t *testing.T) {
	t.Parallel()

	c := valid()
	c.LLMBackend, c.AnthropicAPIKey, c.AnthropicModel = llm.BackendAnthropic, "k", apiModel
	c.AnthropicProxy = "ftp://user:s3cret@vps.example.org"

	err := c.Validate()
	require.Error(t, err)
	require.NotContains(t, err.Error(), "s3cret") //nolint:forbidigo // checks the message text for a secret
}
