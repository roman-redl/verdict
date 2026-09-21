// Package config reads keys and settings from the environment; a .env file
// in the working directory is picked up automatically (variables that are
// already set are never overwritten).
package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	SciteAPIKey     string
	OpenAlexEmail   string
	AnthropicAPIKey string
	AnthropicModel  string
	S2APIKey        string // optional: raises Semantic Scholar's free anonymous limits
	PubPeerDevKey   string // optional: enables the PubPeer commentary check

	// LLM backend for the analytical stages.
	LLMBackend        string // "" (auto) | anthropic | claude-cli | openai-compat
	LLMCommand        string // claude-cli: wrapper from ~/.zshrc (claude-zai, claude, ...)
	LLMModel          string // exact model name (aliases do not resolve in claude-cli)
	LLMModelCritique  string // per-stage override: cheap critic + strong judge
	LLMModelSynthesis string // (empty = LLM_MODEL)
	LLMTimeoutSec     int
	LLMConcurrency    int // LLM stage parallelism

	// OpenAI-compatible backend (OpenRouter/DeepSeek/...).
	OpenAIBaseURL string
	OpenAIKey     string

	MaxPapers   int
	Concurrency int
	// EnrichCacheTTLDays is the freshness window of the per-topic DOI
	// enrichment cache (OpenAlex/Crossref lookups); 0 disables the cache.
	EnrichCacheTTLDays int
}

func Load() *Config {
	loadDotEnv(".env")
	return &Config{
		SciteAPIKey:        os.Getenv("SCITE_API_KEY"),
		OpenAlexEmail:      os.Getenv("OPENALEX_EMAIL"),
		AnthropicAPIKey:    os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:     getenv("ANTHROPIC_MODEL", "claude-sonnet-5"),
		S2APIKey:           os.Getenv("S2_API_KEY"),
		PubPeerDevKey:      os.Getenv("PUBPEER_DEVKEY"),
		LLMBackend:         os.Getenv("LLM_BACKEND"),
		LLMCommand:         getenv("LLM_COMMAND", "claude-zai"),
		LLMModel:           os.Getenv("LLM_MODEL"),
		LLMModelCritique:   os.Getenv("LLM_MODEL_CRITIQUE"),
		LLMModelSynthesis:  os.Getenv("LLM_MODEL_SYNTHESIS"),
		LLMTimeoutSec:      getint("LLM_TIMEOUT", 600),
		LLMConcurrency:     getint("LLM_CONCURRENCY", 4),
		OpenAIBaseURL:      os.Getenv("OPENAI_BASE_URL"),
		OpenAIKey:          os.Getenv("OPENAI_API_KEY"),
		MaxPapers:          getint("MAX_PAPERS", 15),
		Concurrency:        getint("CONCURRENCY", 4),
		EnrichCacheTTLDays: getint("ENRICH_CACHE_TTL_DAYS", 30),
	}
}

// loadDotEnv is a minimal .env loader (KEY=VALUE, '#' starts a comment).
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, strings.Trim(strings.TrimSpace(v), `"'`))
		}
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getint(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
