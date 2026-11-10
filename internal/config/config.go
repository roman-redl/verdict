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

// maxConsensusPageSize is the largest page_size the Consensus API accepts.
const maxConsensusPageSize = 1000

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
	S2APIKey        string `env:"S2_API_KEY"`        // optional: raises Semantic Scholar's free anonymous limits
	PubPeerDevKey   string `env:"PUBPEER_DEVKEY"`    // optional: enables the PubPeer commentary check
	ConsensusAPIKey string `env:"CONSENSUS_API_KEY"` // optional: mode B — semantic discovery via the Consensus API
	// JinaAPIKey is optional: the Jina Reader (the last full-text source)
	// works without it at 20 requests a minute, a free key raises that to 500.
	JinaAPIKey string `env:"JINA_API_KEY"`
	// NCBIAPIKey is optional: PubMed's E-utilities allow 3 requests a second
	// without it and 10 with it. A secret: it goes into requests and nowhere else.
	NCBIAPIKey string `env:"NCBI_API_KEY"`

	// LLM backend for the analytical stages: "" (auto: always claude-cli) |
	// anthropic | claude-cli | openai-compat.
	LLMBackend llm.Backend `env:"LLM_BACKEND"`
	// ClaudeWrapper is the wrapper of the agent session this process runs in:
	// the wrappers export CLAUDE_WRAPPER. It beats LLM_COMMAND — .env serves
	// runs outside a session (a terminal, verdictd).
	ClaudeWrapper string `env:"CLAUDE_WRAPPER"`
	// LLMCommand is the claude-cli wrapper (claude-max, claude-zai, ...) for
	// runs outside an agent session; empty = claude-max. Never the bare
	// `claude`: see Validate.
	LLMCommand string `env:"LLM_COMMAND"`
	// LLMModel is a model name or an alias (opus, sonnet, haiku, fable —
	// they resolve in every wrapper). It is the fallback of the role models;
	// empty = the default of each role.
	LLMModel          string `env:"LLM_MODEL"`
	LLMModelPlan      string `env:"LLM_MODEL_PLAN"`
	LLMModelCritique  string `env:"LLM_MODEL_CRITIQUE"`
	LLMModelDeep      string `env:"LLM_MODEL_DEEP"`
	LLMModelSynthesis string `env:"LLM_MODEL_SYNTHESIS"`
	LLMModelAsk       string `env:"LLM_MODEL_ASK"`
	// LLMTimeout caps one claude-cli attempt.
	LLMTimeout time.Duration `env:"LLM_TIMEOUT" envDefault:"10m"`
	// LLMConcurrency is the LLM stage parallelism; LLMChunk is the number of
	// studies per critique call (1 = one call per study). 15 agrees with 5 as
	// closely as two runs at 5 agree with each other, with half the calls; the
	// output budget of a batch call grows with its size.
	LLMConcurrency int `env:"LLM_CONCURRENCY" envDefault:"4"`
	LLMChunk       int `env:"LLM_CHUNK" envDefault:"15"`

	// Anthropic Messages API (LLM_BACKEND=anthropic only). The personal key
	// goes through an HTTPS proxy, never directly; the model is an exact API
	// model id (the API takes no aliases), with no default.
	AnthropicAPIKey string `env:"ANTHROPIC_API_KEY"`
	AnthropicProxy  string `env:"ANTHROPIC_PROXY"`
	AnthropicModel  string `env:"ANTHROPIC_MODEL"`

	// OpenAI-compatible backend (LLM_BACKEND=openai-compat only). OpenAIProxy
	// is an HTTPS proxy URL: mandatory for the paid personal-key providers
	// (OpenAI, Groq — see llm.ProxyRequired), optional for other hosts, and
	// always used when set.
	OpenAIBaseURL string `env:"OPENAI_BASE_URL"`
	OpenAIKey     string `env:"OPENAI_API_KEY"`
	OpenAIProxy   string `env:"OPENAI_PROXY"`

	// MaxPapers caps the papers sent to the critic (~1 critique call per
	// LLM_CHUNK papers); discovery looks at a wider pool and keeps the best.
	MaxPapers   int `env:"MAX_PAPERS" envDefault:"40"`
	Concurrency int `env:"CONCURRENCY" envDefault:"4"`
	// TextMode selects what the critic reads: lean (abstracts only), hybrid
	// (abstracts first, then OA full texts for the pivotal studies — the
	// default), full (OA full texts for every paper).
	TextMode pipeline.TextMode `env:"TEXT_MODE" envDefault:"hybrid"`
	// HybridPivotalMax caps how many pivotal studies the hybrid mode deepens
	// with full texts (the tail of the critique stage, before the synthesis).
	HybridPivotalMax int `env:"HYBRID_PIVOTAL_MAX" envDefault:"7"`
	// ConsensusPages caps the Consensus search pages fetched per discovery
	// query: with the default page size, 1 page is up to 100 candidates and
	// 1 API call per query (measured: a second page adds almost nothing).
	ConsensusPages int `env:"CONSENSUS_PAGES" envDefault:"1"`
	// ConsensusPageSize is the page_size of every Consensus search: one API
	// call per 100 papers returned. The plan caps it (Free 20, Pro and Teams
	// 300, Deep 750; the response says what was applied), so the default of
	// 100 is one call per page on a paid plan.
	ConsensusPageSize int `env:"CONSENSUS_PAGE_SIZE" envDefault:"100"`
	// DiscoverySources are the mode B channels (consensus, pubmed, review_refs;
	// any subset, taken literally); the run's --discovery-sources overrides it.
	// Empty = consensus and review_refs, with PubMed by PubMed below.
	DiscoverySources pipeline.DiscoverySources `env:"DISCOVERY_SOURCES"`
	// PubMed is the PubMed channel's mode: auto (the default) runs it when the
	// Consensus pool holds fewer than 2 systematic reviews, on always, off
	// never; the run's --pubmed overrides it. Empty = auto, or the channel
	// list when DISCOVERY_SOURCES is set.
	PubMed pipeline.PubMedMode `env:"PUBMED_DISCOVERY"`
	// NoScreen skips the LLM screening of the discovery pool: every candidate
	// counts as included (an audit switch, off by default).
	NoScreen bool `env:"NO_SCREEN"`
	// EnrichCacheTTLDays is the freshness window, in days, of the per-topic
	// DOI enrichment cache (OpenAlex/Crossref lookups); 0 disables the cache.
	EnrichCacheTTLDays int `env:"ENRICH_CACHE_TTL_DAYS" envDefault:"30"`
	// CritiqueCacheTTLDays is the freshness window, in days, of the per-topic
	// critique cache ((study, question) → critique); 0 disables the cache.
	CritiqueCacheTTLDays int `env:"CRITIQUE_CACHE_TTL_DAYS" envDefault:"30"`
}

