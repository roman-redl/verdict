package pipeline

// Attribution in the discovery pool. A paper several channels found keeps all
// of them, each with its own rank, so the contribution of every channel can be
// measured from 1-discovery.json alone.

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/roman-redl/verdict/internal/model"
)

// FoundBy is one channel's finding of a paper: Consensus (one entry per
// query), PubMed (one per search) or the reference lists of reviews.
type FoundBy struct {
	Source model.Source `json:"source"` // consensus | pubmed | review_refs
	// Query is the 1-based number of the Consensus query (Consensus only).
	Query int `json:"query,omitempty"`
	// Search names the PubMed search (PubMed only).
	Search PubMedSearch `json:"search,omitempty"`
	// Rank is the 1-based position in the channel's ranking, over all pages;
	// zero for the review references, which have no ranking.
	Rank int `json:"rank,omitempty"`
	// CitingReviews counts the mined reviews whose reference list holds the
	// paper (review references only).
	CitingReviews int `json:"citing_reviews,omitempty"`
}

// sameChannel reports whether two findings come from the same ranking: the
// same source and, for Consensus, the same query, for PubMed, the same search.
func (f *FoundBy) sameChannel(o FoundBy) bool {
	return f.Source == o.Source && f.Query == o.Query && f.Search == o.Search
}

// merge folds another finding of the same channel into this one: the better
// rank stays, the citing reviews add up.
func (f *FoundBy) merge(o FoundBy) {
	if f.Rank == 0 || (o.Rank > 0 && o.Rank < f.Rank) {
		f.Rank = o.Rank
	}

	f.CitingReviews += o.CitingReviews
}

// addSource records a finding; one already recorded for the same channel is
// merged into.
func (c *Candidate) addSource(by FoundBy) {
	for i := range c.Sources {
		if c.Sources[i].sameChannel(by) {
			c.Sources[i].merge(by)
			return
		}
	}

	c.Sources = append(c.Sources, by)
}

// foundBy reports whether the source found the paper, in any of its queries.
func (c *Candidate) foundBy(src model.Source) bool {
	for _, s := range c.Sources {
		if s.Source == src {
			return true
		}
	}

	return false
}

// channels counts the distinct sources that found the paper.
func (c *Candidate) channels() int {
	seen := map[model.Source]bool{}
	for _, s := range c.Sources {
		seen[s.Source] = true
	}

	return len(seen)
}

// citingReviews counts the mined reviews that list the paper.
func (c *Candidate) citingReviews() int {
	n := 0
	for _, s := range c.Sources {
		n += s.CitingReviews
	}

	return n
}

// bestRank is the best position any ranked channel gave the paper, and the
// Consensus query of that finding as a sort key (a PubMed finding sorts after
// every query). ok is false for a paper no ranking placed.
func (c *Candidate) bestRank() (rank, query int, ok bool) {
	for _, s := range c.Sources {
		if s.Rank == 0 {
			continue
		}

		q := s.Query
		if q == 0 {
			q = math.MaxInt
		}

		if !ok || s.Rank < rank || (s.Rank == rank && q < query) {
			rank, query, ok = s.Rank, q, true
		}
	}

	return rank, query, ok
}

// UnmarshalJSON reads a candidate of an artifact written before the sources
// list existed: its single source, query, rank and cited_by_reviews become
// entries of Sources.
func (c *Candidate) UnmarshalJSON(data []byte) error {
	type plain Candidate

	aux := struct {
		*plain
		Source         model.Source `json:"source"`
		Query          int          `json:"query"`
		Rank           int          `json:"rank"`
		CitedByReviews int          `json:"cited_by_reviews"`
	}{plain: (*plain)(c)}

	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("decoding a pool candidate: %w", err)
	}

	if len(c.Sources) > 0 {
		return nil
	}

	if aux.Source != "" && aux.Source != model.SourceReviewRefs {
		c.Sources = append(c.Sources, FoundBy{Source: aux.Source, Query: aux.Query, Rank: aux.Rank})
	}

	if aux.CitedByReviews > 0 || aux.Source == model.SourceReviewRefs {
		c.Sources = append(c.Sources, FoundBy{Source: model.SourceReviewRefs, CitingReviews: aux.CitedByReviews})
	}

	return nil
}

// candidatePool is the pool under construction: candidates in the order they
// were found, deduplicated by normalized DOI (a record without one, by PMID).
// An excluded DOI never enters it, whichever channel brings it.
type candidatePool struct {
	list []Candidate
	at   map[string]int
	excl *doiExclusion
}

func newCandidatePool(excl *doiExclusion) *candidatePool {
	return &candidatePool{at: map[string]int{}, excl: excl}
}

// key identifies a candidate in the pool; "" for one with neither a DOI nor a
// PMID, which cannot be told from another.
func (c *Candidate) key() string {
	switch {
	case c.DOI != "":
		return c.DOI
	case c.PMID != "":
		return "pmid:" + c.PMID
	}

	return ""
}

// add puts the candidate into the pool. A paper already there gains the new
// finding and whatever the first record lacked, and keeps its first record as
// the corpus paper (channels run richest first).
func (cp *candidatePool) add(c Candidate) {
	key := c.key()
	if key == "" || cp.excl.drop(c.DOI) {
		return
	}

	at, known := cp.at[key]
	if !known {
		cp.at[key] = len(cp.list)
		cp.list = append(cp.list, c)

		return
	}

	have := &cp.list[at]

	for _, by := range c.Sources {
		have.addSource(by)
	}

	if have.Design == "" {
		have.Design = c.Design
	}

	if have.Year == 0 {
		have.Year = c.Year
	}

	// the types any channel gave the paper reach the corpus record too
	have.PublicationTypes = model.MergePublicationTypes(have.PublicationTypes, c.PublicationTypes)
	have.paper.PublicationTypes = have.PublicationTypes

	if have.PMID == "" {
		have.PMID = c.PMID
	}

	if have.Title == "" {
		have.Title, have.paper = c.Title, c.paper
	}
}
