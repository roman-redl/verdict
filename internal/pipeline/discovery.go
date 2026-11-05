package pipeline

// Stage 1: discovery. Three ways to a corpus: a user-supplied DOI list
// (mode C), semantic search through Consensus (mode B), keyword search through
// OpenAlex (mode A).

import (
	"cmp"
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/internal/consensus"
	"github.com/roman-redl/verdict/internal/model"
)

// defaultDOIListQuestion titles a run that has a DOI list but no question.
const defaultDOIListQuestion = "Evidence assessment over a given DOI list"

// maxConsensusPages is the last legal page: (page+1)*page_size <= 1000.
const maxConsensusPages = 50

func (p *Pipeline) discover(ctx context.Context, rp runParams, st *State) error {
	opts := rp.opts

	if opts.DOIFile != "" {
		st.Question = cmp.Or(opts.Question, defaultDOIListQuestion)
		st.Discovery = &DiscoveryInfo{Engine: EngineDOIs}

		return p.discoverByDOIs(ctx, opts, st)
	}

	if opts.Question == "" {
		return &UserError{Msg: "a question or a DOI list is required"}
	}

	st.Question = opts.Question

	engine := opts.Engine
	if engine == "" {
		engine = EngineOpenAlex
		if p.clients.Consensus != nil && p.clients.Consensus.HasKey() {
			engine = EngineConsensus
		}
	}

	queries := opts.Queries
	if len(queries) == 0 {
		queries = []string{opts.Question}
	}

	switch engine {
	case EngineConsensus:
		return p.discoverConsensus(ctx, opts, st, queries)
	case EngineOpenAlex:
		return p.discoverOpenAlex(ctx, opts, st, queries)
	case EngineDOIs:
		// a DOI list is selected with Options.DOIFile, never by engine name
	}

	// unreachable for engines that went through ParseEngine
	p.log.Error("unknown discovery engine", "engine", engine)

	return &UserError{Msg: fmt.Sprintf("unknown engine %q (want consensus | openalex)", engine)}
}

// discoverOpenAlex is mode A: keyword search.
func (p *Pipeline) discoverOpenAlex(ctx context.Context, opts Options, st *State, queries []string) error {
	// The auto path lands here only when no key is configured; an explicit
	// --engine openalex is a deliberate keyword opt-out.
	if opts.Engine == "" {
		p.log.Warn("CONSENSUS_API_KEY not set: keyword search via OpenAlex (mode A); for " +
			"semantic discovery set CONSENSUS_API_KEY (mode B) or use the free web bridge: " +
			"`verdict web` + `run --dois` (mode C)")
	}

	papers, err := p.clients.OpenAlex.Search(ctx, opts.Question, orDefault(opts.MaxPapers, p.set.MaxPapers))
	if err != nil {
		return err
	}

	st.Discovery = &DiscoveryInfo{Engine: EngineOpenAlex, Queries: queries}
	st.Papers = papers

	return nil
}

