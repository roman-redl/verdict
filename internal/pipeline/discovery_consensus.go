package pipeline

// Mode B: discovery through several channels. The stage builds a candidate
// pool — the Consensus hits of every query (this file), the PubMed searches
// (pubmed_discovery.go) and the references mined from the reviews among them
// (refmine.go) — screens it with a cheap LLM (screen.go) and selects the
// corpus from what is left (candidates.go). Each candidate keeps every
// channel that found it.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/roman-redl/verdict/internal/model"
)

// consensusWindow is how deep the API ranks: (page+1)*page_size <= 1000.
const consensusWindow = 1000

// maxConsensusPages is the page bound before the server has said which page
// size it applies: the window at the smallest plan's size of 20.
const maxConsensusPages = consensusWindow / 20

// papersPerAPICall is how many returned papers one Consensus API call covers.
const papersPerAPICall = 100

// apiCalls is the quota a page spends: one call per 100 papers it returned,
// rounded up (an empty page spends nothing).
func apiCalls(papers int) int { return (papers + papersPerAPICall - 1) / papersPerAPICall }

// discoverPooled is mode B. Every Consensus request spends the plan's monthly
// quota, so the walk is page-capped and the spend is recorded in Discovery
// (and logged). The pool is not trimmed by rank: the corpus is chosen only
// after the screening, trials first, and capped at the paper limit — the
// limit caps the papers sent to the critic, not the papers discovery looks at.
// A Consensus API error fails the stage loudly: silently replacing semantic
// search with keyword search is what the engine bake-off forbids
// (docs/how-it-works.md §2.2). A PubMed failure only costs that channel (see
// pubmedChannel): the Consensus quota is already spent.
func (p *Pipeline) discoverPooled(ctx context.Context, opts Options, st *State, queries []string) error {
	plan := p.channelPlan(opts)
	withConsensus := plan.sources.Has(model.SourceConsensus)

	if withConsensus && p.clients.Search == nil {
		return &UserError{Msg: "semantic discovery is not configured (CONSENSUS_API_KEY); " +
			"without it use --discovery-sources pubmed,review_refs"}
	}

	lim := orDefault(opts.MaxPapers, p.set.MaxPapers)
	noScreen := opts.NoScreen || p.set.NoScreen
	excl := newDOIExclusion(opts.ExcludeDOIs)
	w := newConsensusWalk(queries, excl)
	info := w.info
	info.NoScreen = noScreen

	if withConsensus {
		maxPages := min(orDefault(opts.ConsensusPages, p.set.ConsensusPages), maxConsensusPages)
		if err := p.walkConsensus(ctx, w, maxPages); err != nil {
			return err
		}

		info.PapersFound = len(w.pool.list)
		st.Warnings = append(st.Warnings, w.notes...)
	} else {
		info.Queries = nil
	}

	// the Consensus pool is walked first: its review count decides PubMed in auto mode
	withPubMed := p.decidePubMed(info, plan.pubmed, w.pool.list)
	sources := channelsRun(plan.sources, withPubMed)
	info.Sources = sources

	if withPubMed {
		if err := p.pubmedChannel(ctx, opts, st, info, w.pool, !withConsensus); err != nil {
			return err
		}
	}

	info.Pool = w.pool.list

	// the exclusion is final before the mining: the pool holds no excluded
	// review whose reference list could be read; it reads the reviews of every
	// channel
	if sources.Has(model.SourceReviewRefs) {
		p.mineReviewRefs(ctx, info, noScreen, excl)
	}

	p.recordExclusion(info, excl)

	if err := p.judgePool(ctx, st, info, noScreen); err != nil {
		return err
	}

	papers := selectCandidates(info.Pool, lim)
	p.logNonStudyCandidates(info.Pool)
	st.Warnings = append(st.Warnings, p.selectionWarnings(info, len(papers), lim)...)

	st.Papers = papers
	st.Discovery = info
	p.log.Info("discovery done", "sources", sources, "queries", len(info.Queries), "pages", info.PagesFetched,
		"api_calls", info.APICalls, "page_size", info.PageSize, "pool", len(info.Pool),
		"reviews_mined", info.ReviewsMined, "refs_added", info.RefsAdded, "papers", len(papers),
		"dropped_no_doi", info.DroppedNoDOI, "truncated", info.Truncated)

	return nil
}

