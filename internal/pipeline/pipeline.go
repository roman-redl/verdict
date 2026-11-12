// Package pipeline is the orchestrator: discovery → enrichment → critique →
// synthesis. In the hybrid text mode the critique stage ends with a deepening
// pass over the pivotal studies, so the synthesis still runs once. A full
// state checkpoint is written after every stage, so a run that died midway
// can be resumed (--resume) without paying for the paid APIs twice.
package pipeline

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	ConsensusPages int      // max Consensus pages per query (defaults to CONSENSUS_PAGES, 1)
	// Sources limits the discovery channels of mode B (nil = DISCOVERY_SOURCES,
	// default consensus and review_refs plus PubMed on auto); PubMed overrides
	// whether the PubMed channel runs ("" = PUBMED_DISCOVERY, then the channel
	// list, then auto); NoScreen skips the LLM screening (every candidate
	// counts as included); PubMedQuery is a ready PubMed topic query that
	// replaces the model-written one.
	Sources     DiscoverySources
	PubMed      PubMedMode
	NoScreen    bool
	PubMedQuery string
	// ExcludeDOIs are papers the run must not see: dropped from the pool as
	// they arrive from every channel (so an excluded review is never mined),
	// and from a --dois list. Each value is a DOI or a comma-separated list;
	// a Cochrane review DOI excludes all versions of its CD number.
	ExcludeDOIs []string

	LLMCommand     string // claude-cli wrapper override for this run
	LLMModel       string // model for every LLM stage (a name or an alias like sonnet | opus)
	ModelCritique  string // critique-stage model override
	ModelDeep      string // model override for the full-text re-critique of the hybrid pass
	ModelSynthesis string // synthesis-stage model override
	// Deep makes the strongest model (fable) the synthesis default; an
	// explicit synthesis model still wins.
	Deep bool

	// ChallengeNote is the user's counterargument to a previous verdict: not
	// data, but the judge must weigh and answer it explicitly.
	ChallengeNote string

	// TextMode selects what the critic reads: lean | hybrid | full
	// ("" = TEXT_MODE from the environment, default hybrid). lean — abstracts
	// only; hybrid — abstracts first, then OA full texts for the pivotal
	// studies inside the critique stage (one deepening pass, before the single
	// synthesis); full — OA full texts for every paper at enrichment. A PDF
	// pre-placed in the run's fulltext/ is read in every mode: an explicit
	// user action beats the mode.
	TextMode TextMode
}

// Clients are the pipeline's external systems, all ports (see ports.go). A
// nil port is a source that is not available: its stage step is skipped.
type Clients struct {
	Citations  Citations      // Scite: smart-citation tallies and the contradiction drill-down
	Works      Works          // OpenAlex: metadata, retraction flags, keyword discovery
	Notices    Notices        // Crossref: retraction and correction notices
	Scholar    Scholar        // Semantic Scholar: TLDRs, abstracts OpenAlex misses, OA PDFs
	Abstracts  Abstracts      // Europe PMC: abstracts the primary sources lack
	Commentary Commentary     // PubPeer: post-publication commentary (behind a developer key)
	Search     SemanticSearch // Consensus: semantic discovery (behind an API key)
	PubMed     PubMed         // PubMed: the topic and review searches of mode B discovery
	Records    PubMedRecords  // PubMed: the trial registration numbers and publication types of full records
	FullTexts  FullTexts      // Europe PMC: open-access full texts as XML, tried before the PDF download
	PMC        PMCArchive     // PubMed Central: ID converter and efetch XML, after Europe PMC
	Landings   Landings       // doi.org: the publisher page a DOI resolves to (for the page reader)
	Reader     PageReader     // Jina Reader: the last resort, a page or a PDF read as text
	PDFs       PDFSource      // open-access PDF download
	PDFText    PDFText        // PDF text extraction
	LLM        Completer      // the critic: the per-paper methodology review
	// LLMPlan screens the discovery pool (the cheap plan-role model); nil
	// skips the screening and the review-reference mining that needs it.
	LLMPlan Completer
	// LLMDeep re-critiques the pivotal studies in the hybrid pass, once their
	// full texts are in; nil = the same backend as LLM.
	LLMDeep Completer
	// LLMSynthesis overrides the judge model when set (cheap critic + strong
	// judge); nil = the same backend as LLM.
	LLMSynthesis Completer
}

