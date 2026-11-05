// Package app assembles the shared runtime — config into clients (data
// sources plus LLM backends) — so the CLI (cmd/verdict) and the daemon
// (cmd/verdictd) construct identical pipelines. It lives under cmd/ because
// it is the one place besides the mains that may read the config.
package app

import (
	"cmp"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/consensus"
	"github.com/roman-redl/verdict/internal/crossref"
	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/openalex"
	"github.com/roman-redl/verdict/internal/pipeline"
	"github.com/roman-redl/verdict/internal/pubpeer"
	"github.com/roman-redl/verdict/internal/s2"
	"github.com/roman-redl/verdict/internal/scite"
)

const day = 24 * time.Hour

// BuildPipeline assembles a pipeline for one run: the settings from the
// config, the clients from the config plus the run's overrides.
func BuildPipeline(cfg *config.Config, log *slog.Logger, opts pipeline.Options) (*pipeline.Pipeline, error) {
	clients, err := BuildClients(cfg, log, opts)
	if err != nil {
		return nil, err
	}

	return pipeline.New(PipelineSettings(cfg), clients, log), nil
}

// PipelineSettings maps the environment configuration onto the pipeline's
// own settings (the pipeline never sees the config package).
func PipelineSettings(cfg *config.Config) pipeline.Settings {
	return pipeline.Settings{
		MaxPapers:        cfg.MaxPapers,
		Concurrency:      cfg.Concurrency,
		LLMConcurrency:   cfg.LLMConcurrency,
		LLMChunk:         cfg.LLMChunk,
		ConsensusPages:   cfg.ConsensusPages,
		TextMode:         cfg.TextMode,
		HybridPivotalMax: cfg.HybridPivotalMax,
		EnrichCacheTTL:   time.Duration(cfg.EnrichCacheTTLDays) * day,
		CritiqueCacheTTL: time.Duration(cfg.CritiqueCacheTTLDays) * day,
	}
}

// BuildCompleter picks the LLM backend; model is a per-stage override
// (empty = the config default). Explicit LLM_BACKEND, otherwise auto —
// Anthropic API when a key is present, then OpenAI-compatible, else
// claude-cli (existing Claude Code tariffs, no tokens to buy). Credentials
// of an explicit backend are checked by config.Validate.
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
		m := cmp.Or(model, cfg.LLMModel, cfg.AnthropicModel)

		return llm.NewAnthropic(cfg.AnthropicAPIKey, m), nil
	case "openai-compat":
		m := cmp.Or(model, cfg.LLMModel)
		if m == "" {
			return nil, fmt.Errorf("openai-compat requires LLM_MODEL (the endpoint's model name)")
		}

		return llm.NewOpenAICompat(cfg.OpenAIBaseURL, cfg.OpenAIKey, m), nil
	case "claude-cli":
		if strings.HasPrefix(cfg.LLMCommand, "claude-zai") {
			log.Warn("claude-zai: the very first request blocks the corporate gateway " +
				"for hours (one-way cooldown) — make sure corporate models are not needed soon")
		}

		m := cmp.Or(model, cfg.LLMModel)
		log.Info("LLM backend", "command", cfg.LLMCommand,
			"model", cmp.Or(m, "(wrapper default)"))

		return llm.NewClaudeCLI(cfg.LLMCommand, m, cfg.LLMTimeout), nil
	}

	return nil, fmt.Errorf("unknown LLM_BACKEND: %s", backend)
}

// BuildClients assembles the pipeline's clients from the config and the
// run options (per-run LLM command/model overrides included). The judge
// backend defaults to the critic's; a per-stage command or model override
// (LLM_COMMAND_SYNTHESIS / LLM_MODEL_SYNTHESIS) rebuilds it separately —
// the cheap-critic + strong-judge split.
func BuildClients(cfg *config.Config, log *slog.Logger, opts pipeline.Options) (pipeline.Clients, error) {
	cfgRun := *cfg
	if opts.LLMCommand != "" {
		cfgRun.LLMCommand = opts.LLMCommand
	}

	cfgCrit := cfgRun
	if cfgRun.LLMCommandCritique != "" {
		cfgCrit.LLMCommand = cfgRun.LLMCommandCritique
	}

	crit, err := BuildCompleter(&cfgCrit, log,
		cmp.Or(opts.ModelCritique, cfgRun.LLMModelCritique, opts.LLMModel))
	if err != nil {
		return pipeline.Clients{}, err
	}

	synth := crit

	if cfgRun.LLMCommandSynthesis != "" || opts.ModelSynthesis != "" || cfgRun.LLMModelSynthesis != "" {
		cfgSynth := cfgRun
		if cfgRun.LLMCommandSynthesis != "" {
			cfgSynth.LLMCommand = cfgRun.LLMCommandSynthesis
		}

		synth, err = BuildCompleter(&cfgSynth, log,
			cmp.Or(opts.ModelSynthesis, cfgRun.LLMModelSynthesis, opts.LLMModel))
		if err != nil {
			return pipeline.Clients{}, err
		}
	}

	clients := pipeline.Clients{
		Scite:        scite.New(cfg.SciteAPIKey),         // key is optional
		OpenAlex:     openalex.New(cfg.OpenAlexEmail),    // no key needed
		Crossref:     crossref.New(cfg.OpenAlexEmail),    // no key needed
		S2:           s2.New(cfg.S2APIKey),               // key optional
		PubPeer:      pubpeer.New(cfg.PubPeerDevKey),     // behind a devkey
		Consensus:    consensus.New(cfg.ConsensusAPIKey), // mode B; key optional, HasKey gates discovery
		LLM:          crit,
		LLMSynthesis: synth,
	}

	return clients, nil
}
