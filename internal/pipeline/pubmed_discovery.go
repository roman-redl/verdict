package pipeline

// The PubMed channel of mode B. Two searches per run, both composed in Go from
// the topic query: the topic AND the Cochrane Highly Sensitive Search Strategy
// for randomized trials, and the topic AND a systematic-review filter. PubMed
// ranks by relevance; the best of each search enter the pool with their rank.

import (
	"context"
	"fmt"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// cochraneHSSS is the Cochrane Highly Sensitive Search Strategy for
// identifying randomized trials in MEDLINE, PubMed format: the
// sensitivity-maximizing version (2008 revision) from the Cochrane Handbook
// for Systematic Reviews of Interventions, Technical Supplement to Chapter 4
// (Lefebvre C, Glanville J, et al.; the same text is on
// https://work.cochrane.org/pubmed). It finds about 0.96 of the randomized
// trials when the topic query is good. The Handbook's "sensitivity- and
// precision-maximizing" version is another, narrower filter.
const cochraneHSSS = `(randomized controlled trial[pt] OR controlled clinical trial[pt] OR randomized[tiab] OR ` +
	`placebo[tiab] OR drug therapy[sh] OR randomly[tiab] OR trial[tiab] OR groups[tiab]) ` +
	`NOT (animals[mh] NOT humans[mh])`

// reviewsFilter picks PubMed's systematic reviews and meta-analyses.
const reviewsFilter = `systematic[sb] OR meta-analysis[pt]`

// Caps of the two searches: the trials are the corpus, the reviews only feed
// the reference mining and the context.
const (
	rctFilterLimit = 300
	reviewsLimit   = 50
)

// PubMedSearch labels one of the two PubMed searches of a run.
type PubMedSearch string

const (
	// SearchRCTFilter is the topic with the Cochrane trial filter.
	SearchRCTFilter PubMedSearch = "rct_filter"
	// SearchReviews is the topic with the review filter.
	SearchReviews PubMedSearch = "reviews"
)

// limit is how many hits the search keeps.
func (s PubMedSearch) limit() int {
	switch s {
	case SearchRCTFilter:
		return rctFilterLimit
	case SearchReviews:
		return reviewsLimit
	}

	return 0
}

// query composes the search from the topic query.
func (s PubMedSearch) query(topic string) string {
	filter := reviewsFilter
	if s == SearchRCTFilter {
		filter = cochraneHSSS
	}

	return "(" + topic + ") AND (" + filter + ")"
}

// PubMedSearchInfo records one search: the query as sent, how many records
// PubMed matched in all, how many the run kept, and how many of those have no
// DOI.
type PubMedSearchInfo struct {
	Search   PubMedSearch `json:"search"`
	Query    string       `json:"query"`
	Total    int          `json:"total"`
	Returned int          `json:"returned"`
	NoDOI    int          `json:"no_doi,omitempty"`
	// ComparatorDropped marks the retry of an empty trial search without the
	// comparator concept.
	ComparatorDropped bool `json:"comparator_dropped,omitempty"`
}

// PubMedInfo is the audit trail of the PubMed channel.
type PubMedInfo struct {
	// TopicQuery is the topic query as sent (the comparator included).
	TopicQuery string `json:"topic_query,omitempty"`
	// Supplied marks a topic query given with --pubmed-query: no model call.
	Supplied bool `json:"supplied,omitempty"`
	// ModelQuery is the query the model wrote itself, kept for the log only:
	// the one sent is composed from Concepts in Go.
	ModelQuery string             `json:"model_query,omitempty"`
	Concepts   []PubMedConcept    `json:"concepts,omitempty"`
	Searches   []PubMedSearchInfo `json:"searches"`
	// NoDOI counts the distinct records without a DOI: they sit in the pool
	// for the audit and cannot enter the corpus.
	NoDOI int `json:"no_doi,omitempty"`
	// Error is why the channel brought nothing: its failure is a warning, not
	// a failed run.
	Error string `json:"error,omitempty"`
}

// pubmedChannel runs the PubMed channel into the pool. Its failure is a
// warning — the Consensus quota is already spent and the other channels still
// deliver — unless PubMed is the run's only search channel (alone), where
// failing is the only honest answer.
func (p *Pipeline) pubmedChannel(
	ctx context.Context, opts Options, st *State, info *DiscoveryInfo, pool *candidatePool, alone bool,
) error {
	if p.clients.PubMed == nil {
		p.log.Info("PubMed discovery skipped: PubMed is not configured")
		return nil
	}

	pm := &PubMedInfo{}
	info.PubMed = pm

	err := p.runPubMed(ctx, opts, st, pm, pool)
	if err == nil {
		return nil
	}

	if alone {
		return fmt.Errorf("PubMed discovery: %w", err)
	}

	p.log.Warn("PubMed discovery failed: the run goes on with the other channels", "err", err)

	pm.Error = persistedErr(err)
	st.Warnings = append(st.Warnings, "pubmed: discovery failed, the pool has no PubMed candidates: "+pm.Error)

	return nil
}

// runPubMed writes (or takes) the topic query, runs the two searches and adds
// their hits to the pool.
func (p *Pipeline) runPubMed(ctx context.Context, opts Options, st *State, pm *PubMedInfo, pool *candidatePool) error {
	concepts, err := p.pubmedTopic(ctx, opts, st, pm)
	if err != nil {
		return err
	}

	hits := map[PubMedSearch][]string{}

	topic := pm.TopicQuery

	rct, err := p.pubmedSearch(ctx, pm, SearchRCTFilter, topic, false)
	if err != nil {
		return err
	}

	// An empty trial search is usually a topic too narrow: the comparator is
	// the concept most likely to be, and the one safe to drop.
	if narrow := topicQuery(concepts, false); rct.Total == 0 && hasComparator(concepts) && narrow != "" {
		p.log.Warn("PubMed trial search found nothing: retrying without the comparator concept", "query", narrow)

		topic = narrow

		if rct, err = p.pubmedSearch(ctx, pm, SearchRCTFilter, topic, true); err != nil {
			return err
		}
	}

	hits[SearchRCTFilter] = rct.PMIDs

	rev, err := p.pubmedSearch(ctx, pm, SearchReviews, topic, false)
	if err != nil {
		return err
	}

	hits[SearchReviews] = rev.PMIDs

	return p.poolPubMed(ctx, st, pm, pool, hits)
}

// pubmedTopic returns the concepts the topic query is made of (none for a
// supplied query) and records the topic query in pm.
func (p *Pipeline) pubmedTopic(ctx context.Context, opts Options, st *State, pm *PubMedInfo) ([]PubMedConcept, error) {
	if q := strings.TrimSpace(opts.PubMedQuery); q != "" {
		if !balanced(q) {
			return nil, &UserError{Msg: "--pubmed-query has unbalanced parentheses or quotes"}
		}

		pm.Supplied, pm.TopicQuery = true, q

		return nil, nil
	}

	concepts, modelQuery, err := p.writePubMedQuery(ctx, st, st.Question)
	if err != nil {
		return nil, err
	}

	pm.Concepts, pm.ModelQuery = concepts, modelQuery
	pm.TopicQuery = topicQuery(concepts, true)

	if pm.TopicQuery == "" || !balanced(pm.TopicQuery) {
		return nil, fmt.Errorf("the composed PubMed topic query is empty or unbalanced: %q", pm.TopicQuery)
	}

	p.log.Info("PubMed topic query written", "query", pm.TopicQuery, "model_query", modelQuery)

	return concepts, nil
}

// pubmedSearch runs one search and records it.
func (p *Pipeline) pubmedSearch(
	ctx context.Context, pm *PubMedInfo, search PubMedSearch, topic string, narrowed bool,
) (model.PubMedHits, error) {
	query := search.query(topic)

	hits, err := p.clients.PubMed.Search(ctx, query, search.limit())
	if err != nil {
		return model.PubMedHits{}, fmt.Errorf("the %s search: %w", search, err)
	}

	pm.Searches = append(pm.Searches, PubMedSearchInfo{
		Search: search, Query: query, Total: hits.Total, Returned: len(hits.PMIDs), ComparatorDropped: narrowed,
	})
	p.log.Info("PubMed search done", "search", search, "total", hits.Total, "returned", len(hits.PMIDs),
		"comparator_dropped", narrowed)

	return hits, nil
}

// poolPubMed reads the records of the hits and adds each to the pool with its
// rank in its search. A record without a DOI enters the pool too, for the
// audit, and is counted.
func (p *Pipeline) poolPubMed(
	ctx context.Context, st *State, pm *PubMedInfo, pool *candidatePool, hits map[PubMedSearch][]string,
) error {
	var all []string

	seen := map[string]bool{}

	for _, search := range []PubMedSearch{SearchRCTFilter, SearchReviews} {
		for _, id := range hits[search] {
			if !seen[id] {
				seen[id] = true
				all = append(all, id)
			}
		}
	}

	records, err := p.clients.PubMed.Summaries(ctx, all)
	if err != nil {
		return fmt.Errorf("reading the PubMed records: %w", err)
	}

	byPMID := make(map[string]model.PubMedRecord, len(records))
	for _, r := range records {
		byPMID[r.PMID] = r
	}

	if len(byPMID) < len(all) {
		p.log.Warn("PubMed returned no record for some hits", "hits", len(all), "records", len(byPMID))
	}

	noDOI := map[string]bool{}

	for _, search := range []PubMedSearch{SearchRCTFilter, SearchReviews} {
		for i, id := range hits[search] {
			rec, ok := byPMID[id]
			if !ok {
				continue
			}

			if rec.DOI == "" {
				noDOI[id] = true

				pm.noDOIOf(search)
			}

			pool.add(pubmedCandidate(rec, FoundBy{Source: model.SourcePubMed, Search: search, Rank: i + 1}))
		}
	}

	pm.NoDOI = len(noDOI)

	if pm.NoDOI > 0 {
		p.log.Info("PubMed records without a DOI kept in the pool, not selectable", "count", pm.NoDOI)

		st.Warnings = append(st.Warnings, fmt.Sprintf(
			"pubmed: %d record(s) without a DOI are in the pool for the audit but cannot enter the corpus — "+
				"the pipeline is keyed by DOI", pm.NoDOI))
	}

	return nil
}

// noDOIOf counts one more DOI-less hit in the last record of the search.
func (pm *PubMedInfo) noDOIOf(search PubMedSearch) {
	for i := len(pm.Searches) - 1; i >= 0; i-- {
		if pm.Searches[i].Search == search {
			pm.Searches[i].NoDOI++
			return
		}
	}
}

// pubmedCandidate wraps a PubMed record as a pool candidate.
func pubmedCandidate(rec model.PubMedRecord, by FoundBy) Candidate {
	c := newCandidate(model.Paper{
		DOI: rec.DOI, Title: rec.Title, Year: rec.Year, StudyDesign: designTagFromPubTypes(rec.PubTypes),
		PublicationTypes: model.NormalizePublicationTypes(rec.PubTypes),
		Source:           model.SourcePubMed,
	}, by)
	c.PMID = rec.PMID

	return c
}

// designTagFromPubTypes maps NLM's publication types onto the discovery tag
// the design resolver reads: a review or a meta-analysis is told first (a
// meta-analysis of trials is also tagged as a trial), then a trial. Empty when
// the types say neither.
func designTagFromPubTypes(types []string) string {
	review, trial := "", false

	for _, t := range types {
		switch lower := strings.ToLower(strings.TrimSpace(t)); {
		case lower == discTagMeta:
			return discTagMeta
		case lower == discTagSystematic:
			review = discTagSystematic
		case lower == "randomized controlled trial", lower == "controlled clinical trial",
			lower == "pragmatic clinical trial", lower == "clinical trial", strings.HasPrefix(lower, "clinical trial, "):
			trial = true
		}
	}

	switch {
	case review != "":
		return review
	case trial:
		return discTagRCT
	}

	return ""
}