// validateBackend checks the credentials of an explicitly chosen backend.
func (c *Config) validateBackend() []error {
	var errs []error

	switch c.LLMBackend {
	case llm.BackendAnthropic:
		for _, f := range []struct{ name, val string }{
			{"ANTHROPIC_API_KEY", c.AnthropicAPIKey},
			{"ANTHROPIC_PROXY", c.AnthropicProxy},
			{"ANTHROPIC_MODEL", c.AnthropicModel},
		} {
			if f.val == "" {
				errs = append(errs, fmt.Errorf("LLM_BACKEND=anthropic requires %s", f.name))
			}
		}

		if c.AnthropicProxy != "" {
			// the error does not quote the value: the proxy URL carries credentials
			if _, err := llm.ProxyTransport(c.AnthropicProxy); err != nil {
				errs = append(errs, fmt.Errorf("ANTHROPIC_PROXY is not usable: %w", err))
			}
		}
	case llm.BackendOpenAICompat:
		if c.OpenAIKey == "" {
			errs = append(errs, errors.New("LLM_BACKEND=openai-compat requires OPENAI_API_KEY"))
		}

		errs = append(errs, c.validateOpenAIProxy()...)
	case llm.BackendAuto, llm.BackendClaudeCLI:
		// no credentials to check: the claude-cli wrapper holds its own login
	}

	return errs
}

// validateOpenAIProxy enforces the owner's rule: a personal paid API key
// (OpenAI, Groq) never goes out from the local address, only through the VPS
// proxy. The error texts do not quote the URLs: they may carry credentials.
func (c *Config) validateOpenAIProxy() []error {
	var errs []error

	required, err := llm.ProxyRequired(c.OpenAIBaseURL)
	if err != nil {
		errs = append(errs, fmt.Errorf("OPENAI_BASE_URL is not usable: %w", err))
	}

	if required && c.OpenAIProxy == "" {
		errs = append(errs, errors.New("personal paid API keys never go out directly: set OPENAI_PROXY "+
			"(OPENAI_BASE_URL points to a provider that requires it)"))
	}

	if c.OpenAIProxy != "" {
		if _, err := llm.ProxyTransport(c.OpenAIProxy); err != nil {
			errs = append(errs, fmt.Errorf("OPENAI_PROXY is not usable: %w", err))
		}
	}

	return errs
}

// Validate checks the rules a struct tag cannot express: the backend's
// credentials, the wrapper and the value ranges.
func (c *Config) Validate() error {
	var errs []error

	errs = append(errs, c.validateBackend()...)

	// The session wrapper and --llm-command are checked where they are
	// resolved (cmd/internal/app); LLM_COMMAND is checked here.
	if err := llm.CheckWrapper(c.LLMCommand); err != nil {
		errs = append(errs, fmt.Errorf("LLM_COMMAND %w", err))
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

	// (page+1)*page_size <= 1000 is the API's own bound
	if c.ConsensusPageSize < 1 || c.ConsensusPageSize > maxConsensusPageSize {
		errs = append(errs, fmt.Errorf("CONSENSUS_PAGE_SIZE must be between 1 and %d, got %d",
			maxConsensusPageSize, c.ConsensusPageSize))
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
