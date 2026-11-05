// Package config is the process configuration, read from the environment by
// the executables in cmd/ (env.Parse in main, then Validate). Nothing else
// imports it: adapters and the pipeline receive plain values.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// The values of LOG_FORMAT.
const (
	LogText = "text"
	LogJSON = "json"
)

// Config is the full environment of verdict and verdictd. Durations use Go
// syntax (LLM_TIMEOUT=10m); a malformed value is a startup error.
type Config struct {
	// LogLevel is the slog level (debug, info, warn, error); LogFormat is
	// "text" for a terminal or "json" for a container.
	LogLevel  slog.Level `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat string     `env:"LOG_FORMAT" envDefault:"text"`

	SciteAPIKey     string `env:"SCITE_API_KEY"`
	OpenAlexEmail   string `env:"OPENALEX_EMAIL"`
	AnthropicAPIKey string `env:"ANTHROPIC_API_KEY"`
	AnthropicModel  string `env:"ANTHROPIC_MODEL" envDefault:"claude-sonnet-5"`
	S2APIKey        string `env:"S2_API_KEY"`        // optional: raises Semantic Scholar's free anonymous limits
	PubPeerDevKey   string `env:"PUBPEER_DEVKEY"`    // optional: enables the PubPeer commentary check
	ConsensusAPIKey string `env:"CONSENSUS_API_KEY"` // optional: mode B — semantic discovery via the Consensus API

	// LLM backend for the analytical stages: "" (auto) | anthropic | claude-cli
	// | openai-compat.
	LLMBackend llm.Backend `env:"LLM_BACKEND"`
	// LLMCommand is the claude-cli wrapper from ~/.zshrc; the per-stage
	// overrides (cheap critic, strong judge) fall back to it when empty.
	LLMCommand          string `env:"LLM_COMMAND" envDefault:"claude-zai"`
	LLMCommandCritique  string `env:"LLM_COMMAND_CRITIQUE"`
	LLMCommandSynthesis string `env:"LLM_COMMAND_SYNTHESIS"`
	// LLMModel is an exact model name (aliases do not resolve in claude-cli);
	// the per-stage overrides fall back to it when empty.
	LLMModel          string        `env:"LLM_MODEL"`
	LLMModelCritique  string        `env:"LLM_MODEL_CRITIQUE"`
	LLMModelSynthesis string        `env:"LLM_MODEL_SYNTHESIS"`
	LLMTimeout        time.Duration `env:"LLM_TIMEOUT" envDefault:"10m"`
	// LLMConcurrency is the LLM stage parallelism; LLMChunk is the number of
	// studies per critique call (1 = one call per study).
	LLMConcurrency int `env:"LLM_CONCURRENCY" envDefault:"4"`
	LLMChunk       int `env:"LLM_CHUNK" envDefault:"5"`

	// OpenAI-compatible backend (OpenRouter/DeepSeek/...).
	OpenAIBaseURL string `env:"OPENAI_BASE_URL"`
	OpenAIKey     string `env:"OPENAI_API_KEY"`

	MaxPapers   int `env:"MAX_PAPERS" envDefault:"15"`
	Concurrency int `env:"CONCURRENCY" envDefault:"4"`
	// TextMode selects what the critic reads: lean (abstracts only), hybrid
	// (abstracts first, then OA full texts for the pivotal studies — the
	// default), full (OA full texts for every paper).
	TextMode pipeline.TextMode `env:"TEXT_MODE" envDefault:"hybrid"`
	// HybridPivotalMax caps how many pivotal studies the hybrid mode deepens
	// with full texts after the first synthesis.
	HybridPivotalMax int `env:"HYBRID_PIVOTAL_MAX" envDefault:"5"`
	// ConsensusPages caps the Consensus search pages fetched per discovery
	// query (quota: each HTTP request plausibly consumes one of the plan's
	// monthly uses).
	ConsensusPages int `env:"CONSENSUS_PAGES" envDefault:"3"`
	// EnrichCacheTTLDays is the freshness window, in days, of the per-topic
	// DOI enrichment cache (OpenAlex/Crossref lookups); 0 disables the cache.
	EnrichCacheTTLDays int `env:"ENRICH_CACHE_TTL_DAYS" envDefault:"30"`
	// CritiqueCacheTTLDays is the freshness window, in days, of the per-topic
	// critique cache ((study, question) → critique); 0 disables the cache.
	CritiqueCacheTTLDays int `env:"CRITIQUE_CACHE_TTL_DAYS" envDefault:"30"`
}

// Validate checks the rules a struct tag cannot express: the backend's
// credentials and the value ranges.
func (c *Config) Validate() error {
	var errs []error

	switch c.LLMBackend {
	case llm.BackendAnthropic:
		if c.AnthropicAPIKey == "" {
			errs = append(errs, errors.New("LLM_BACKEND=anthropic requires ANTHROPIC_API_KEY"))
		}
	case llm.BackendOpenAICompat:
		if c.OpenAIKey == "" {
			errs = append(errs, errors.New("LLM_BACKEND=openai-compat requires OPENAI_API_KEY"))
		}
	case llm.BackendAuto, llm.BackendClaudeCLI:
		// no credentials to check: auto falls back to the claude-cli wrapper
	}

	if c.LogFormat != LogText && c.LogFormat != LogJSON {
		errs = append(errs, fmt.Errorf("LOG_FORMAT must be %q or %q, got %q", LogText, LogJSON, c.LogFormat))
	}

	if c.LLMTimeout <= 0 {
		errs = append(errs, fmt.Errorf("LLM_TIMEOUT must be positive, got %s", c.LLMTimeout))
	}

	for _, f := range []struct {
		name string
		val  int
	}{
		{"LLM_CONCURRENCY", c.LLMConcurrency},
		{"LLM_CHUNK", c.LLMChunk},
		{"MAX_PAPERS", c.MaxPapers},
		{"CONCURRENCY", c.Concurrency},
		{"HYBRID_PIVOTAL_MAX", c.HybridPivotalMax},
		{"CONSENSUS_PAGES", c.ConsensusPages},
	} {
		if f.val < 1 {
			errs = append(errs, fmt.Errorf("%s must be at least 1, got %d", f.name, f.val))
		}
	}

	for _, f := range []struct {
		name string
		val  int
	}{
		{"ENRICH_CACHE_TTL_DAYS", c.EnrichCacheTTLDays},
		{"CRITIQUE_CACHE_TTL_DAYS", c.CritiqueCacheTTLDays},
	} {
		if f.val < 0 {
			errs = append(errs, fmt.Errorf("%s must not be negative, got %d", f.name, f.val))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}

	return nil
}
