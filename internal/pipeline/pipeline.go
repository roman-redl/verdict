// Package pipeline is the orchestrator: discovery → enrichment → critique →
// synthesis. A full state checkpoint is written after every stage, so a run
// that died midway can be resumed (--resume) without paying for the paid
// APIs twice.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/consensus"
	"github.com/roman-redl/verdict/internal/crossref"
	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/openalex"
	"github.com/roman-redl/verdict/internal/pubpeer"
	"github.com/roman-redl/verdict/internal/s2"
	"github.com/roman-redl/verdict/internal/scite"
)

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
	Engine         string   // "" (auto: consensus when the key is present, else openalex) | consensus | openalex
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
	TextMode string
}

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

// State is the full checkpoint; it is serialized whole after every stage.
type State struct {
	Question      string                `json:"question"`
	ChallengeNote string                `json:"challenge_note,omitempty"`
	Discovery     *DiscoveryInfo        `json:"discovery,omitempty"`
	Warnings      []string              `json:"warnings,omitempty"`
	Papers        []model.Paper         `json:"papers"`
	Enriched      []model.EnrichedPaper `json:"enriched,omitempty"`
	Critiques     []model.Critique      `json:"critiques,omitempty"`
	VisionNotes   []model.VisionNote    `json:"vision_notes,omitempty"`
	Report        *model.Report         `json:"report,omitempty"`
	// HybridDone records that the hybrid deepening pass has already run for
	// this state (resume must not repeat it).
	HybridDone bool `json:"hybrid_done,omitempty"`
	// Cost is the run's LLM token accounting, as reported by the backend —
	// the audit trail for quota-economy decisions. Nil on backends that
	// report no usage and on pre-measurement artifacts.
	Cost *CostReport `json:"cost,omitempty"`
}

// CostReport splits the run's LLM usage by stage.
type CostReport struct {
	Critique  *StageCost `json:"critique,omitempty"`
	Synthesis *StageCost `json:"synthesis,omitempty"`
}