// judgePool screens the pool with the model, or — with --no-screen — counts
// every candidate as included, so the audit of the channels sees what each
// found, unfiltered.
func (p *Pipeline) judgePool(ctx context.Context, st *State, info *DiscoveryInfo, noScreen bool) error {
	if !noScreen {
		return p.screenPool(ctx, st, info)
	}

	for i := range info.Pool {
		if info.Pool[i].DOI != "" {
			info.Pool[i].Screen = &ScreenDecision{Include: true, Reason: noScreenReason}
		}
	}

	p.log.Info("discovery screening off (--no-screen): every candidate counts as included",
		"candidates", len(info.Pool))

	return nil
}

// noScreenReason is the decision text of a candidate counted in without
// screening.
const noScreenReason = "not screened (--no-screen)"

// selectionWarnings reports what the corpus cut left out; it also logs the cap.
func (p *Pipeline) selectionWarnings(info *DiscoveryInfo, selected, lim int) []string {
	var warnings []string

	if info.DroppedNoDOI > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"consensus: %d result(s) without a DOI dropped — the pipeline needs DOIs for enrichment",
			info.DroppedNoDOI))
	}

	switch {
	case len(info.Pool) == 0:
		// A null answer is data, not failure: the verdict path handles an
		// empty corpus honestly.
		warnings = append(warnings,
			"discovery returned no papers for the query(ies) — an honest null answer; reformulate "+
				"(bin/verdict pico) or fall back to mode C (`verdict web` + --dois)")
	case selected == 0:
		warnings = append(warnings, fmt.Sprintf(
			"discovery screening excluded all %d candidates — an honest null answer or a screen that was "+
				"too strict: the decisions are in the pool of 1-discovery.json", len(info.Pool)))
	}

	if eligible := countEligible(info.Pool); eligible > selected {
		p.log.Warn("capping the corpus: the best-ranked trials go first, reviews are limited",
			"eligible", eligible, "keep", selected, "limit", lim,
			"hint", "raise --max-papers (or MAX_PAPERS) for a fatter, costlier corpus")
	}

	return warnings
}

// logNonStudyCandidates logs what the publication types keep out of the
// corpus, one line per kind of paper that is not a study report.
func (p *Pipeline) logNonStudyCandidates(pool []Candidate) {
	counts := map[string]int{}

	for i := range pool {
		if pool[i].DOI == "" {
			continue
		}

		if kind := model.NonStudyKind(pool[i].PublicationTypes); kind != "" {
			counts[kind]++
		}
	}

	for _, kind := range slices.Sorted(maps.Keys(counts)) {
		p.log.Info("candidates left out of the corpus: not a study report", "kind", kind, "count", counts[kind])
	}
}

func countEligible(pool []Candidate) int {
	n := 0

	for i := range pool {
		if pool[i].Eligible() {
			n++
		}
	}

	return n
}

// consensusWalk is the state of a page walk over several semantic queries:
// the pool of candidates, deduplicated by DOI across queries. A paper found by
// several queries keeps its rank in each.
type consensusWalk struct {
	queries []string
	pool    *candidatePool
	// seen counts the results of each query so far: the rank of the next one,
	// DOI-less results included — the rank is a position in the ranking.
	seen   []int
	info   *DiscoveryInfo
	denied bool     // a query's pagination was refused
	notes  []string // plan-related warnings for the artifact
}

func newConsensusWalk(queries []string, excl *doiExclusion) *consensusWalk {
	return &consensusWalk{
		queries: queries,
		pool:    newCandidatePool(excl),
		seen:    make([]int, len(queries)),
		info:    &DiscoveryInfo{Engine: EngineConsensus, Queries: queries},
	}
}