// Settings are the pipeline's tunables: process-wide defaults that the
// per-run Options override. Zero or negative limits fall back to 1.
type Settings struct {
	MaxPapers      int
	Concurrency    int // enrichment parallelism
	LLMConcurrency int // LLM stage parallelism
	LLMChunk       int // studies per critique call
	ConsensusPages int // Consensus pages per discovery query
	// DiscoverySources is the default set of mode B channels (empty = consensus
	// and review_refs, PubMed by its mode); PubMed is the default PubMed mode
	// (empty = the channel list, else auto); NoScreen turns the discovery
	// screening off for every run.
	DiscoverySources DiscoverySources
	PubMed           PubMedMode
	NoScreen         bool
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
	now     func() time.Time
}

// New builds a pipeline. now is the clock: it stamps cache entries, reports
// and run directory names, and tests pin it.
func New(set Settings, clients Clients, log *slog.Logger, now func() time.Time) (*Pipeline, error) {
	if log == nil {
		return nil, errors.New("the pipeline needs a logger")
	}

	if now == nil {
		return nil, errors.New("the pipeline needs a clock")
	}

	return &Pipeline{set: set, clients: clients, log: log, now: now}, nil
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
		{stageCritique, p.critiqueStage},
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

	if err := p.runStages(ctx, rp, st); err != nil {
		return nil, dir, err
	}
	// set after the stages: a resumed artifact carries the mode of the run
	// that wrote it, and the report states what this run was asked to read
	st.TextMode = mode

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
// With Resume, a stage whose artifact is on disk is loaded instead of run.
func (p *Pipeline) runStages(ctx context.Context, rp runParams, st *State) error {
	for _, s := range p.stages() {
		artifact := filepath.Join(rp.dir, s.name+".json")
		loaded := rp.opts.Resume && fileExists(artifact)

		var err error
		if loaded {
			err = p.resumeStage(ctx, rp, st, s.name, artifact)
		} else {
			err = p.runStage(ctx, rp, st, s, artifact)
		}

		if err != nil {
			return err
		}
		// a --stop-after at a loaded stage ends the run there too: nothing
		// after it (the deepening included) may start
		if stopsAfter(rp.opts.StopAfter, s.name) {
			p.log.Info("stopping after --stop-after", "stage", s.name)
			break
		}
	}

	return nil
}

// resumeStage loads a stage's finished artifact instead of running the stage.
func (p *Pipeline) resumeStage(ctx context.Context, rp runParams, st *State, name, artifact string) error {
	if err := loadJSON(artifact, st); err != nil {
		return fmt.Errorf("resume %s: %w", name, err)
	}

	p.log.Info("stage skipped: artifact on disk", "stage", name)

	switch name {
	case stageEnrichment:
		return p.refillLoadedAbstracts(ctx, rp, st, artifact)
	case stageCritique:
		return p.deepenLoadedCritique(ctx, rp, st, artifact)
	}

	return nil
}

// runStage runs one stage and saves its checkpoint.
func (p *Pipeline) runStage(ctx context.Context, rp runParams, st *State, s stage, artifact string) error {
	if s.name == stageSynthesis {
		p.loadVisionNotes(rp.dir, st) // optional agent-written chart critique
	}

	if err := s.run(ctx, rp, st); err != nil {
		return fmt.Errorf("stage %s: %w", s.name, err)
	}
	// A canceled context makes the sources fail one by one without failing
	// the stage; the half-filled state must not be saved as a finished
	// stage, or --resume would skip it.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stage %s interrupted: %w", s.name, err)
	}

	if err := saveJSON(artifact, st); err != nil {
		return fmt.Errorf("saving %s: %w", s.name, err)
	}

	p.log.Info("stage done", "stage", s.name, "papers", len(st.Papers))

	return nil
}

// deepenLoadedCritique gives a critique artifact loaded by --resume the hybrid
// deepening it lacks: one written by older code (which deepened after the
// synthesis) or by a run stopped right after the abstracts critique. When the
// synthesis artifact is already on disk the run is left as it is — a resume
// never repeats the pass. The artifact is saved again after the deepening.
func (p *Pipeline) deepenLoadedCritique(ctx context.Context, rp runParams, st *State, artifact string) error {
	if rp.opts.TextMode != TextHybrid || st.HybridDone || fileExists(filepath.Join(rp.dir, stageSynthesis+".json")) {
		return nil
	}

	if err := p.hybridPass(ctx, rp, st); err != nil {
		return fmt.Errorf("stage %s: %w", stageCritique, err)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stage %s interrupted: %w", stageCritique, err)
	}

	if err := saveJSON(artifact, st); err != nil {
		return fmt.Errorf("saving %s: %w", stageCritique, err)
	}

	return nil
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
