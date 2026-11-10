package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// Fixture values of the discovery pool tests, shared across their files.
const (
	abstractText = "the abstract"
	doiReview    = "10.1/rev"
	doiPoolTrial = "10.1/trial"
	guessRCT     = string(ScreenRCT)
	guessOther   = string(ScreenOther)
)

// cons is a Consensus hit of the pool: query and rank are 1-based.
func cons(doi string, query, rank int, tag string) Candidate {
	return newCandidate(model.Paper{
		DOI: doi, Title: "Title " + doi, StudyDesign: tag, Source: model.SourceConsensus,
	}, FoundBy{Source: model.SourceConsensus, Query: query, Rank: rank})
}

// pub is a PubMed hit of the pool: search and rank are as PubMed gave them.
func pub(doi string, search PubMedSearch, rank int, tag string) Candidate {
	return newCandidate(model.Paper{
		DOI: doi, Title: "Title " + doi, StudyDesign: tag, Source: model.SourcePubMed,
	}, FoundBy{Source: model.SourcePubMed, Search: search, Rank: rank})
}

// screenedPub is a PubMed hit the screening let in: an unscreened one stays
// out of the corpus.
func screenedPub(doi string, search PubMedSearch, rank int, tag string) Candidate {
	return withScreen(pub(doi, search, rank, tag), ScreenDecision{Include: true})
}

// alsoBy adds a finding of another channel to a fixture candidate.
func alsoBy(c Candidate, by FoundBy) Candidate {
	c.Sources = append([]FoundBy(nil), c.Sources...)
	c.addSource(by)

	return c
}

// mine is a reference mined from reviews, cited by n of them.
func mine(doi, title string, n int, screen *ScreenDecision) Candidate {
	c := newCandidate(model.Paper{DOI: doi, Title: title, Source: model.SourceReviewRefs},
		FoundBy{Source: model.SourceReviewRefs, CitingReviews: n})
	c.Screen = screen

	return c
}

// withScreen attaches a screening decision to a fixture candidate.
func withScreen(c Candidate, d ScreenDecision) Candidate {
	c.Screen = &d

	return c
}

// withTitle replaces the fixture's placeholder title.
func withTitle(c Candidate, title string) Candidate {
	c.Title = title
	c.paper.Title = title

	return c
}

func includeAs(d ScreenDesign) *ScreenDecision { return &ScreenDecision{Include: true, Design: d} }

func dois(papers []model.Paper) []string {
	out := make([]string, len(papers))
	for i := range papers {
		out[i] = papers[i].DOI
	}

	return out
}

