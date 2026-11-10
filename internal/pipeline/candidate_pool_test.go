package pipeline

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

func TestParseDiscoverySources(t *testing.T) {
	tests := []struct {
		in      string
		want    DiscoverySources
		wantErr bool
	}{
		{"", nil, false},
		{"  ", nil, false},
		{"consensus", DiscoverySources{model.SourceConsensus}, false},
		{"pubmed,consensus", DiscoverySources{model.SourceConsensus, model.SourcePubMed}, false},
		{" Review_Refs , pubmed ", DiscoverySources{model.SourcePubMed, model.SourceReviewRefs}, false},
		{"consensus,pubmed,review_refs,consensus", channelOrder, false},
		{"openalex", nil, true},
		{"consensus,,pubmed", nil, true},
		{"manual", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseDiscoverySources(tt.in)
			if tt.wantErr {
				var ue *UserError

				require.ErrorAs(t, err, &ue)
				assert.Contains(t, ue.Msg, "unknown discovery source")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got, "in the canonical order, once each")
		})
	}
}

func TestDiscoverySourcesHas(t *testing.T) {
	d := DiscoverySources{model.SourcePubMed}

	assert.True(t, d.Has(model.SourcePubMed))
	assert.False(t, d.Has(model.SourceConsensus))
}

func TestDiscoverySourcesResolution(t *testing.T) {
	tests := []struct {
		name string
		set  DiscoverySources
		opts DiscoverySources
		want DiscoverySources
	}{
		{"all by default", nil, nil, channelOrder},
		{"the settings", DiscoverySources{model.SourcePubMed}, nil, DiscoverySources{model.SourcePubMed}},
		{
			"the run beats the settings",
			DiscoverySources{model.SourcePubMed},
			DiscoverySources{model.SourceConsensus},
			DiscoverySources{model.SourceConsensus},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTest(t, Settings{DiscoverySources: tt.set}, Clients{}, quietLog())
			assert.Equal(t, tt.want, p.discoverySources(Options{Sources: tt.opts}))
		})
	}
}

func TestCandidatePoolMergesSources(t *testing.T) {
	cp := newCandidatePool(nil)

	cp.add(cons("10.1/a", 1, 5, ""))
	cp.add(cons("10.1/a", 1, 3, "rct")) // the same query again, a better rank
	cp.add(cons("10.1/a", 2, 9, ""))

	byPubMed := pub("10.1/a", SearchRCTFilter, 4, "")
	byPubMed.PMID, byPubMed.Year = "77", 2019
	cp.add(byPubMed)
	cp.add(pub("", SearchReviews, 1, "")) // no DOI and no PMID: nothing to key it by

	noDOI := pub("", SearchReviews, 2, "")
	noDOI.PMID = "55"
	cp.add(noDOI)
	cp.add(noDOI) // the same record again

	require.Len(t, cp.list, 2)

	a := cp.list[0]
	assert.Equal(t, []FoundBy{
		{Source: model.SourceConsensus, Query: 1, Rank: 3},
		{Source: model.SourceConsensus, Query: 2, Rank: 9},
		{Source: model.SourcePubMed, Search: SearchRCTFilter, Rank: 4},
	}, a.Sources, "one entry per channel and query, the better rank of repeats")
	assert.Equal(t, "rct", a.Design, "a blank is filled by a later finding")
	assert.Equal(t, "77", a.PMID)
	assert.Equal(t, 2019, a.Year)
	assert.Equal(t, model.SourceConsensus, a.paper.Source, "the first record stays the corpus paper")
	assert.Equal(t, 2, a.channels())

	assert.Equal(t, "55", cp.list[1].PMID)
	assert.Len(t, cp.list[1].Sources, 1)
}

func TestFoundByHelpers(t *testing.T) {
	c := alsoBy(alsoBy(cons("10.1/a", 2, 7, ""), FoundBy{Source: model.SourcePubMed, Search: SearchReviews, Rank: 4}),
		FoundBy{Source: model.SourceReviewRefs, CitingReviews: 2})
	c.addSource(FoundBy{Source: model.SourceReviewRefs, CitingReviews: 1})

	assert.True(t, c.foundBy(model.SourceReviewRefs))
	assert.False(t, c.foundBy(model.SourceManual))
	assert.Equal(t, 3, c.channels())
	assert.Equal(t, 3, c.citingReviews(), "citing reviews add up")

	rank, query, ok := c.bestRank()
	assert.True(t, ok)
	assert.Equal(t, 4, rank, "the best rank over the channels")
	assert.Equal(t, math.MaxInt, query, "a PubMed finding sorts after any Consensus query")

	m := mine("10.1/m", "T", 3, nil)
	_, _, ok = m.bestRank()
	assert.False(t, ok, "references alone have no rank")
}

// An artifact written before the sources list existed still loads: its single
// source, query, rank and citing count become entries of Sources.
func TestCandidateLoadsTheOldFormat(t *testing.T) {
	tests := []struct {
		name string
		json string
		want []FoundBy
	}{
		{
			name: "a Consensus hit",
			json: `{"doi":"10.1/a","title":"A","source":"consensus","query":2,"rank":5,"selected":true}`,
			want: []FoundBy{{Source: model.SourceConsensus, Query: 2, Rank: 5}},
		},
		{
			name: "a mined reference",
			json: `{"doi":"10.1/b","title":"B","source":"review_refs","cited_by_reviews":4,"selected":false}`,
			want: []FoundBy{{Source: model.SourceReviewRefs, CitingReviews: 4}},
		},
		{
			name: "a Consensus hit cited by reviews",
			json: `{"doi":"10.1/c","title":"C","source":"consensus","query":1,"rank":1,"cited_by_reviews":2,"selected":true}`,
			want: []FoundBy{
				{Source: model.SourceConsensus, Query: 1, Rank: 1},
				{Source: model.SourceReviewRefs, CitingReviews: 2},
			},
		},
		{
			name: "the new format is read as it is",
			json: `{"doi":"10.1/d","title":"D","sources":[{"source":"pubmed","search":"reviews","rank":3}],"selected":false}`,
			want: []FoundBy{{Source: model.SourcePubMed, Search: SearchReviews, Rank: 3}},
		},
		{name: "nothing known", json: `{"doi":"10.1/e","title":"E","selected":false}`, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c Candidate

			require.NoError(t, json.Unmarshal([]byte(tt.json), &c))
			assert.Equal(t, tt.want, c.Sources)
			assert.NotEmpty(t, c.DOI)
			assert.NotEmpty(t, c.Title)
		})
	}
}

func TestCandidateJSONRoundTrip(t *testing.T) {
	c := alsoBy(cons("10.1/a", 1, 2, "rct"), FoundBy{Source: model.SourcePubMed, Search: SearchRCTFilter, Rank: 1})
	c.PMID = "123"
	c.Screen = includeAs(ScreenRCT)

	raw, err := json.Marshal(c)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "cited_by_reviews", "the old flat fields are gone")
	assert.Contains(t, string(raw), `"sources":[{"source":"consensus","query":1,"rank":2},`+
		`{"source":"pubmed","search":"rct_filter","rank":1}]`)

	var back Candidate

	require.NoError(t, json.Unmarshal(raw, &back))
	c.paper = back.paper // the full record is not part of the artifact

	assert.Equal(t, c, back)
}