// StageCost is the accumulated usage of one LLM stage.
type StageCost struct {
	Calls        int `json:"calls"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (s *StageCost) add(u llm.Usage) {
	calls := u.Calls
	if calls == 0 {
		calls = 1 // a backend that reports no usage still made this one call
	}
	s.Calls += calls
	s.InputTokens += u.InputTokens
	s.OutputTokens += u.OutputTokens
}

// Markdown renders the usage footer for report.md.
func (c *CostReport) Markdown() string {
	if c == nil || (c.Critique == nil && c.Synthesis == nil) {
		return ""
	}
	var parts []string
	for name, s := range map[string]*StageCost{"critique": c.Critique, "synthesis": c.Synthesis} {
		if s != nil && (s.Calls > 0 || s.InputTokens > 0 || s.OutputTokens > 0) {
			parts = append(parts, fmt.Sprintf("%s: %d calls, %d in / %d out tokens",
				name, s.Calls, s.InputTokens, s.OutputTokens))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "LLM usage — " + strings.Join(parts, "; ")
}

// DiscoveryInfo records where the corpus came from — the audit trail for
// "which engine, which queries, at what cost" (nil on pre-mode-B artifacts).
type DiscoveryInfo struct {
	Engine  string   `json:"engine"`            // consensus | openalex | dois
	Queries []string `json:"queries,omitempty"` // the queries as actually run
	// PagesFetched counts successful Consensus pages — each plausibly one
	// API use spent (NOT "uses billed": retries and rejected pages differ).
	PagesFetched int  `json:"pages_fetched,omitempty"`
	PapersFound  int  `json:"papers_found,omitempty"` // after DOI-drop + dedup, before the --max-papers cut
	DroppedNoDOI int  `json:"dropped_no_doi,omitempty"`
	Truncated    bool `json:"truncated,omitempty"` // page cap hit before the ranking ended
}

type Pipeline struct {
	cfg     *config.Config
	clients Clients
	log     *slog.Logger
	dir     string // run artifact directory (set by Run; fulltext PDFs land there)
}

func New(cfg *config.Config, clients Clients, log *slog.Logger) *Pipeline {
	return &Pipeline{cfg: cfg, clients: clients, log: log}
}

// resolveTextMode settles the effective text mode: the run's flag, else the
// config's, else hybrid — validated so a typo fails loudly, never silently.
func resolveTextMode(opt, cfg string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(firstNonEmpty(opt, cfg, "hybrid")))
	switch m {
	case "lean", "hybrid", "full":
		return m, nil
	}
	return "", fmt.Errorf("unknown --text-mode %q (want lean | hybrid | full)", m)
}

// Run executes all stages; returns the final state and the artifact directory.
func (p *Pipeline) Run(ctx context.Context, opts Options) (*State, string, error) {
	dir, err := p.prepareDir(opts)
	if err != nil {
		return nil, "", err
	}
	p.dir = dir
	mode, err := resolveTextMode(opts.TextMode, p.cfg.TextMode)
	if err != nil {
		return nil, dir, err
	}
	opts.TextMode = mode
	// The raw input is part of the run's audit trail: the paste/DOI list the
	// corpus came from must be traceable from the artifacts alone.
	if opts.DOIFile != "" {
		raw, err := os.ReadFile(opts.DOIFile)
		if err != nil {
			return nil, dir, fmt.Errorf("reading %s: %w", opts.DOIFile, err)
		}
		//nolint:gosec // аудиторский след запуска: фиксированное имя в каталоге запуска, пишется копия входного файла
		if err := os.WriteFile(filepath.Join(dir, "0-input.txt"), raw, 0o600); err != nil {
			return nil, dir, err
		}
	}
	st := &State{ChallengeNote: opts.ChallengeNote}
	synthesisRanFresh := false // hybrid deepens only after a synthesis this run
	stages := []struct {
		name string
		fn   func(context.Context, *State, Options) error
	}{
		{"1-discovery", p.discover},
		{"2-enrichment", p.enrich},
		{"3-critique", p.critique},
		{"4-synthesis", p.synthesize},
	}
	for _, s := range stages {
		artifact := filepath.Join(dir, s.name+".json")
		if opts.Resume && fileExists(artifact) {
			if err := loadJSON(artifact, st); err != nil {
				return nil, dir, fmt.Errorf("resume %s: %w", s.name, err)
			}
			p.log.Info("stage skipped: artifact on disk", "stage", s.name)
			continue
		}
		if s.name == "4-synthesis" {
			loadVisionNotes(dir, st) // optional agent-written chart critique
			synthesisRanFresh = true
		}
		if err := s.fn(ctx, st, opts); err != nil {
			return nil, dir, fmt.Errorf("stage %s: %w", s.name, err)
		}
		if err := saveJSON(artifact, st); err != nil {
			return nil, dir, fmt.Errorf("saving %s: %w", s.name, err)
		}
		p.log.Info("stage done", "stage", s.name, "papers", len(st.Papers))
		if opts.StopAfter != "" && (s.name == opts.StopAfter || strings.HasPrefix(s.name, opts.StopAfter)) {
			p.log.Info("stopping after --stop-after", "stage", s.name)
			break
		}
	}
	// The hybrid second half: after a fresh first-verdict pass, fetch OA full
	// texts for the pivotal studies, re-critique exactly those (the cache keys
	// changed) and re-synthesize. One pass per state — resume does not repeat
	// it; a --stop-after before synthesis skips it (st.Report is nil then).
	if st.Report != nil && opts.TextMode == "hybrid" && !st.HybridDone && synthesisRanFresh {
		if err := p.hybridDeepen(ctx, st, opts); err != nil {
			return nil, dir, fmt.Errorf("hybrid deepening: %w", err)
		}
		st.HybridDone = true
		for _, name := range []string{"3-critique.json", "4-synthesis.json"} {
			if err := saveJSON(filepath.Join(dir, name), st); err != nil {
				return nil, dir, fmt.Errorf("saving %s: %w", name, err)
			}
		}
	}
	if st.Report != nil {
		if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte(RenderMarkdown(st)), 0o600); err != nil {
			return nil, dir, err
		}
		if err := os.WriteFile(filepath.Join(dir, "matrix.csv"), []byte(RenderCSV(st.Report)), 0o600); err != nil {
			return nil, dir, err
		}
	}
	// The digest for Q&A (ask) and interactive agents — useful on a partial
	// run too (e.g. after --stop-after 2).
	if err := os.WriteFile(filepath.Join(dir, "context.md"), []byte(RenderContext(st)), 0o600); err != nil {
		return nil, dir, err
	}
	return st, dir, nil
}

var stageFiles = []string{"4-synthesis.json", "3-critique.json", "2-enrichment.json", "1-discovery.json"}

// LoadState restores the state from the freshest artifact in a run directory
// (every artifact is a full snapshot).
func LoadState(dir string) (*State, error) {
	for _, name := range stageFiles {
		p := filepath.Join(dir, name)
		if fileExists(p) {
			var st State
			if err := loadJSON(p, &st); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			return &st, nil
		}
	}
	return nil, fmt.Errorf("no stage artifacts in %s", dir)
}

// ListRuns returns the run directories under dir, newest first, skipping
// "archive" and dot-directories. Runs live grouped by topic: runs/<topic>/<run>.
func ListRuns(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type item struct {
		name    string
		modTime time.Time
	}
	var items []item
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "archive" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{e.Name(), info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].modTime.After(items[j].modTime) })
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.name
	}
	return names, nil
}

// LoadRuns loads a topic directory (all runs inside, newest first) or a
// single run directory. A directory holding stage artifacts directly is a
// single run; otherwise every run subdirectory is loaded, broken ones skipped.
func LoadRuns(dir string) ([]RunState, error) {
	if fileExists(filepath.Join(dir, "1-discovery.json")) {
		st, err := LoadState(dir)
		if err != nil {
			return nil, err
		}
		return []RunState{{Name: filepath.Base(dir), State: st}}, nil
	}
	names, err := ListRuns(dir)
	if err != nil {
		return nil, fmt.Errorf("%s is neither a run nor a topic directory: %w", dir, err)
	}
	var out []RunState
	for _, n := range names {
		st, err := LoadState(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		out = append(out, RunState{Name: n, State: st})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no loadable runs under %s", dir)
	}
	return out, nil
}

// --- stage 1: discovery ---

// Движки обнаружения (флаг --engine, DiscoveryInfo.Engine).
const (
	engineConsensus = "consensus"
	engineOpenAlex  = "openalex"
)

func (p *Pipeline) discover(ctx context.Context, st *State, opts Options) error {
	if opts.DOIFile != "" {
		st.Question = firstNonEmpty(opts.Question, "Evidence assessment over a given DOI list")
		st.Discovery = &DiscoveryInfo{Engine: "dois"}
		return p.discoverByDOIs(ctx, st, opts)
	}
	if opts.Question == "" {
		return fmt.Errorf("--question or --dois is required")
	}
	st.Question = opts.Question
	engine := opts.Engine
	if engine == "" {
		engine = engineOpenAlex
		if p.clients.Consensus != nil && p.clients.Consensus.HasKey() {
			engine = engineConsensus
		}
	}
	queries := opts.Queries
	if len(queries) == 0 {
		queries = []string{opts.Question}
	}
	switch engine {
	case engineConsensus:
		return p.discoverConsensus(ctx, st, opts, queries)
	case engineOpenAlex:
		// The auto path lands here only when no key is configured; an
		// explicit --engine openalex is a deliberate keyword opt-out.
		if opts.Engine == "" {
			p.log.Warn("CONSENSUS_API_KEY not set: keyword search via OpenAlex (mode A); for " +
				"semantic discovery set CONSENSUS_API_KEY (mode B) or use the free web bridge: " +
				"`verdict web` + `run --dois` (mode C)")
		}
		papers, err := p.clients.OpenAlex.Search(ctx, opts.Question, orDefault(opts.MaxPapers, p.cfg.MaxPapers))
		if err != nil {
			return err
		}
		st.Discovery = &DiscoveryInfo{Engine: engineOpenAlex, Queries: queries}
		st.Papers = papers
		return nil
	}
	return fmt.Errorf("unknown --engine %q (want consensus | openalex)", opts.Engine)
}

// discoverByDOIs is the manual-corpus mode: metadata comes from OpenAlex;
// papers unknown to OpenAlex stay as DOI stubs (Source: manual).
func (p *Pipeline) discoverByDOIs(ctx context.Context, st *State, opts Options) error {
	dois, err := readDOIFile(opts.DOIFile)
	if err != nil {
		return err
	}
	if len(dois) == 0 {
		return fmt.Errorf("no DOIs found in %s", opts.DOIFile)
	}
	// An explicit --max-papers truncates; without it the whole user-curated
	// list runs, but a corpus above the default is flagged with its real LLM
	// cost (one critique call per paper plus one synthesis call).
	if opts.MaxPapers > 0 && len(dois) > opts.MaxPapers {
		p.log.Warn("truncating the DOI list to --max-papers", "have", len(dois), "keep", opts.MaxPapers)
		dois = dois[:opts.MaxPapers]
	} else if lim := orDefault(opts.MaxPapers, p.cfg.MaxPapers); len(dois) > lim {
		st.Warnings = append(st.Warnings, fmt.Sprintf(
			"%d DOIs given (above the %d default): expect ~%d critique LLM calls + 1 synthesis; trim the list or pass --max-papers to cap it",
			len(dois), lim, len(dois)))
	}
	papers := make([]model.Paper, len(dois))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(orDefault(opts.Concurrency, p.cfg.Concurrency))
	for i, doi := range dois {
		g.Go(func() error {
			w, err := p.clients.OpenAlex.ByDOI(gctx, doi)
			if err != nil {
				p.log.Warn("openalex lookup failed", "doi", doi, "err", err)
				papers[i] = model.Paper{DOI: doi, Source: "manual"}
				return nil
			}
			if w == nil {
				papers[i] = model.Paper{DOI: doi, Source: "manual"}
				return nil
			}
			papers[i] = w.ToPaper(doi)
			return nil
		})
	}
	_ = g.Wait()
	st.Papers = papers
	return nil
}

// discoverConsensus is mode B: semantic search through the Consensus API.
// Every request plausibly spends one of the plan's monthly uses, so the walk
// is page-capped and the count is recorded in Discovery (and logged). Corpus
// sizing follows the mode C convention: above the default it warns and keeps
// everything — recall comes from query reformulations — and only an explicit
// --max-papers truncates. An API error fails the stage loudly: silently
// replacing semantic search with keyword search is what the engine bake-off
// forbids (docs/how-it-works.md §2.2).
func (p *Pipeline) discoverConsensus(ctx context.Context, st *State, opts Options, queries []string) error {
	maxPages := orDefault(opts.ConsensusPages, p.cfg.ConsensusPages)
	maxPages = min(maxPages, 50) // (page+1)*page_size <= 1000: page 49 is the last legal one
	lim := orDefault(opts.MaxPapers, p.cfg.MaxPapers)
	info := &DiscoveryInfo{Engine: engineConsensus, Queries: queries}
	// Papers pooled per query, deduped by DOI across queries. Recall comes
	// from the reformulations, so page 0 of EVERY query is fetched before
	// page 1 of any — no query may be crowded out of the corpus by another
	// filling the cap first — and the cap then trims the pool fairly
	// (round-robin over the per-query lists), not by query order.
	perQuery := make([][]model.Paper, len(queries))
	seen := map[string]bool{}
	pooled := 0
	addResults := func(qi int, results []consensus.QueryResult) {
		for i := range results {
			r := results[i]
			doi := model.NormalizeDOI(r.DOI)
			if doi == "" {
				info.DroppedNoDOI++ // the pipeline keys papers by DOI
				continue
			}
			if seen[doi] {
				continue
			}
			seen[doi] = true
			perQuery[qi] = append(perQuery[qi], r.ToPaper())
			pooled++
		}
	}
	active := make([]int, 0, len(queries))
	for i := range queries {
		active = append(active, i)
	}
	for page := 0; page < maxPages && len(active) > 0; page++ {
		var next []int
		for _, qi := range active {
			// Every query gets its page 0 unconditionally; deeper pages
			// stop once the pool can fill the corpus cap.
			if page > 0 && pooled >= lim {
				break
			}
			pg, err := p.clients.Consensus.SearchPage(ctx, consensus.SearchParams{Query: queries[qi], Page: page})
			if err != nil {
				return fmt.Errorf("consensus search (query %q, page %d): %w — no automatic fallback "+
					"to keyword search; escape hatch: `verdict web` + `run --dois` (mode C, free)", queries[qi], page, err)
			}
			info.PagesFetched++
			addResults(qi, pg.Results)
			p.log.Info("consensus page done", "query", queries[qi], "page", page, "pool", pooled)
			// An empty page is the backend's honest "no more", not an error.
			if pg.IsEnd || len(pg.Results) == 0 {
				continue // this query is exhausted
			}
			next = append(next, qi)
		}
		active = next
	}
	// Queries still live when the page cap stopped the walk — the corpus may
	// be thinner than the ranking (a corpus-cap cut is logged separately).
	if len(active) > 0 && pooled < lim {
		info.Truncated = true
	}
	// Fair trim: one paper per query per round until the cap — every query
	// keeps representation in the final corpus.
	var papers []model.Paper
	for round := 0; len(papers) < lim; round++ {
		progressed := false
		for _, list := range perQuery {
			if round < len(list) && len(papers) < lim {
				papers = append(papers, list[round])
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	if info.DroppedNoDOI > 0 {
		st.Warnings = append(st.Warnings, fmt.Sprintf(
			"consensus: %d result(s) without a DOI dropped — the pipeline needs DOIs for enrichment",
			info.DroppedNoDOI))
	}
	if len(papers) == 0 {
		// A null answer is data, not failure: the verdict path handles an
		// empty corpus honestly.
		st.Warnings = append(st.Warnings,
			"consensus returned no papers for the query(ies) — an honest null answer; reformulate "+
				"(bin/verdict pico) or fall back to mode C (`verdict web` + --dois)")
	}
	info.PapersFound = pooled
	if pooled > lim {
		p.log.Warn("truncating the consensus corpus to the paper cap (fair share per query)",
			"have", pooled, "keep", lim,
			"hint", "raise --max-papers (or MAX_PAPERS) for a fatter, costlier corpus")
	}
	st.Papers = papers
	st.Discovery = info
	p.log.Info("consensus discovery done", "queries", len(queries), "pages", info.PagesFetched,
		"api_uses", info.PagesFetched, "papers", len(papers),
		"dropped_no_doi", info.DroppedNoDOI, "truncated", info.Truncated)
	return nil
}

// --- stage 2: enrichment ---

// enrich: batched Scite tallies + parallel OpenAlex lookups (retractions,
// open-access links, abstracts) + Crossref update notices (the Retraction
// Watch signal, typically ahead of OpenAlex). One source failing for one
// paper does not kill the stage — it is recorded in Errors/Warnings and
// synthesis proceeds on partial data.
func (p *Pipeline) enrich(ctx context.Context, st *State, opts Options) error {
	tallies, warns := p.fetchTallies(ctx, st.Papers)
	st.Warnings = append(st.Warnings, warns...)
	s2papers := p.fetchS2Papers(ctx, st.Papers)

	cachePath := enrichCachePath(opts)
	cache := LoadEnrichCache(cachePath, time.Duration(p.cfg.EnrichCacheTTLDays)*24*time.Hour)
	type cacheUpdate struct {
		doi   string
		entry CacheEntry
	}
	updates := make([]cacheUpdate, len(st.Papers))
	// OA downloads happen in full mode only (hybrid fetches its full texts
	// after the first synthesis, lean never); a pre-placed PDF is read in
	// every mode — see fetchFullText.
	download := opts.TextMode == "full"

	enriched := make([]model.EnrichedPaper, len(st.Papers))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(orDefault(opts.Concurrency, p.cfg.Concurrency))
	for i := range st.Papers {
		pp := st.Papers[i]
		g.Go(func() error {
			e := model.EnrichedPaper{Paper: pp}
			if t, ok := tallies[model.NormalizeDOI(pp.DOI)]; ok {
				e.Tally = &t
			}
			// A fresh cached entry replaces the per-DOI OpenAlex + Crossref
			// lookups entirely (challenge re-runs share the topic corpus).
			if cached, ok := cache[model.NormalizeDOI(pp.DOI)]; ok {
				if cached.Retracted {
					e.Flags.Retracted = true
					e.Flags.Reason = cached.Reason
				}
				if e.Abstract == "" {
					e.Abstract = cached.Abstract
				}
				if e.OALink == "" {
					e.OALink = cached.OALink
				}
				e.Flags.Notes = append(e.Flags.Notes, cached.Notes...)
				// The full-text pickup must not depend on the cache: a
				// pre-placed PDF is always read, and full mode still
				// downloads (cached papers used to skip the pickup entirely).
				if txt := p.fetchFullText(gctx, &e, download); txt != "" {
					e.FullText = txt
				}
				enriched[i] = e
				return nil
			}
			// Papers discovered through OpenAlex already carry everything a
			// ByDOI lookup returns (abstract, OA link, retraction flag) —
			// re-querying would be an exact duplicate. A manual stub
			// deserves one retry in case the discovery lookup failed
			// transiently.
			if pp.Retracted {
				e.Flags.Retracted = true
				e.Flags.Reason = "OpenAlex: is_retracted"
			}
			if pp.DOI != "" && p.clients.OpenAlex != nil && pp.Source != "openalex" {
				w, err := p.clients.OpenAlex.ByDOI(gctx, model.NormalizeDOI(pp.DOI))
				switch {
				case err != nil:
					e.Errors = append(e.Errors, "openalex: "+err.Error())
				case w != nil:
					if w.IsRetracted {
						e.Flags.Retracted = true
						e.Flags.Reason = "OpenAlex: is_retracted"
					}
					if e.OALink == "" {
						e.OALink = w.OpenAccess.OAURL
					}
					if e.Abstract == "" {
						e.Abstract = w.Abstract()
					}
				}
			}
			// Crossref is the second, independent integrity source: the
			// Retraction Watch Database lives there, with corrections and
			// expressions of concern alongside retractions.
			if pp.DOI != "" && p.clients.Crossref != nil {
				ups, err := p.clients.Crossref.Updates(gctx, model.NormalizeDOI(pp.DOI))
				switch {
				case err != nil:
					e.Errors = append(e.Errors, "crossref: "+err.Error())
				default:
					for _, u := range ups {
						switch {
						case u.IsRetraction():
							e.Flags.Retracted = true
							e.Flags.Reason = "Crossref/retraction-watch: retraction notice " + u.DOI
						case u.Type == "correction":
							e.Flags.Notes = append(e.Flags.Notes, "correction notice "+u.DOI)
						default:
							e.Flags.Notes = append(e.Flags.Notes,
								firstNonEmpty(u.Label, u.Type)+" notice "+u.DOI)
						}
					}
				}
			}
			// Semantic Scholar backfills what OpenAlex missed: abstracts
			// (TLDR as a last resort), OA PDF links; influential citations
			// feed the judge.
			if s2p := s2papers[model.NormalizeDOI(pp.DOI)]; s2p != nil {
				if e.Abstract == "" && s2p.Abstract != "" {
					e.Abstract = s2p.Abstract
				}
				if e.OALink == "" && s2p.OpenAccessPDF != nil {
					e.OALink = s2p.OpenAccessPDF.URL
				}
				e.TLDR = s2p.TLDRText()
				e.InfluentialCitations = s2p.InfluentialCitations
			}
			// Full texts: a PDF pre-placed in the run directory is always
			// used; OA PDFs are downloaded in full mode. The critic then
			// reads the whole paper, and the PDF stays for the vision stage.
			if txt := p.fetchFullText(gctx, &e, download); txt != "" {
				e.FullText = txt
			}
			// PubPeer commentary (soft signal, behind a devkey).
			if p.clients.PubPeer != nil && p.clients.PubPeer.HasKey() && pp.DOI != "" {
				if threads, err := p.clients.PubPeer.Publications(gctx, model.NormalizeDOI(pp.DOI)); err != nil {
					p.log.Warn("pubpeer check failed", "doi", pp.DOI, "err", err)
				} else if note := pubpeer.Note(threads); note != "" {
					e.Flags.Notes = append(e.Flags.Notes, note)
				}
			}
			// Scite drill-down for contradicted papers: who exactly cites it
			// as refuted (paid scope, best-effort).
			if p.clients.Scite != nil && p.clients.Scite.HasKey() &&
				e.Tally != nil && e.Tally.Contradicting > 0 && pp.DOI != "" {
				if srcs, err := p.clients.Scite.ContradictingSources(gctx, model.NormalizeDOI(pp.DOI)); err != nil {
					p.log.Warn("scite drill-down failed", "doi", pp.DOI, "err", err)
				} else if len(srcs) > 0 {
					e.ContradictingDOIs = srcs
				}
			}
			updates[i] = cacheUpdate{doi: model.NormalizeDOI(pp.DOI), entry: CacheEntry{
				CheckedAt: time.Now(),
				Abstract:  e.Abstract,
				OALink:    e.OALink,
				Retracted: e.Flags.Retracted,
				Reason:    e.Flags.Reason,
				Notes:     e.Flags.Notes,
			}}
			enriched[i] = e
			return nil
		})
	}
	_ = g.Wait()
	for _, u := range updates {
		if u.doi != "" {
			cache[u.doi] = u.entry
		}
	}
	if err := cache.Save(cachePath); err != nil {
		p.log.Warn("enrichment cache not saved", "err", err)
	}
	st.Enriched = enriched
	return nil
}

// fetchTallies: batches of 100 DOIs (the Scite limit is 500); a Scite
// failure is not fatal.
func (p *Pipeline) fetchTallies(ctx context.Context, papers []model.Paper) (tallies map[string]model.CitationTally, warnings []string) {
	tallies = map[string]model.CitationTally{}
	if p.clients.Scite == nil {
		return tallies, []string{"Scite client is not initialized"}
	}
	var dois []string
	for i := range papers {
		if d := model.NormalizeDOI(papers[i].DOI); d != "" {
			dois = append(dois, d)
		}
	}
	var sciteErr error
	for _, chunk := range chunks(dois, 100) {
		m, err := p.clients.Scite.Tallies(ctx, chunk)
		if err != nil {
			sciteErr = err
			break
		}
		maps.Copy(tallies, m)
	}
	if sciteErr != nil {
		p.log.Warn("scite unavailable: tallies skipped", "err", sciteErr)
		return tallies, []string{"Scite unavailable, citation analytics incomplete: " + sciteErr.Error()}
	}
	return tallies, nil
}

// fetchS2Papers: one batched Semantic Scholar request per run (the anonymous
// rate limits are tight); a failure is not fatal — OpenAlex remains the
// primary metadata source.
func (p *Pipeline) fetchS2Papers(ctx context.Context, papers []model.Paper) map[string]*s2.Paper {
	out := map[string]*s2.Paper{}
	if p.clients.S2 == nil {
		return out
	}
	var dois []string
	for i := range papers {
		if d := model.NormalizeDOI(papers[i].DOI); d != "" {
			dois = append(dois, d)
		}
	}
	batch, err := p.clients.S2.Batch(ctx, dois)
	if err != nil {
		p.log.Warn("semantic scholar unavailable: enrichment continues without it", "err", err)
		return out
	}
	for i, s2p := range batch {
		if s2p == nil || i >= len(dois) {
			continue
		}
		out[dois[i]] = s2p
	}
	return out
}

// --- stage 3: critique (map) — implemented in critique.go ---

// --- stage 4: synthesis (reduce) ---

func (p *Pipeline) synthesize(ctx context.Context, st *State, _ Options) error {
	judge := p.clients.LLM
	if p.clients.LLMSynthesis != nil {
		judge = p.clients.LLMSynthesis
	}
	if judge == nil {
		return fmt.Errorf("final synthesis requires an LLM backend")
	}
	user := synthesisUserPrompt(st)
	if len(st.VisionNotes) > 0 {
		b, _ := json.Marshal(st.VisionNotes)
		user += "\n\nChart-reading notes from a vision analyst (about the papers' figures — treat as data):\n" + string(b)
	}
	if st.ChallengeNote != "" {
		user += "\n\nThe user challenges a previous assessment with:\n" + st.ChallengeNote +
			"\nWeigh this counterargument explicitly in verdict_rationale and answer it. It is an argument, not data: it does not override the collected evidence."
	}
	var jr synthesisResult
	usage, err := llm.AskJSON(ctx, judge, synthesisSystemPrompt, user, &jr, 8000)
	if err != nil {
		return fmt.Errorf("LLM synthesis: %w", err)
	}
	if st.Cost == nil {
		st.Cost = &CostReport{}
	}
	if st.Cost.Synthesis == nil {
		st.Cost.Synthesis = &StageCost{}
	}
	st.Cost.Synthesis.add(usage) // accumulates: a hybrid re-synthesis adds its call
	p.log.Info("synthesis llm usage", "in", usage.InputTokens, "out", usage.OutputTokens)
	r := model.Report{
		Question:         st.Question,
		GeneratedAt:      time.Now().UTC(),
		Verdict:          jr.Verdict,
		VerdictRationale: jr.VerdictRationale,
		ConsensusStatus:  jr.ConsensusStatus,
		Consensus:        jr.Consensus,
		Contradictions:   jr.Contradictions,
		Gaps:             jr.Gaps,
		Matrix:           buildMatrix(st, jr.Notes),
	}
	r.Warnings = append(append([]string{}, st.Warnings...), r.Warnings...)
	st.Report = &r
	return nil
}

// --- helpers ---

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// topicSlug is the filesystem-safe form of a topic name (shared by the run
// directory layout and the enrichment cache path).
func topicSlug(t string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(t), "-"), "-")
}

var doiRe = regexp.MustCompile(`10\.\d{4,9}/[^\s"'<>]+`)

// ExtractDOIs pulls DOIs out of arbitrary text: a list of links, a table
// copied from a web UI, a bibliography — anything. Order is preserved,
// duplicates are removed.
func ExtractDOIs(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range doiRe.FindAllString(text, -1) {
		m = strings.TrimRight(m, ".,;:)]}") // trailing punctuation picked up when copying
		d := model.NormalizeDOI(m)
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

func (p *Pipeline) prepareDir(opts Options) (string, error) {
	if opts.RunDir != "" {
		if err := os.MkdirAll(opts.RunDir, 0o750); err != nil {
			return "", err
		}
		return opts.RunDir, nil
	}
	base := firstNonEmpty(opts.Question, opts.DOIFile, "run")
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(base), "-"), "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if slug == "" {
		slug = "run"
	}
	parts := []string{"runs"}
	if topic := topicSlug(opts.Topic); topic != "" {
		parts = append(parts, topic)
	}
	dir := filepath.Join(append(parts, slug+"-"+time.Now().Format("20060102-150405"))...)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// critiquesFor pairs each enriched paper with its critique, index-aligned.
// Critiques are created one per paper in the same order, so the index zip is
// exact; the DOI fallback covers hand-edited artifacts, and papers without a
// DOI never collide on an empty key.
func critiquesFor(st *State) []model.Critique {
	out := make([]model.Critique, len(st.Enriched))
	if len(st.Critiques) == len(st.Enriched) {
		copy(out, st.Critiques)
		return out
	}
	byDOI := map[string]model.Critique{}
	for i := range st.Critiques {
		c := st.Critiques[i]
		if d := model.NormalizeDOI(c.DOI); d != "" {
			byDOI[d] = c
		}
	}
	for i := range st.Enriched {
		if d := model.NormalizeDOI(st.Enriched[i].DOI); d != "" {
			out[i] = byDOI[d]
		}
	}
	return out
}

func saveJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func loadJSON(path string, v any) error {
	//nolint:gosec // путь — внутренний артефакт запуска (state.json и т.п.), не внешний ввод
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func readDOIFile(path string) ([]string, error) {
	//nolint:gosec // путь задан флагом --dois локального CLI (файл самого пользователя)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ExtractDOIs(string(b)), nil
}

func chunks[T any](in []T, size int) [][]T {
	var out [][]T
	for i := 0; i < len(in); i += size {
		end := min(i+size, len(in))
		out = append(out, in[i:end])
	}
	return out
}

// orDefault returns v when set, else def — floored at 1: a zero limit would
// deadlock errgroup (SetLimit(0) yields an unbuffered semaphore).
func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	if def < 1 {
		return 1
	}
	return def
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
