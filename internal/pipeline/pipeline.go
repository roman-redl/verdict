// Package pipeline is the orchestrator: discovery → enrichment → critique →
// synthesis. A full state checkpoint is written after every stage, so a run
// that died midway can be resumed (--resume) without paying for the paid
// APIs twice.
package pipeline

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roman-redl/verdict/internal/consensus"
	"github.com/roman-redl/verdict/internal/crossref"
	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/openalex"
	"github.com/roman-redl/verdict/internal/pubpeer"
	"github.com/roman-redl/verdict/internal/s2"
	"github.com/roman-redl/verdict/internal/scite"
)

// Options are the parameters of one run.
type Options struct {
	Question       string // research question; if empty, DOIFile is required
	DOIFile        string // file with a list of DOIs: the manual-corpus mode
	RunDir         string // artifact directory; overrides Topic and the default
	Topic          string // group the run under runs/<topic>/<slug>-<time>
	Resume         bool   // skip stages whose artifacts are already on disk
	StopAfter      string // stop after a stage: "1".."4" or the full name
	MaxPapers      int
	Concurrency    int
	LLMConcurrency int // LLM stage parallelism
	LLMChunk       int // studies per critique call (0 = LLM_CHUNK/config; 1 = one call per study)

	// Discovery controls.
	Queries        []string // semantic queries for mode B (repeatable --queries); defaults to -q
	Engine         Engine   // "" (auto: consensus when the key is present, else openalex) | consensus | openalex
	ConsensusPages int      // max Consensus pages per query (defaults to CONSENSUS_PAGES, 3)

	LLMCommand     string // claude-cli wrapper override for this run
	LLMModel       string // exact model name for both LLM stages
	ModelCritique  string // critique-stage model override
	ModelSynthesis string // synthesis-stage model override

	// ChallengeNote is the user's counterargument to a previous verdict: not
	// data, but the judge must weigh and answer it explicitly.
	ChallengeNote string

	// TextMode selects what the critic reads: lean | hybrid | full
	// ("" = TEXT_MODE from the environment, default hybrid). lean — abstracts
	// only; hybrid — abstracts first, then OA full texts for the pivotal
	// studies after the first synthesis (one deepening pass); full — OA full
	// texts for every paper at enrichment. A PDF pre-placed in the run's
	// fulltext/ is read in every mode: an explicit user action beats the mode.
	TextMode TextMode
}

// Clients are the pipeline's external systems.
type Clients struct {
	Scite     *scite.Client
	OpenAlex  *openalex.Client
	Crossref  *crossref.Client
	S2        *s2.Client        // second metadata source: TLDRs, abstracts OpenAlex misses, OA PDFs
	PubPeer   *pubpeer.Client   // post-publication commentary (behind PUBPEER_DEVKEY)
	Consensus *consensus.Client // mode B: semantic discovery (behind CONSENSUS_API_KEY)
	LLM       llm.Completer
	// LLMSynthesis overrides the judge model when set (cheap critic + strong
	// judge); empty = the same backend as LLM.
	LLMSynthesis llm.Completer
}

// Settings are the pipeline's tunables: process-wide defaults that the
// per-run Options override. Zero or negative limits fall back to 1.
type Settings struct {
	MaxPapers      int
	Concurrency    int // enrichment parallelism
	LLMConcurrency int // LLM stage parallelism
	LLMChunk       int // studies per critique call
	ConsensusPages int // Consensus pages per discovery query
	// TextMode is the default text mode ("" = hybrid).
	TextMode TextMode
	// HybridPivotalMax caps the pivotal studies the hybrid mode deepens.
	HybridPivotalMax int
	// EnrichCacheTTL and CritiqueCacheTTL are the freshness windows of the
	// per-topic caches; zero disables the cache.
	EnrichCacheTTL   time.Duration
	CritiqueCacheTTL time.Duration
}

// Pipeline runs assessments. It holds only dependencies and settings: what
// belongs to one run (its directory, its options) travels in runParams, so
// one instance can serve any number of runs.
type Pipeline struct {
	set     Settings
	clients Clients
	log     *slog.Logger
}

func New(set Settings, clients Clients, log *slog.Logger) *Pipeline {
	return &Pipeline{set: set, clients: clients, log: log}
}

// runParams are the parameters of one run that every stage needs besides the
// shared state.
type runParams struct {
	opts Options // TextMode is resolved
	dir  string  // artifact directory; downloaded and dropped-in PDFs live in dir/fulltext
}

// Stage names double as artifact file names (<name>.json).
const (
	stageDiscovery  = "1-discovery"
	stageEnrichment = "2-enrichment"
	stageCritique   = "3-critique"
	stageSynthesis  = "4-synthesis"
)

type stage struct {
	name string
	run  func(context.Context, runParams, *State) error
}

func (p *Pipeline) stages() []stage {
	return []stage{
		{stageDiscovery, p.discover},
		{stageEnrichment, p.enrich},
		{stageCritique, p.critique},
		{stageSynthesis, p.synthesize},
	}
}