// add pools one page of results for query qi.
func (w *consensusWalk) add(qi int, papers []model.Paper) {
	for i := range papers {
		w.seen[qi]++

		if model.NormalizeDOI(papers[i].DOI) == "" {
			w.info.DroppedNoDOI++ // the pipeline keys papers by DOI
			continue
		}

		w.pool.add(newCandidate(papers[i], FoundBy{Source: model.SourceConsensus, Query: qi + 1, Rank: w.seen[qi]}))
	}
}

// pageCap is the page limit of the walk: the requested one, within the API's
// ranking window at the page size the server applied (a page past the window
// is a 422).
func (w *consensusWalk) pageCap(maxPages int) int {
	if size := w.info.PageSize; size > 0 {
		return min(maxPages, consensusWindow/size)
	}

	return maxPages
}

// walkConsensus fetches result pages round-robin over the queries until the
// page cap or the end of every ranking. It does not stop at a pool size: the
// pool is screened and selected afterwards, not trimmed by rank.
func (p *Pipeline) walkConsensus(ctx context.Context, w *consensusWalk, maxPages int) error {
	active := make([]int, len(w.queries))
	for i := range active {
		active[i] = i
	}

	for page := 0; page < w.pageCap(maxPages) && len(active) > 0; page++ {
		var next []int

		for _, qi := range active {
			more, err := p.fetchPage(ctx, w, qi, page)
			if err != nil {
				return err
			}

			if more {
				next = append(next, qi)
			}
		}

		active = next
	}
	// Queries still live when the page cap stopped the walk: the pool may be
	// thinner than the ranking.
	w.info.Truncated = len(active) > 0 || w.denied

	return nil
}

// fetchPage pools one page of one query and reports whether the query has
// more pages. A page after the first that the plan refuses ends that query's
// walk with a warning — the pages already pooled are kept; any other error
// fails the stage.
func (p *Pipeline) fetchPage(ctx context.Context, w *consensusWalk, qi, page int) (bool, error) {
	pg, err := p.clients.Search.Page(ctx, w.queries[qi], page)
	if err != nil {
		if page > 0 && errors.Is(err, ErrPaginationDenied) {
			p.log.Warn("consensus refused a page after the first: pagination needs a paid Consensus plan "+
				"(a Free key gets page 0 only) — keeping what was fetched",
				"query", w.queries[qi], "page", page, "applied_page_size", w.info.PageSize, "err", err)

			if !w.denied {
				w.notes = append(w.notes, fmt.Sprintf(
					"consensus: pages after the first were refused (applied page_size %d) — pagination "+
						"needs a paid Consensus plan; the pool holds the first page of each query",
					w.info.PageSize))
			}

			w.denied = true

			return false, nil
		}

		return false, fmt.Errorf("consensus search (query %q, page %d): %w — no automatic fallback "+
			"to keyword search; escape hatch: `verdict web` + `run --dois` (mode C, free)", w.queries[qi], page, err)
	}

	w.info.PagesFetched++
	w.info.APICalls += apiCalls(len(pg.Papers))
	p.notePageSize(w, pg)
	w.add(qi, pg.Papers)
	p.log.Info("consensus page done", "query", w.queries[qi], "page", page, "papers", len(pg.Papers),
		"page_size", pg.PageSize, "pool", len(w.pool.list))

	// An empty page is the backend's honest "no more", not an error.
	return !pg.IsEnd && len(pg.Papers) > 0, nil
}

// notePageSize records the page size the server applied and warns, once, when
// the plan caps it below the requested one: a smaller plan is visible at once.
func (p *Pipeline) notePageSize(w *consensusWalk, pg model.SearchPage) {
	if pg.PageSize <= 0 {
		return
	}

	asked := cmp.Or(pg.PageSizeAsked, pg.PageSize)
	if pg.PageSize < asked && w.info.PageSize != pg.PageSize {
		p.log.Warn("consensus applied a smaller page_size than requested: the plan caps it",
			"requested", asked, "applied", pg.PageSize)
		w.notes = append(w.notes, fmt.Sprintf(
			"consensus: page_size %d applied instead of the requested %d — the plan caps it", pg.PageSize, asked))
	}

	w.info.PageSize = pg.PageSize
}
