package eval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

const (
	doiA     = "10.7/a"
	doiB     = "10.7/b"
	doiC     = "10.7/c"
	doiOther = "10.9/x"

	claimAHigher = "A is higher."
)

// cand is a pool candidate: found by src, screened in or out (nil = unscreened).
func cand(doi string, src model.Source, include *bool) pipeline.Candidate {
	c := pipeline.Candidate{DOI: doi, Sources: []pipeline.FoundBy{{Source: src}}}
	if include != nil {
		c.Screen = &pipeline.ScreenDecision{Include: *include}
	}

	return c
}

func paper(doi string) model.Paper { return model.Paper{DOI: doi} }

func TestRecallOf(t *testing.T) {
	// the DOIs are written in the forms a set may hold: case, URL prefix, a
	// repeat and a study without a DOI; the question has three distinct studies
	q := &Question{Studies: []Study{
		{DOI: "10.7/A"}, {DOI: "https://doi.org/" + doiB}, {DOI: ""}, {DOI: doiA}, {DOI: "doi:" + doiC},
	}}

	tests := []struct {
		name string
		q    *Question
		st   pipeline.State
		want Recall
	}{
		{name: "no studies", q: &Question{}, st: pipeline.State{}, want: Recall{}},
		{
			name: "no discovery and no papers",
			q:    q, st: pipeline.State{}, want: Recall{Total: 3},
		},
		{
			name: "none of the studies found",
			q:    q,
			st: pipeline.State{
				Discovery: &pipeline.DiscoveryInfo{Pool: []pipeline.Candidate{
					cand(doiOther, model.SourceConsensus, new(true)),
				}},
				Papers: []model.Paper{paper(doiOther)},
			},
			want: Recall{Total: 3},
		},
		{
			name: "every stage, DOIs written in other forms",
			q:    q,
			st: pipeline.State{
				Discovery: &pipeline.DiscoveryInfo{Pool: []pipeline.Candidate{
					cand("HTTPS://DOI.ORG/10.7/A", model.SourceConsensus, new(true)),
					cand("10.7/B", model.SourceReviewRefs, new(false)),
					cand(doiC, model.SourceReviewRefs, new(true)),
					cand(doiOther, model.SourceConsensus, new(true)),
				}},
				Papers: []model.Paper{paper(doiA), paper("https://doi.org/10.7/C"), paper(doiOther)},
			},
			want: Recall{Total: 3, Pool: 3, Screened: 2, Corpus: 2},
		},
		{
			name: "unscreened Consensus hit stays in, unscreened PubMed hit does not",
			q:    q,
			st: pipeline.State{
				Discovery: &pipeline.DiscoveryInfo{Pool: []pipeline.Candidate{
					cand(doiA, model.SourceConsensus, nil),
					cand(doiB, model.SourcePubMed, nil),
				}},
			},
			want: Recall{Total: 3, Pool: 2, Screened: 1},
		},
		{
			name: "a study twice in the pool and the corpus counts once",
			q:    q,
			st: pipeline.State{
				Discovery: &pipeline.DiscoveryInfo{Pool: []pipeline.Candidate{
					cand(doiA, model.SourceConsensus, new(true)),
					cand(doiA, model.SourcePubMed, new(true)),
				}},
				Papers: []model.Paper{paper(doiA), paper(doiA)},
			},
			want: Recall{Total: 3, Pool: 1, Screened: 1, Corpus: 1},
		},
		{
			name: "corpus without a discovery pool",
			q:    q,
			st:   pipeline.State{Papers: []model.Paper{paper(doiB)}},
			want: Recall{Total: 3, Corpus: 1},
		},
		{
			name: "a pool candidate without a DOI is not a study",
			q:    q,
			st: pipeline.State{Discovery: &pipeline.DiscoveryInfo{Pool: []pipeline.Candidate{
				cand("", model.SourcePubMed, new(true)),
			}}},
			want: Recall{Total: 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, recallOf(tt.q, &tt.st))
		})
	}
}

func TestRecallCellsAndAdd(t *testing.T) {
	r := Recall{Total: 4, Pool: 3, Screened: 2, Corpus: 1}
	assert.Equal(t, []string{"3/4", "2/4", "1/4"}, r.cells())

	sum := Recall{}
	sum.add(r)
	sum.add(Recall{Total: 2, Pool: 2, Screened: 2, Corpus: 2})
	assert.Equal(t, Recall{Total: 6, Pool: 5, Screened: 4, Corpus: 3}, sum)
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{in: "dois", want: ModeDOIs},
		{in: "search\n", want: ModeSearch},
		{in: " search ", want: ModeSearch},
		{in: "", wantErr: true},
		{in: "Search", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseMode(tt.in)
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSearchQueries(t *testing.T) {
	tests := []struct {
		name string
		q    Question
		want []string
	}{
		{
			name: "question and claim", q: Question{Question: " Is A higher? ", Claim: claimAHigher},
			want: []string{"Is A higher?", claimAHigher},
		},
		{
			name: "the same text twice", q: Question{Question: claimAHigher, Claim: claimAHigher},
			want: []string{claimAHigher},
		},
		{name: "no question text", q: Question{Claim: claimAHigher}, want: []string{claimAHigher}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, searchQueries(&tt.q))
		})
	}
}