// resolveTextMode settles the effective text mode: the run's flag, else the
// config's, else hybrid. The result is validated again because Options and
// Settings can be built in code, not only parsed from user input.
func resolveTextMode(opt, cfg TextMode) (TextMode, error) {
	return ParseTextMode(string(cmp.Or(opt, cfg, TextHybrid)))
}

// Run executes all stages; returns the final state and the artifact directory.
func (p *Pipeline) Run(ctx context.Context, opts Options) (*State, string, error) {
	dir, err := p.prepareDir(opts)
	if err != nil {
		return nil, "", err
	}

	mode, err := resolveTextMode(opts.TextMode, p.set.TextMode)
	if err != nil {
		return nil, dir, err
	}

	opts.TextMode = mode
	rp := runParams{opts: opts, dir: dir}

	if err := copyInput(rp); err != nil {
		return nil, dir, err
	}

	st := &State{ChallengeNote: opts.ChallengeNote}

	synthesisRanFresh, err := p.runStages(ctx, rp, st)
	if err != nil {
		return nil, dir, err
	}
	// The hybrid second half: after a fresh first-verdict pass, fetch OA full
	// texts for the pivotal studies, re-critique exactly those (the cache keys
	// changed) and re-synthesize. One pass per state — resume does not repeat
	// it; a --stop-after before synthesis skips it (st.Report is nil then).
	if st.Report != nil && opts.TextMode == TextHybrid && !st.HybridDone && synthesisRanFresh {
		if err := p.hybridPass(ctx, rp, st); err != nil {
			return nil, dir, err
		}
	}

	if err := writeReports(dir, st); err != nil {
		return nil, dir, err
	}

	return st, dir, nil
}

// copyInput keeps the raw DOI list in the run directory: it is part of the
// audit trail, and the corpus must be traceable from the artifacts alone.
func copyInput(rp runParams) error {
	if rp.opts.DOIFile == "" {
		return nil
	}

	raw, err := os.ReadFile(rp.opts.DOIFile)
	if err != nil {
		return fmt.Errorf("reading %s: %w", rp.opts.DOIFile, err)
	}
	//nolint:gosec // the run's audit trail: a fixed name inside the run directory, a copy of the input file
	if err := os.WriteFile(filepath.Join(rp.dir, "0-input.txt"), raw, 0o600); err != nil {
		return fmt.Errorf("writing input copy: %w", err)
	}

	return nil
}

// runStages runs the stages in order, saving a full checkpoint after each.
// With Resume, a stage whose artifact is on disk is loaded instead of run. It
// reports whether the synthesis ran in this call (the hybrid pass only
// follows a fresh synthesis).
func (p *Pipeline) runStages(ctx context.Context, rp runParams, st *State) (bool, error) {
	synthesisRanFresh := false

	for _, s := range p.stages() {
		artifact := filepath.Join(rp.dir, s.name+".json")
		if rp.opts.Resume && fileExists(artifact) {
			if err := loadJSON(artifact, st); err != nil {
				return false, fmt.Errorf("resume %s: %w", s.name, err)
			}

			p.log.Info("stage skipped: artifact on disk", "stage", s.name)

			continue
		}

		if s.name == stageSynthesis {
			p.loadVisionNotes(rp.dir, st) // optional agent-written chart critique

			synthesisRanFresh = true
		}

		if err := s.run(ctx, rp, st); err != nil {
			return false, fmt.Errorf("stage %s: %w", s.name, err)
		}
		// A canceled context makes the sources fail one by one without failing
		// the stage; the half-filled state must not be saved as a finished
		// stage, or --resume would skip it.
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("stage %s interrupted: %w", s.name, err)
		}

		if err := saveJSON(artifact, st); err != nil {
			return false, fmt.Errorf("saving %s: %w", s.name, err)
		}

		p.log.Info("stage done", "stage", s.name, "papers", len(st.Papers))

		if stopsAfter(rp.opts.StopAfter, s.name) {
			p.log.Info("stopping after --stop-after", "stage", s.name)
			break
		}
	}

	return synthesisRanFresh, nil
}

// stopsAfter reports whether --stop-after names the stage, by full name or
// by its leading number ("2" stops after 2-enrichment).
func stopsAfter(flag, stage string) bool {
	return flag != "" && (stage == flag || strings.HasPrefix(stage, flag))
}

// writeReports renders the human-readable and machine-readable outputs from
// the final state.
func writeReports(dir string, st *State) error {
	if st.Report != nil {
		if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte(RenderMarkdown(st)), 0o600); err != nil {
			return fmt.Errorf("writing report.md: %w", err)
		}

		csvText, err := RenderCSV(st.Report)
		if err != nil {
			return err
		}

		if err := os.WriteFile(filepath.Join(dir, "matrix.csv"), []byte(csvText), 0o600); err != nil {
			return fmt.Errorf("writing matrix.csv: %w", err)
		}
	}
	// The digest for Q&A (ask) and interactive agents — useful on a partial
	// run too (e.g. after --stop-after 2).
	if err := os.WriteFile(filepath.Join(dir, "context.md"), []byte(RenderContext(st)), 0o600); err != nil {
		return fmt.Errorf("writing context.md: %w", err)
	}

	return nil
}
