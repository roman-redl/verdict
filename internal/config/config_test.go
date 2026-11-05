package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const backendAnthropic = "anthropic"

// valid returns a configuration that passes Validate, as env.Parse would
// build it from an empty environment.
func valid() Config {
	return Config{
		LLMTimeout:     10 * time.Minute,
		LLMConcurrency: 4, LLMChunk: 5,
		MaxPapers: 15, Concurrency: 4,
		HybridPivotalMax: 5, ConsensusPages: 3,
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
			c.LLMBackend = backendAnthropic
		}},
		{name: "anthropic with a key", mutate: func(c *Config) {
			c.LLMBackend, c.AnthropicAPIKey = backendAnthropic, "k"
		}},
		{name: "openai-compat needs a key", wantErr: "OPENAI_API_KEY", mutate: func(c *Config) {
			c.LLMBackend = "openai-compat"
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
	c.LLMBackend = backendAnthropic
	c.MaxPapers = 0

	err := c.Validate()
	require.ErrorContains(t, err, "ANTHROPIC_API_KEY")
	require.ErrorContains(t, err, "MAX_PAPERS")
}