func TestSelectCandidates(t *testing.T) {
	excluded := ScreenDecision{Include: false, Design: ScreenOther}

	tests := []struct {
		name  string
		pool  []Candidate
		limit int
		want  []string
	}{
		{
			name: "trials first, then other primary studies, then reviews",
			pool: []Candidate{
				cons("r1", 1, 1, tagSystematicReview), cons("c1", 1, 2, tagCohort),
				cons("t1", 1, 3, designRCT), cons("t2", 1, 4, designRCT),
			},
			limit: 10, want: []string{"t1", "t2", "c1", "r1"},
		},
		{
			name: "at most three reviews, strongest first",
			pool: []Candidate{
				cons("r5", 1, 5, tagSystematicReview), cons("r1", 1, 1, tagSystematicReview), cons("r3", 1, 3, "meta-analysis"),
				cons("r2", 1, 2, tagSystematicReview), cons("r4", 1, 4, tagSystematicReview),
			},
			limit: 10, want: []string{"r1", "r2", "r3"},
		},
		{
			name: "the cut falls on the later groups",
			pool: []Candidate{
				cons("c1", 1, 1, tagCohort), cons("t1", 1, 2, designRCT), cons("t2", 1, 3, designRCT), cons("c2", 1, 4, tagCohort),
			},
			limit: 3, want: []string{"t1", "t2", "c1"},
		},
		{
			name: "more trials than the limit: no room for the rest",
			pool: []Candidate{
				cons("c1", 1, 1, tagCohort), cons("t1", 1, 2, designRCT), cons("t2", 1, 3, designRCT),
			},
			limit: 2, want: []string{"t1", "t2"},
		},
		{
			name: "rank first, then the query: the queries share the slots",
			pool: []Candidate{
				cons("q1r2", 1, 2, designRCT), cons("q2r1", 2, 1, designRCT), cons("q1r1", 1, 1, designRCT),
			},
			limit: 10, want: []string{"q1r1", "q2r1", "q1r2"},
		},
		{
			name: "ranks of the channels compare; a tie goes to the paper more channels found",
			pool: []Candidate{
				screenedPub("pub3", SearchRCTFilter, 3, pmTagRCT), cons("con2", 1, 2, designRCT),
				screenedPub("pub1", SearchRCTFilter, 1, pmTagRCT),
				alsoBy(cons("both", 1, 3, designRCT), FoundBy{Source: model.SourcePubMed, Search: SearchRCTFilter, Rank: 1}),
			},
			limit: 10, want: []string{"both", "pub1", "con2", "pub3"},
		},
		{
			name: "a PubMed review is a review: told by its tag, capped with the others",
			pool: []Candidate{
				screenedPub("r1", SearchReviews, 1, pmTagMeta), screenedPub("r2", SearchReviews, 2, discTagSystematic),
				screenedPub("r3", SearchReviews, 3, pmTagMeta), screenedPub("r4", SearchReviews, 4, pmTagMeta),
				screenedPub("t1", SearchRCTFilter, 9, pmTagRCT),
			},
			limit: 10, want: []string{"t1", "r1", "r2", "r3"},
		},
		{
			name: "a screened-in PubMed hit competes with the Consensus ones by rank",
			pool: []Candidate{
				cons("c2", 1, 2, designRCT), withScreen(pub("p1", SearchRCTFilter, 1, pmTagRCT), *includeAs(ScreenRCT)),
				pub("unscreened", SearchRCTFilter, 1, pmTagRCT),
			},
			limit: 10, want: []string{"p1", "c2"},
		},
		{
			name: "mined references follow the Consensus hits of their group, the most cited first",
			pool: []Candidate{
				mine("m1", "Trial one", 1, includeAs(ScreenRCT)),
				cons("t9", 1, 9, designRCT),
				mine("m3", "Trial three", 3, includeAs(ScreenRCT)),
			},
			limit: 10, want: []string{"t9", "m3", "m1"},
		},
		{
			name: "a screened trial joins the trials, a screened other study the primary group",
			pool: []Candidate{
				cons("c1", 1, 1, tagCohort),
				withScreen(cons("untagged", 1, 2, ""), *includeAs(ScreenRCT)),
				mine("m1", "Registry study", 5, includeAs(ScreenOtherPrimary)),
			},
			limit: 10, want: []string{"untagged", "c1", "m1"},
		},
		{
			name: "a review is told by its title or by the screening guess",
			pool: []Candidate{
				withTitle(cons("titled", 1, 1, ""), "Creatine: a meta-analysis"),
				mine("m1", "Pooled evidence", 2, includeAs(ScreenReview)),
				cons("t1", 1, 3, designRCT),
			},
			limit: 10, want: []string{"t1", "titled", "m1"},
		},
		{
			name: "excluded candidates and unscreened mined references stay out; an unscreened hit stays in",
			pool: []Candidate{
				withScreen(cons("no", 1, 1, designRCT), excluded),
				mine("unscreened", "Unjudged reference", 9, nil),
				cons("hit", 1, 2, designRCT),
			},
			limit: 10, want: []string{"hit"},
		},
		{name: "an empty pool", pool: nil, limit: 5, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := append([]Candidate(nil), tt.pool...)
			got := selectCandidates(pool, tt.limit)

			assert.Equal(t, tt.want, dois(got))

			selected := 0

			for i := range pool {
				if pool[i].Selected {
					selected++
				}
			}

			assert.Len(t, got, selected, "exactly the returned papers are marked selected")
		})
	}
}

// The corpus papers are the candidates' full records, mined ones included.
func TestSelectCandidatesReturnsTheFullRecords(t *testing.T) {
	c := cons("10.1/a", 1, 1, designRCT)
	c.paper.Abstract = abstractText

	pool := []Candidate{c}
	got := selectCandidates(pool, 5)

	require.Len(t, got, 1)
	assert.Equal(t, abstractText, got[0].Abstract)
	assert.Equal(t, model.SourceConsensus, got[0].Source)
}

func TestParseScreenDesign(t *testing.T) {
	tests := []struct {
		in      string
		want    ScreenDesign
		wantErr bool
	}{
		{guessRCT, ScreenRCT, false},
		{" RCT ", ScreenRCT, false},
		{"other_primary", ScreenOtherPrimary, false},
		{"Other primary", ScreenOtherPrimary, false},
		{"other-primary", ScreenOtherPrimary, false},
		{"review", ScreenReview, false},
		{guessOther, ScreenOther, false},
		{"", "", true},
		{"cohort", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseScreenDesign(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewCandidateKeepsASnippet(t *testing.T) {
	long := make([]rune, 2*snippetChars)
	for i := range long {
		long[i] = 'x'
	}

	c := newCandidate(model.Paper{
		DOI: "https://doi.org/10.1/A", Abstract: string(long), Source: model.SourceConsensus,
	}, FoundBy{Source: model.SourceConsensus, Query: 2, Rank: 7})
	assert.Equal(t, "10.1/a", c.DOI, "the DOI is normalized")
	assert.Len(t, []rune(c.Snippet), snippetChars+1, "the snippet is capped (plus the ellipsis)")

	c = newCandidate(model.Paper{Takeaway: "short takeaway", Abstract: abstractText},
		FoundBy{Source: model.SourceConsensus, Query: 1, Rank: 1})
	assert.Equal(t, "short takeaway", c.Snippet, "the takeaway is shorter than the abstract: it goes first")
}
