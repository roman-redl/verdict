package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

func TestParsePubMedMode(t *testing.T) {
	tests := []struct {
		in      string
		want    PubMedMode
		wantErr bool
	}{
		{in: "", want: ""},
		{in: " Auto ", want: PubMedAuto},
		{in: "on", want: PubMedOn},
		{in: "OFF", want: PubMedOff},
		{in: "sometimes", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParsePubMedMode(tt.in)
			if tt.wantErr {
				var ue *UserError

				require.ErrorAs(t, err, &ue)
				assert.Contains(t, ue.Msg, "auto | on | off")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// The defaults are Consensus and the review references with PubMed on auto; a
// channel list is literal, and the PubMed mode overrides it.
func TestChannelPlan(t *testing.T) {
	consPubMed := DiscoverySources{model.SourceConsensus, model.SourcePubMed}
	consOnly := DiscoverySources{model.SourceConsensus}

	tests := []struct {
		name        string
		set         Settings
		opts        Options
		wantSources DiscoverySources
		wantPubMed  PubMedMode
	}{
		{name: "defaults", wantSources: defaultChannels, wantPubMed: PubMedAuto},
		{
			name: "configured mode", set: Settings{PubMed: PubMedOff},
			wantSources: defaultChannels, wantPubMed: PubMedOff,
		},
		{
			name: "flag mode beats the configured one", set: Settings{PubMed: PubMedOff},
			opts: Options{PubMed: PubMedOn}, wantSources: defaultChannels, wantPubMed: PubMedOn,
		},
		{
			name: "a list with pubmed forces it on", opts: Options{Sources: consPubMed},
			wantSources: consOnly, wantPubMed: PubMedOn,
		},
		{
			name: "a list without pubmed keeps it off", opts: Options{Sources: consOnly},
			wantSources: consOnly, wantPubMed: PubMedOff,
		},
		{
			name: "the configured list is literal too", set: Settings{DiscoverySources: consOnly},
			wantSources: consOnly, wantPubMed: PubMedOff,
		},
		{
			name: "the flag list beats the configured one", set: Settings{DiscoverySources: consOnly},
			opts: Options{Sources: consPubMed}, wantSources: consOnly, wantPubMed: PubMedOn,
		},
		{
			name: "off overrides a listed pubmed", opts: Options{Sources: consPubMed, PubMed: PubMedOff},
			wantSources: consOnly, wantPubMed: PubMedOff,
		},
		{
			name: "auto over a list without pubmed", opts: Options{Sources: consOnly, PubMed: PubMedAuto},
			wantSources: consOnly, wantPubMed: PubMedAuto,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTest(t, tt.set, Clients{}, quietLog())

			got := p.channelPlan(tt.opts)

			assert.Equal(t, tt.wantSources, got.sources)
			assert.Equal(t, tt.wantPubMed, got.pubmed)
		})
	}
}

func TestChannelsRunKeepsTheCanonicalOrder(t *testing.T) {
	assert.Equal(t, channelOrder, channelsRun(defaultChannels, true))
	assert.Equal(t, defaultChannels, channelsRun(defaultChannels, false))
	assert.Equal(t, DiscoverySources{model.SourcePubMed}, channelsRun(nil, true))
}

// Auto turns PubMed on below two reviews in the Consensus pool; on and off
// ignore the count. The decision and the count are recorded and logged.
func TestDecidePubMed(t *testing.T) {
	review := func(doi string) Candidate { return cons(doi, 1, 1, tagSystematicReview) }
	trial := func(doi string) Candidate { return cons(doi, 1, 2, discTagRCT) }
	// a review by title only: the tag is empty, the resolver reads the title
	titled := newCandidate(model.Paper{DOI: "10.1/t", Title: "A systematic review and meta-analysis of X"},
		FoundBy{Source: model.SourceConsensus, Query: 1, Rank: 3})

	tests := []struct {
		name        string
		mode        PubMedMode
		pool        []Candidate
		wantReviews int
		want        bool
	}{
		{"auto, empty pool", PubMedAuto, nil, 0, true},
		{"auto, no reviews", PubMedAuto, []Candidate{trial("10.1/a")}, 0, true},
		{"auto, one review", PubMedAuto, []Candidate{review("10.1/r1"), trial("10.1/a")}, 1, true},
		{"auto, two reviews", PubMedAuto, []Candidate{review("10.1/r1"), review("10.1/r2")}, 2, false},
		{"auto, a review by title counts", PubMedAuto, []Candidate{review("10.1/r1"), titled}, 2, false},
		{"auto, a review without a DOI does not count", PubMedAuto, []Candidate{review("10.1/r1"), review("")}, 1, true},
		{"on with many reviews", PubMedOn, []Candidate{review("a/1"), review("a/2"), review("a/3")}, 3, true},
		{"off with no reviews", PubMedOff, nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, buf := logBuffer()
			p := newTest(t, Settings{}, Clients{}, log)
			info := &DiscoveryInfo{}

			assert.Equal(t, tt.want, p.decidePubMed(info, tt.mode, tt.pool))
			assert.Equal(t, &PubMedDecision{Mode: tt.mode, ConsensusReviews: tt.wantReviews, Enabled: tt.want},
				info.PubMedDecision)
			assert.Contains(t, buf.String(), "PubMed channel decided")
		})
	}
}

// Through the whole discovery stage: PubMed is searched only when the decision
// says so, and the artifact carries the decision.
func TestDiscoverPubMedAuto(t *testing.T) {
	reviewHit := func(doi string) model.Paper {
		h := hit(doi, "Review "+doi)
		h.StudyDesign = tagSystematicReview

		return h
	}

	tests := []struct {
		name         string
		consensus    []model.Paper
		mode         PubMedMode
		wantPubMed   bool
		wantReviews  int
		wantSources  []model.Source
		wantDecision PubMedMode
	}{
		{
			name: "no reviews turns PubMed on", consensus: []model.Paper{hit(pmConsDOI, "C1")},
			wantPubMed: true, wantSources: []model.Source{model.SourceConsensus, model.SourcePubMed, model.SourceReviewRefs},
			wantDecision: PubMedAuto,
		},
		{
			name: "one review turns PubMed on", consensus: []model.Paper{reviewHit("10.7/r1"), hit(pmConsDOI, "C1")},
			wantPubMed: true, wantReviews: 1,
			wantSources:  []model.Source{model.SourceConsensus, model.SourcePubMed, model.SourceReviewRefs},
			wantDecision: PubMedAuto,
		},
		{
			name: "two reviews keep PubMed off", consensus: []model.Paper{reviewHit("10.7/r1"), reviewHit("10.7/r2")},
			wantReviews: 2, wantSources: []model.Source{model.SourceConsensus, model.SourceReviewRefs},
			wantDecision: PubMedAuto,
		},
		{
			name: "on overrides two reviews", consensus: []model.Paper{reviewHit("10.7/r1"), reviewHit("10.7/r2")},
			mode: PubMedOn, wantPubMed: true, wantReviews: 2,
			wantSources:  []model.Source{model.SourceConsensus, model.SourcePubMed, model.SourceReviewRefs},
			wantDecision: PubMedOn,
		},
		{
			name: "off overrides no reviews", consensus: []model.Paper{hit(pmConsDOI, "C1")},
			mode: PubMedOff, wantSources: []model.Source{model.SourceConsensus, model.SourceReviewRefs},
			wantDecision: PubMedOff,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
				return pageOf(true, tt.consensus...), nil
			}}
			pm := &fakePubMed{
				hits:    func(string) model.PubMedHits { return model.PubMedHits{PMIDs: []string{"1"}, Total: 1} },
				records: map[string]model.PubMedRecord{"1": pmRec("1", pmTrialDOI, "P", pmTypeRCT)},
			}
			p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10},
				Clients{Search: search, PubMed: pm, Works: reviewWorks(nil)}, quietLog())

			st := &State{}
			require.NoError(t, p.discoverWith(t.Context(), st,
				Options{Question: "q", PubMed: tt.mode, PubMedQuery: pmGERD, NoScreen: true}))

			assert.Equal(t, tt.wantPubMed, pm.calls.total() > 0, "PubMed calls")
			assert.Equal(t, tt.wantSources, st.Discovery.Sources)
			assert.Equal(t, &PubMedDecision{
				Mode: tt.wantDecision, ConsensusReviews: tt.wantReviews, Enabled: tt.wantPubMed,
			}, st.Discovery.PubMedDecision)
			assert.Equal(t, tt.wantPubMed, st.Discovery.PubMed != nil, "the PubMed record exists only when it ran")
		})
	}
}
