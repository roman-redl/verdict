package pipeline

// Stage 1: discovery. Three ways to a corpus: a user-supplied DOI list
// (mode C), the pooled channels of mode B — semantic search through Consensus
// (discovery_consensus.go), PubMed (pubmed_discovery.go) and the references of
// reviews — and keyword search through OpenAlex (mode A).

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/internal/model"
)

// errNoWorks is returned when a stage needs the metadata source and none is
// wired.
var errNoWorks = errors.New("no metadata source (OpenAlex) is configured")

// defaultDOIListQuestion titles a run that has a DOI list but no question.
const defaultDOIListQuestion = "Evidence assessment over a given DOI list"

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
		// a run that asks for channels other than Consensus is mode B as well,
		// key or not
		hasKey := p.clients.Search != nil && p.clients.Search.HasKey()
		if hasKey || !p.channelPlan(opts).sources.Has(model.SourceConsensus) {
			engine = EngineConsensus
		}
	}

	queries := opts.Queries
	if len(queries) == 0 {
		queries = []string{opts.Question}
	}

	switch engine {
	case EngineConsensus:
		return p.discoverPooled(ctx, opts, st, queries)
	case EngineOpenAlex:
		return p.discoverOpenAlex(ctx, opts, st, queries)
	case EngineDOIs:
		// a DOI list is selected with Options.DOIFile, never by engine name
	}

	// unreachable for engines that went through ParseEngine
	p.log.Error("unknown discovery engine", "engine", engine)

	return &UserError{Msg: fmt.Sprintf("unknown engine %q (want consensus | openalex)", engine)}
}

// discoverOpenAlex is mode A: keyword search. It takes the top of the keyword
// ranking as is: the screening and the review-reference mining of mode B need
// a design tag and a semantic ranking that keyword search does not have.
func (p *Pipeline) discoverOpenAlex(ctx context.Context, opts Options, st *State, queries []string) error {
	// The auto path lands here only when no key is configured; an explicit
	// --engine openalex is a deliberate keyword opt-out.
	if opts.Engine == "" {
		p.log.Warn("CONSENSUS_API_KEY not set: keyword search via OpenAlex (mode A); for " +
			"semantic discovery set CONSENSUS_API_KEY (mode B) or use the free web bridge: " +
			"`verdict web` + `run --dois` (mode C)")
	}

	if p.clients.Works == nil {
		return errNoWorks
	}

	papers, err := p.clients.Works.Search(ctx, opts.Question, orDefault(opts.MaxPapers, p.set.MaxPapers))
	if err != nil {
		return fmt.Errorf("searching OpenAlex: %w", err)
	}

	excl := p.newExclusion(ctx, opts.ExcludeDOIs)
	papers = slices.DeleteFunc(papers, func(pp model.Paper) bool { return excl.drop(pp.DOI, pp.Title) })

	st.Discovery = &DiscoveryInfo{Engine: EngineOpenAlex, Queries: queries}
	p.recordExclusion(st.Discovery, excl)
	st.Papers = papers

	return nil
}

// discoverByDOIs is the manual-corpus mode: metadata comes from OpenAlex;
// papers unknown to OpenAlex stay as DOI stubs (Source: manual).
func (p *Pipeline) discoverByDOIs(ctx context.Context, opts Options, st *State) error {
	if p.clients.Works == nil {
		return errNoWorks
	}

	dois, err := readDOIFile(opts.DOIFile)
	if err != nil {
		return err
	}

	if len(dois) == 0 {
		return &UserError{Msg: "no DOIs found in the input list"}
	}

	// the list is the user's own corpus: its DOIs have no titles to match, so
	// the exclusion is by DOI alone and needs no lookup
	excl := newDOIExclusion(opts.ExcludeDOIs)

	if dois = p.dropExcludedDOIs(dois, excl); len(dois) == 0 {
		return &UserError{Msg: "every DOI of the input list is excluded by --exclude-dois"}
	}

	p.recordExclusion(st.Discovery, excl)

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
	w, err := p.clients.Works.ByDOI(ctx, doi)
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
// call per paper plus one synthesis call). The user's list is respected as
// given: no screening, no mining.
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
