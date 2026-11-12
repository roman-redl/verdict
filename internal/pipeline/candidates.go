package pipeline

// The discovery pool: every paper discovery considered, before the corpus is
// cut. Consensus hits, PubMed hits and the references mined from reviews enter
// it; the LLM screening annotates it; the selection turns it into the corpus
// (the papers the critic reads).

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// maxSelectedReviews caps the reviews in the corpus: a review repeats the data
// of the trials it covers, so a few give the context and more only crowd out
// primary studies.
const maxSelectedReviews = 3

// snippetChars caps the summary kept with a candidate.
const snippetChars = 300

// ScreenDesign is the screening model's guess of a candidate's design from
// its title and snippet — coarser than model.StudyDesign, because that is all
// a title can tell.
type ScreenDesign string

const (
	// ScreenRCT is a randomized trial (parallel, crossover or cluster).
	ScreenRCT ScreenDesign = "rct"
	// ScreenOtherPrimary is any other original study.
	ScreenOtherPrimary ScreenDesign = "other_primary"
	// ScreenReview is a systematic review, a meta-analysis or another review.
	ScreenReview ScreenDesign = "review"
	// ScreenOther is not a study: an editorial, a protocol, a guideline.
	ScreenOther ScreenDesign = "other"
)

// ParseScreenDesign parses the design as the screening model writes it,
// ignoring case and reading spaces and hyphens as underscores.
func ParseScreenDesign(s string) (ScreenDesign, error) {
	d := ScreenDesign(strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(s))))

	switch d {
	case ScreenRCT, ScreenOtherPrimary, ScreenReview, ScreenOther:
		return d, nil
	}

	return "", fmt.Errorf("unknown screening design %q (want rct | other_primary | review | other)", s)
}

// ScreenDecision is the screening verdict on one candidate.
type ScreenDecision struct {
	Include bool         `json:"include"`
	Design  ScreenDesign `json:"design,omitempty"`
	Reason  string       `json:"reason,omitempty"`
}

// Candidate is one paper of the discovery pool, as saved in 1-discovery.json.
type Candidate struct {
	// DOI is empty for a PubMed record without one: it stays in the pool for
	// the audit but cannot be selected (the pipeline is keyed by DOI).
	DOI   string `json:"doi"`
	PMID  string `json:"pmid,omitempty"`
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
	// Sources lists every channel that found the paper, each with its own
	// rank (see FoundBy): the audit of what each channel contributes.
	Sources []FoundBy `json:"sources"`
	// Design is the discovery study-type tag (Consensus's, or derived from
	// PubMed's publication types), empty for mined references.
	Design string `json:"design,omitempty"`
	// PublicationTypes are the types the discovering source gave the paper
	// (PubMed's publication types, OpenAlex's type), lower case; empty for a
	// Consensus hit no other channel typed. A paper they show not to be a study
	// report (an erratum, a comment, a letter) is not eligible.
	PublicationTypes []string `json:"publication_types,omitempty"`
	// Snippet is the takeaway or the abstract start: what the screening saw
	// besides the title.
	Snippet string `json:"snippet,omitempty"`
	// Screen is the LLM screening decision; nil when the paper was not
	// screened (no screening backend, or its batch failed).
	Screen *ScreenDecision `json:"screen,omitempty"`
	// Selected marks the papers that entered the corpus.
	Selected bool `json:"selected"`

	// paper is the full record; it becomes the corpus paper when selected.
	paper model.Paper
}

// newCandidate wraps a paper of the pool, found by one channel.
func newCandidate(p model.Paper, by FoundBy) Candidate {
	return Candidate{
		DOI: model.NormalizeDOI(p.DOI), Title: p.Title, Year: p.Year, Sources: []FoundBy{by},
		Design:           p.StudyDesign,
		PublicationTypes: p.PublicationTypes,
		Snippet:          truncate(cmp.Or(p.Takeaway, p.Abstract), snippetChars),
		paper:            p,
	}
}

// design is the best design known before any critique: the Consensus tag,
// then the title (the same resolver the later stages use).
func (c *Candidate) design() model.StudyDesign {
	var e model.EnrichedPaper

	e.Title, e.StudyDesign = c.Title, c.Design

	return resolveDesign(&e, model.Critique{})
}

// Eligible reports whether the candidate may enter the corpus: what the
// screening stage lets through (the eval reads it as "kept by screening"). A
// paper with no DOI never does, nor does one whose publication types show it is
// not a study report (an erratum takes no corpus slot, whatever the screening
// says). An unscreened Consensus hit is on-topic by construction and stays in;
// an unscreened PubMed hit (a loose study-design filter over an LLM-written
// topic query) or reference (one of hundreds picked by citation alone) stays
// out.
func (c *Candidate) Eligible() bool {
	if c.DOI == "" || model.NonStudyKind(c.PublicationTypes) != "" {
		return false
	}

	if c.Screen != nil {
		return c.Screen.Include
	}

	return c.foundBy(model.SourceConsensus)
}

// designGroup orders the corpus: trials first, then other primary studies,
// then reviews.
type designGroup int

const (
	groupRCT designGroup = iota
	groupPrimary
	groupReview
)

// group places the candidate by the known design first (a review is told by
// the tag or the title, which is reliable), then by the screening guess.
func (c *Candidate) group() designGroup {
	d := c.design()

	switch {
	case d.IsSecondary():
		return groupReview
	case d == model.DesignRCT || d == model.DesignClusterRCT || d == model.DesignCrossoverRCT:
		return groupRCT
	case c.Screen != nil && c.Screen.Design == ScreenRCT:
		return groupRCT
	case c.Screen != nil && c.Screen.Design == ScreenReview:
		return groupReview
	}

	return groupPrimary
}

// betterRanked orders two candidates of one group: those a ranked channel
// (Consensus, PubMed) placed come first, by the best rank over the channels —
// a position in a ranked list means the same in each; ties go to the paper
// more channels found, then to the lower Consensus query number, then to the
// one more reviews cite. The papers only reviews cite follow, the most cited
// first; pool order settles the rest.
func betterRanked(a, b *Candidate) int {
	ar, aq, aRanked := a.bestRank()
	br, bq, bRanked := b.bestRank()

	if aRanked != bRanked {
		if aRanked {
			return -1
		}

		return 1
	}

	if aRanked {
		if c := cmp.Compare(ar, br); c != 0 {
			return c
		}
	}

	if c := cmp.Compare(b.channels(), a.channels()); c != 0 {
		return c
	}

	if aRanked {
		if c := cmp.Compare(aq, bq); c != 0 {
			return c
		}
	}

	return cmp.Compare(b.citingReviews(), a.citingReviews())
}

// selectCandidates builds the corpus from the pool and marks the chosen
// candidates: the eligible ones, trials first, then other primary studies,
// then at most maxSelectedReviews reviews, each group by rank, cut to limit.
func selectCandidates(pool []Candidate, limit int) []model.Paper {
	var groups [groupReview + 1][]int

	for i := range pool {
		if pool[i].Eligible() {
			g := pool[i].group()
			groups[g] = append(groups[g], i)
		}
	}

	var papers []model.Paper

	for g, idx := range groups {
		// SortStableFunc keeps the pool order between equal candidates
		slices.SortStableFunc(idx, func(a, b int) int { return betterRanked(&pool[a], &pool[b]) })

		if designGroup(g) == groupReview {
			idx = idx[:min(len(idx), maxSelectedReviews)]
		}

		for _, i := range idx {
			if len(papers) >= limit {
				return papers
			}

			pool[i].Selected = true
			papers = append(papers, pool[i].paper)
		}
	}

	return papers
}
