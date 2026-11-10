// Package app assembles the shared runtime — config into clients (data
// sources plus LLM backends) — so the CLI (cmd/verdict) and the daemon
// (cmd/verdictd) construct identical pipelines. It lives under cmd/ because
// it is the one place besides the mains that may read the config.
package app

import (
	"log/slog"
	"time"

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/consensus"
	"github.com/roman-redl/verdict/internal/crossref"
	"github.com/roman-redl/verdict/internal/europepmc"
	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/openalex"
	"github.com/roman-redl/verdict/internal/pdf"
	"github.com/roman-redl/verdict/internal/pipeline"
	"github.com/roman-redl/verdict/internal/pubmed"
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

	return pipeline.New(PipelineSettings(cfg), clients, log, time.Now)
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
		DiscoverySources: cfg.DiscoverySources,
		NoScreen:         cfg.NoScreen,
		TextMode:         cfg.TextMode,
		HybridPivotalMax: cfg.HybridPivotalMax,
		EnrichCacheTTL:   time.Duration(cfg.EnrichCacheTTLDays) * day,
		CritiqueCacheTTL: time.Duration(cfg.CritiqueCacheTTLDays) * day,
	}
}

// BuildClients assembles the pipeline's clients from the config and the
// run options (per-run LLM command/model overrides included). The pipeline
// roles get their own backends — a cheap planner screens the discovery pool,
// the critic reads the abstracts, a strong deep critic re-reads the pivotal
// studies in full, a strong judge writes the verdict — each with the model and
// effort of its role.
func BuildClients(cfg *config.Config, log *slog.Logger, opts pipeline.Options) (pipeline.Clients, error) {
	spec := func(role llm.Role, roleModel string) CompleterSpec {
		return CompleterSpec{Role: role, Command: opts.LLMCommand, Model: roleModel, RunModel: opts.LLMModel, Deep: opts.Deep}
	}

	// the plan role screens the discovery pool: a cheap model, no run flag of its own
	plan, err := BuildCompleter(cfg, log, spec(llm.RolePlan, ""))
	if err != nil {
		return pipeline.Clients{}, err
	}

	crit, err := BuildCompleter(cfg, log, spec(llm.RoleCritique, opts.ModelCritique))
	if err != nil {
		return pipeline.Clients{}, err
	}

	deep, err := BuildCompleter(cfg, log, spec(llm.RoleDeep, opts.ModelDeep))
	if err != nil {
		return pipeline.Clients{}, err
	}

	synth, err := BuildCompleter(cfg, log, spec(llm.RoleSynthesis, opts.ModelSynthesis))
	if err != nil {
		return pipeline.Clients{}, err
	}

	epmc := europepmc.New() // no key needed; abstracts and full texts
	// the page size is the plan's to cap: the response says which one applied
	search := consensus.New(cfg.ConsensusAPIKey, cfg.ConsensusPageSize)

	clients := pipeline.Clients{
		Citations:    scite.New(cfg.SciteAPIKey),      // key is optional
		Works:        openalex.New(cfg.OpenAlexEmail), // no key needed
		Notices:      crossref.New(cfg.OpenAlexEmail), // no key needed
		Scholar:      s2.New(cfg.S2APIKey),            // key optional
		Abstracts:    epmc,                            // abstracts OpenAlex lacks
		FullTexts:    epmc,                            // OA full texts as XML, before the PDF
		Commentary:   pubpeer.New(cfg.PubPeerDevKey),  // behind a devkey; HasKey gates the check
		Search:       search,                          // mode B; key optional, HasKey gates discovery
		PDFs:         pdf.NewDownloader(),
		PDFText:      pdf.NewExtractor(log),
		PubMed:       pubmed.New(cfg.NCBIAPIKey, cfg.OpenAlexEmail),
		LLMPlan:      plan,
		LLM:          crit,
		LLMDeep:      deep,
		LLMSynthesis: synth,
	}

	return clients, nil
}