// discoverByDOIs is the manual-corpus mode: metadata comes from OpenAlex;
// papers unknown to OpenAlex stay as DOI stubs (Source: manual).
func (p *Pipeline) discoverByDOIs(ctx context.Context, opts Options, st *State) error {
	dois, err := readDOIFile(opts.DOIFile)
	if err != nil {
		return err
	}

	if len(dois) == 0 {
		return &UserError{Msg: "no DOIs found in the input list"}
	}

	dois = p.capDOIList(st, dois, opts)

	papers := make([]model.Paper, len(dois))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(orDefault(opts.Concurrency, p.set.Concurrency))

	for i, doi := range dois {
		g.Go(func() error {
			papers[i] = p.lookupDOI(gctx, doi)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("looking up the DOIs: %w", err)
	}

	st.Papers = papers

	return nil
}

// lookupDOI resolves one DOI of a user-supplied list; an unknown or failing
// lookup leaves a stub that keeps the DOI in the corpus.
func (p *Pipeline) lookupDOI(ctx context.Context, doi string) model.Paper {
	w, err := p.clients.OpenAlex.ByDOI(ctx, doi)
	if err != nil {
		p.log.Warn("openalex lookup failed", "doi", doi, "err", err)
		return model.Paper{DOI: doi, Source: model.SourceManual}
	}

	if w == nil {
		return model.Paper{DOI: doi, Source: model.SourceManual}
	}

	return w.ToPaper(doi)
}

// capDOIList applies the corpus size policy to a user-curated list. An
// explicit --max-papers truncates; without it the whole list runs, but a
// corpus above the default is flagged with its real LLM cost (one critique
// call per paper plus one synthesis call).
func (p *Pipeline) capDOIList(st *State, dois []string, opts Options) []string {
	if opts.MaxPapers > 0 && len(dois) > opts.MaxPapers {
		p.log.Warn("truncating the DOI list to --max-papers", "have", len(dois), "keep", opts.MaxPapers)
		return dois[:opts.MaxPapers]
	}

	if lim := orDefault(opts.MaxPapers, p.set.MaxPapers); len(dois) > lim {
		st.Warnings = append(st.Warnings, fmt.Sprintf(
			"%d DOIs given (above the %d default): expect ~%d critique LLM calls + 1 synthesis; "+
				"trim the list or pass --max-papers to cap it",
			len(dois), lim, len(dois)))
	}

	return dois
}

// discoverConsensus is mode B: semantic search through the Consensus API.
// Every request plausibly spends one of the plan's monthly uses, so the walk
// is page-capped and the count is recorded in Discovery (and logged). Corpus
// sizing follows the mode C convention: above the default it warns and keeps
// everything — recall comes from query reformulations — and only an explicit
// --max-papers truncates. An API error fails the stage loudly: silently
// replacing semantic search with keyword search is what the engine bake-off
// forbids (docs/how-it-works.md §2.2).
func (p *Pipeline) discoverConsensus(ctx context.Context, opts Options, st *State, queries []string) error {
	maxPages := min(orDefault(opts.ConsensusPages, p.set.ConsensusPages), maxConsensusPages)
	lim := orDefault(opts.MaxPapers, p.set.MaxPapers)
	w := newConsensusWalk(queries)

	if err := p.walkConsensus(ctx, w, maxPages, lim); err != nil {
		return err
	}

	papers := w.fairTrim(lim)

	if w.info.DroppedNoDOI > 0 {
		st.Warnings = append(st.Warnings, fmt.Sprintf(
			"consensus: %d result(s) without a DOI dropped — the pipeline needs DOIs for enrichment",
			w.info.DroppedNoDOI))
	}

	if len(papers) == 0 {
		// A null answer is data, not failure: the verdict path handles an
		// empty corpus honestly.
		st.Warnings = append(st.Warnings,
			"consensus returned no papers for the query(ies) — an honest null answer; reformulate "+
				"(bin/verdict pico) or fall back to mode C (`verdict web` + --dois)")
	}

	w.info.PapersFound = w.pooled

	if w.pooled > lim {
		p.log.Warn("truncating the consensus corpus to the paper cap (fair share per query)",
			"have", w.pooled, "keep", lim,
			"hint", "raise --max-papers (or MAX_PAPERS) for a fatter, costlier corpus")
	}

	st.Papers = papers
	st.Discovery = w.info
	p.log.Info("consensus discovery done", "queries", len(queries), "pages", w.info.PagesFetched,
		"api_uses", w.info.PagesFetched, "papers", len(papers),
		"dropped_no_doi", w.info.DroppedNoDOI, "truncated", w.info.Truncated)

	return nil
}

// consensusWalk is the state of a page walk over several semantic queries:
// papers pooled per query, deduplicated by DOI across queries. Recall comes
// from the reformulations, so page 0 of EVERY query is fetched before page 1
// of any — no query may be crowded out of the corpus by another filling the
// cap first — and the cap then trims the pool fairly, not by query order.
type consensusWalk struct {
	queries  []string
	perQuery [][]model.Paper
	seen     map[string]bool
	pooled   int
	info     *DiscoveryInfo
}

func newConsensusWalk(queries []string) *consensusWalk {
	return &consensusWalk{
		queries:  queries,
		perQuery: make([][]model.Paper, len(queries)),
		seen:     map[string]bool{},
		info:     &DiscoveryInfo{Engine: EngineConsensus, Queries: queries},
	}
}

// add pools one page of results for query qi.
func (w *consensusWalk) add(qi int, results []consensus.QueryResult) {
	for i := range results {
		r := results[i]

		doi := model.NormalizeDOI(r.DOI)
		if doi == "" {
			w.info.DroppedNoDOI++ // the pipeline keys papers by DOI
			continue
		}

		if w.seen[doi] {
			continue
		}

		w.seen[doi] = true

		w.perQuery[qi] = append(w.perQuery[qi], r.ToPaper())
		w.pooled++
	}
}

// fairTrim takes one paper per query per round until the cap, so every query
// keeps representation in the final corpus.
func (w *consensusWalk) fairTrim(lim int) []model.Paper {
	var papers []model.Paper

	for round := 0; len(papers) < lim; round++ {
		progressed := false

		for _, list := range w.perQuery {
			if round < len(list) && len(papers) < lim {
				papers = append(papers, list[round])
				progressed = true
			}
		}

		if !progressed {
			break
		}
	}

	return papers
}

// walkConsensus fetches result pages round-robin over the queries until the
// page cap, the end of every ranking, or (for pages after the first) a pool
// that can already fill the corpus cap.
func (p *Pipeline) walkConsensus(ctx context.Context, w *consensusWalk, maxPages, lim int) error {
	active := make([]int, 0, len(w.queries))
	for i := range w.queries {
		active = append(active, i)
	}

	for page := 0; page < maxPages && len(active) > 0; page++ {
		var next []int

		for _, qi := range active {
			// Every query gets its page 0 unconditionally; deeper pages
			// stop once the pool can fill the corpus cap.
			if page > 0 && w.pooled >= lim {
				break
			}

			pg, err := p.clients.Consensus.SearchPage(ctx, consensus.SearchParams{Query: w.queries[qi], Page: page})
			if err != nil {
				return fmt.Errorf("consensus search (query %q, page %d): %w — no automatic fallback "+
					"to keyword search; escape hatch: `verdict web` + `run --dois` (mode C, free)", w.queries[qi], page, err)
			}

			w.info.PagesFetched++

			w.add(qi, pg.Results)
			p.log.Info("consensus page done", "query", w.queries[qi], "page", page, "pool", w.pooled)
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
	if len(active) > 0 && w.pooled < lim {
		w.info.Truncated = true
	}

	return nil
}
