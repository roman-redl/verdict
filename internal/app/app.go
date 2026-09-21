// Package app assembles the shared runtime — config into clients (data
// sources plus LLM backends) — so the CLI (cmd/verdict) and the daemon
// (cmd/verdictd) construct identical pipelines.
package app

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/crossref"
	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/openalex"
	"github.com/roman-redl/verdict/internal/pipeline"
	"github.com/roman-redl/verdict/internal/pubpeer"
	"github.com/roman-redl/verdict/internal/s2"
	"github.com/roman-redl/verdict/internal/scite"
)

// BuildCompleter picks the LLM backend; model is a per-stage override
// (empty = the config default). Explicit LLM_BACKEND, otherwise auto —
// Anthropic API when a key is present, then OpenAI-compatible, else
// claude-cli (existing Claude Code tariffs, no tokens to buy).
func BuildCompleter(cfg *config.Config, log *slog.Logger, model string) (llm.Completer, error) {
	backend := cfg.LLMBackend
	if backend == "" {
		switch {
		case cfg.AnthropicAPIKey != "":
			backend = "anthropic"
		case cfg.OpenAIKey != "":
			backend = "openai-compat"
		default:
			backend = "claude-cli"
		}
	}
	switch backend {
	case "anthropic":
		if cfg.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("LLM_BACKEND=anthropic but ANTHROPIC_API_KEY is empty")
		}
		m := FirstNonEmpty(model, cfg.LLMModel, cfg.AnthropicModel)
		return llm.NewAnthropic(cfg.AnthropicAPIKey, m), nil
	case "openai-compat":
		if cfg.OpenAIKey == "" {
			return nil, fmt.Errorf("LLM_BACKEND=openai-compat but OPENAI_API_KEY is empty")
		}
		m := FirstNonEmpty(model, cfg.LLMModel)
		if m == "" {
			return nil, fmt.Errorf("openai-compat requires LLM_MODEL (the endpoint's model name)")
		}
		return llm.NewOpenAICompat(cfg.OpenAIBaseURL, cfg.OpenAIKey, m), nil
	case "claude-cli":
		if cfg.LLMCommand == "claude-zai" {
			log.Warn("claude-zai: the very first request blocks the corporate gateway " +
				"for hours (one-way cooldown) — make sure corporate models are not needed soon")
		}
		m := FirstNonEmpty(model, cfg.LLMModel)
		log.Info("LLM backend", "command", cfg.LLMCommand,
			"model", FirstNonEmpty(m, "(wrapper default)"))
		return llm.NewClaudeCLI(cfg.LLMCommand, m,
			time.Duration(cfg.LLMTimeoutSec)*time.Second), nil
	}
	return nil, fmt.Errorf("unknown LLM_BACKEND: %s", backend)
}

// BuildClients assembles the pipeline's clients from the config and the
// run options (per-run LLM command/model overrides included).
func BuildClients(cfg *config.Config, log *slog.Logger, opts pipeline.Options) (pipeline.Clients, error) {
	cfgRun := *cfg
	if opts.LLMCommand != "" {
		cfgRun.LLMCommand = opts.LLMCommand
	}
	crit, err := BuildCompleter(&cfgRun, log,
		FirstNonEmpty(opts.ModelCritique, cfgRun.LLMModelCritique, opts.LLMModel))
	if err != nil {
		return pipeline.Clients{}, err
	}
	synth := crit
	if m := FirstNonEmpty(opts.ModelSynthesis, cfgRun.LLMModelSynthesis, opts.LLMModel); m != "" {
		if synth, err = BuildCompleter(&cfgRun, log, m); err != nil {
			return pipeline.Clients{}, err
		}
	}
	clients := pipeline.Clients{
		Scite:        scite.New(cfg.SciteAPIKey),      // key is optional
		OpenAlex:     openalex.New(cfg.OpenAlexEmail), // no key needed
		Crossref:     crossref.New(cfg.OpenAlexEmail), // no key needed
		S2:           s2.New(cfg.S2APIKey),            // key optional
		PubPeer:      pubpeer.New(cfg.PubPeerDevKey),  // behind a devkey
		LLM:          crit,
		LLMSynthesis: synth,
	}
	return clients, nil
}

func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
