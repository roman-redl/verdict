package pipeline

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// Fixture values of the PubMed discovery tests.
const (
	pmTrialDOI   = "10.7/trial-a"
	pmTrial2DOI  = "10.7/trial-b"
	pmReviewDOI  = "10.7/review"
	pmConsDOI    = "10.7/c1"
	pmTagMeta    = discTagMeta
	pmTagRCT     = discTagRCT
	pmTypeRCT    = "Randomized Controlled Trial"
	pmTypeMeta   = "Meta-Analysis"
	pmTypeClin   = "Clinical Trial"
	pmTypeSR     = "Systematic Review"
	pmTypeReview = "Review"
	pmGERD       = "GERD[tiab]"
	pmFundo      = "fundoplicat*[tiab]"
	pmOmep       = "omeprazole[tiab]"
	pmNoTerms    = "no population or intervention terms"
	pmTopic      = `("Gastroesophageal Reflux"[Mesh] OR GERD[tiab]) AND (fundoplicat*[tiab])`
	pmComparator = `("Proton Pump Inhibitors"[Mesh] OR omeprazole[tiab])`
)

// writerReply is the query writer's answer: a population, an intervention, a
// comparator and an outcome (which the program must drop).
const writerReply = `{"concepts":[` +
	`{"name":"population","terms":["\"Gastroesophageal Reflux\"[Mesh]","GERD[tiab]"]},` +
	`{"name":"intervention","terms":["fundoplicat*[tiab]"]},` +
	`{"name":"comparator","terms":["\"Proton Pump Inhibitors\"[Mesh]","omeprazole[tiab]"]},` +
	`{"name":"outcome","terms":["pain[tiab]"]}],` +
	`"query":"the model's own join, never sent"}`

// planFake is the plan-role model: it tells the query writer from the
// screening by the system prompt, counts both and answers each from a script.
type planFake struct {
	mu          sync.Mutex
	writerCalls int
	screenCalls int
	writer      string
	writerUser  string
	screen      func(call int, user string) (string, error)
}

var _ Completer = (*planFake)(nil)

func (f *planFake) Name() string { return "plan-fake" }

func (f *planFake) Complete(_ context.Context, system, user string, _ CompleteOptions) (string, Usage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if system == pubmedQuerySystemPrompt {
		f.writerCalls++
		f.writerUser = user

		return f.writer, Usage{InputTokens: 30, OutputTokens: 20}, nil
	}

	f.screenCalls++

	text, err := f.screen(f.screenCalls, user)

	return text, Usage{InputTokens: 10, OutputTokens: 5}, err
}

// includeAll is a screening that includes every candidate and trusts its tag.
func includeAll(t *testing.T) func(int, string) (string, error) {
	t.Helper()

	return answerBy(t, func(screenItem) (bool, string) { return true, guessRCT })
}

func pmRec(pmid, doi, title string, types ...string) model.PubMedRecord {
	return model.PubMedRecord{PMID: pmid, DOI: doi, Title: title, Year: 2020, PubTypes: types}
}

// pubmedOnly runs discovery over the PubMed channel alone.
func pubmedOnly() Options {
	return Options{Question: "does fundoplication beat omeprazole?", Sources: DiscoverySources{model.SourcePubMed}}
}

// topicWith is the topic query of writerReply, the comparator in or out.
func topicWith(comparator bool) string {
	if comparator {
		return pmTopic + " AND " + pmComparator
	}

	return pmTopic
}

func TestDiscoverPubMedPoolsBothSearches(t *testing.T) {
	pm := &fakePubMed{
		hits: func(q string) model.PubMedHits {
			if strings.Contains(q, "systematic[sb]") {
				return model.PubMedHits{PMIDs: []string{"9", "1"}, Total: 120}
			}

			return model.PubMedHits{PMIDs: []string{"1", "2", "3"}, Total: 1533}
		},
		records: map[string]model.PubMedRecord{
			"1": pmRec("1", pmTrialDOI, "Fundoplication versus omeprazole", pmTypeRCT, "Journal Article"),
			"2": pmRec("2", pmTrial2DOI, "A phase II study", pmTypeClin),
			"3": pmRec("3", "", "A trial without a DOI", pmTypeRCT),
			"9": pmRec("9", pmReviewDOI, "Surgery for reflux", pmTypeMeta, pmTypeRCT),
		},
	}
	plan := &planFake{writer: writerReply, screen: includeAll(t)}
	p := newTest(t, Settings{MaxPapers: 10, LLMConcurrency: 1}, Clients{PubMed: pm, LLMPlan: plan}, quietLog())

	st := &State{Question: "does fundoplication beat omeprazole?"}
	require.NoError(t, p.discoverWith(t.Context(), st, pubmedOnly()))

	// the two searches are composed in Go from the concepts; the outcome and the model's own join are gone
	require.Equal(t, []string{
		"(" + topicWith(true) + ") AND (" + cochraneHSSS + ")",
		"(" + topicWith(true) + ") AND (" + reviewsFilter + ")",
	}, pm.queries)
	assert.Equal(t, []int{300, 50}, pm.limits, "300 trials, 50 reviews")
	assert.NotContains(t, strings.Join(pm.queries, "\n"), "pain")

	d := st.Discovery
	require.NotNil(t, d.PubMed)
	assert.Equal(t, []model.Source{model.SourcePubMed}, d.Sources)
	assert.Equal(t, topicWith(true), d.PubMed.TopicQuery)
	assert.Equal(t, "the model's own join, never sent", d.PubMed.ModelQuery, "kept for the log only")
	assert.Equal(t, []PubMedSearchInfo{
		{Search: SearchRCTFilter, Query: pm.queries[0], Total: 1533, Returned: 3, NoDOI: 1},
		{Search: SearchReviews, Query: pm.queries[1], Total: 120, Returned: 2},
	}, d.PubMed.Searches)
	assert.Equal(t, 1, d.PubMed.NoDOI)

	byPMID := map[string]Candidate{}
	for _, c := range d.Pool {
		byPMID[c.PMID] = c
	}

	require.Len(t, d.Pool, 4, "the DOI-less record is in the pool for the audit")
	assert.Equal(t, []FoundBy{
		{Source: model.SourcePubMed, Search: SearchRCTFilter, Rank: 1},
		{Source: model.SourcePubMed, Search: SearchReviews, Rank: 2},
	}, byPMID["1"].Sources, "found by both searches: both kept, each with its rank")
	assert.Equal(t, pmTagRCT, byPMID["1"].Design)
	assert.Equal(t, pmTagRCT, byPMID["2"].Design, "a clinical trial counts as a trial")
	assert.Equal(t, pmTagMeta, byPMID["9"].Design, "a meta-analysis of trials is a review")
	assert.Empty(t, byPMID["3"].DOI)
	assert.False(t, byPMID["3"].Selected, "no DOI, not selectable")
	assert.Nil(t, byPMID["3"].Screen, "and not worth a screening call")

	assert.Equal(t, []string{pmTrialDOI, pmTrial2DOI, pmReviewDOI}, dois(st.Papers))
	assert.Equal(t, model.SourcePubMed, st.Papers[0].Source)
	assert.Contains(t, strings.Join(st.Warnings, "\n"), "1 record(s) without a DOI")

	assert.Equal(t, 1, plan.writerCalls, "one query-writing call per run")
	assert.Contains(t, plan.writerUser, "does fundoplication beat omeprazole?")
	require.NotNil(t, st.Cost)
	assert.Equal(t, 1, st.Cost.QueryWriting.Calls, "the writer's cost is in the discovery cost")
	assert.Equal(t, 30, st.Cost.QueryWriting.InputTokens)
	assert.Equal(t, 1, st.Cost.Screening.Calls)
}

// An empty trial search is retried once without the comparator concept, and
// the broader topic serves the review search too.
func TestDiscoverPubMedRetriesWithoutTheComparator(t *testing.T) {
	pm := &fakePubMed{
		hits: func(q string) model.PubMedHits {
			if strings.Contains(q, "omeprazole") && strings.Contains(q, "randomized controlled trial[pt]") {
				return model.PubMedHits{} // too narrow
			}

			return model.PubMedHits{PMIDs: []string{"1"}, Total: 1}
		},
		records: map[string]model.PubMedRecord{"1": pmRec("1", pmTrialDOI, "T", pmTypeRCT)},
	}
	plan := &planFake{writer: writerReply, screen: includeAll(t)}
	p := newTest(t, Settings{MaxPapers: 10}, Clients{PubMed: pm, LLMPlan: plan}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, pubmedOnly()))

	require.Len(t, pm.queries, 3)
	assert.Contains(t, pm.queries[0], "omeprazole")
	assert.NotContains(t, pm.queries[1], "omeprazole", "the retry drops the comparator")
	assert.Contains(t, pm.queries[1], "randomized controlled trial[pt]")
	assert.NotContains(t, pm.queries[2], "omeprazole", "the review search uses the broader topic")

	searches := st.Discovery.PubMed.Searches
	require.Len(t, searches, 3)
	assert.Zero(t, searches[0].Total)
	assert.True(t, searches[1].ComparatorDropped)
	assert.Equal(t, 1, searches[1].Returned)
	assert.Equal(t, []string{pmTrialDOI}, dois(st.Papers))
}

// Without a comparator in the query there is nothing to drop: an empty trial
// search stays empty and is not repeated.
func TestDiscoverPubMedNoRetryWithoutAComparator(t *testing.T) {
	pm := &fakePubMed{}
	plan := &planFake{
		writer: `{"concepts":[{"name":"population","terms":["GERD[tiab]"]},` +
			`{"name":"intervention","terms":["fundoplicat*[tiab]"]}]}`,
		screen: includeAll(t),
	}
	p := newTest(t, Settings{MaxPapers: 10}, Clients{PubMed: pm, LLMPlan: plan}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, pubmedOnly()))

	assert.Len(t, pm.queries, 2, "the trial search and the review search")
	assert.Empty(t, st.Papers)
	assert.Contains(t, strings.Join(st.Warnings, "\n"), "no papers")
}

// A ready query skips the writer; with --no-screen the stage spends no LLM
// call at all.
func TestDiscoverPubMedSuppliedQueryAndNoScreenSpendNoLLM(t *testing.T) {
	pm := &fakePubMed{
		hits: func(string) model.PubMedHits { return model.PubMedHits{PMIDs: []string{"1", "2"}, Total: 2} },
		records: map[string]model.PubMedRecord{
			"1": pmRec("1", pmTrialDOI, "A", pmTypeRCT), "2": pmRec("2", pmTrial2DOI, "B"),
		},
	}
	plan := &scriptCompleter{}
	p := newTest(t, Settings{MaxPapers: 10}, Clients{PubMed: pm, LLMPlan: plan}, quietLog())

	opts := pubmedOnly()
	opts.PubMedQuery = `(GERD[tiab]) AND (fundoplicat*[tiab])`
	opts.NoScreen = true

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, opts))

	assert.Zero(t, plan.calls, "no query writing, no screening")
	assert.Nil(t, st.Cost)
	assert.Equal(t, "("+opts.PubMedQuery+") AND ("+cochraneHSSS+")", pm.queries[0])
	assert.True(t, st.Discovery.PubMed.Supplied)
	assert.True(t, st.Discovery.NoScreen)
	assert.Equal(t, []string{pmTrialDOI, pmTrial2DOI}, dois(st.Papers), "every candidate counts as included")

	for _, c := range st.Discovery.Pool {
		require.NotNil(t, c.Screen)
		assert.True(t, c.Screen.Include)
		assert.Equal(t, noScreenReason, c.Screen.Reason)
	}
}

// The no-screen switch also comes from the settings (NO_SCREEN).
func TestDiscoverNoScreenFromSettings(t *testing.T) {
	pm := &fakePubMed{
		hits:    func(string) model.PubMedHits { return model.PubMedHits{PMIDs: []string{"1"}, Total: 1} },
		records: map[string]model.PubMedRecord{"1": pmRec("1", pmTrialDOI, "A", pmTypeRCT)},
	}
	plan := &scriptCompleter{}
	p := newTest(t, Settings{MaxPapers: 10, NoScreen: true}, Clients{PubMed: pm, LLMPlan: plan}, quietLog())

	opts := pubmedOnly()
	opts.PubMedQuery = pmGERD

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, opts))
	assert.Zero(t, plan.calls)
	assert.Len(t, st.Papers, 1)
}

func TestDiscoverPubMedRejectsAnUnbalancedSuppliedQuery(t *testing.T) {
	p := newTest(t, Settings{}, Clients{PubMed: &fakePubMed{}}, quietLog())

	opts := pubmedOnly()
	opts.PubMedQuery = "((GERD[tiab])"

	var ue *UserError

	require.ErrorAs(t, p.discoverWith(t.Context(), &State{}, opts), &ue)
	assert.Contains(t, ue.Msg, "--pubmed-query")
}

// A paper several channels found keeps all its sources; the corpus order uses
// the best rank over the channels, ties go to the paper more channels found.
func TestDiscoverKeepsEverySourceAndOrdersByTheBestRank(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		return pageOf(true, hit(pmConsDOI, "C1"), hit("10.7/c2", "C2"), hit(pmTrialDOI, "Both")), nil
	}}
	pm := &fakePubMed{
		hits: func(q string) model.PubMedHits {
			if strings.Contains(q, "systematic[sb]") {
				return model.PubMedHits{}
			}

			return model.PubMedHits{PMIDs: []string{"1", "2"}, Total: 2}
		},
		records: map[string]model.PubMedRecord{
			"1": pmRec("1", "10.7/p1", "P1", pmTypeRCT), "2": pmRec("2", pmTrialDOI, "Both", pmTypeRCT),
		},
	}
	works := reviewWorks(nil)
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10},
		Clients{Search: search, PubMed: pm, Works: works}, quietLog())

	opts := Options{Question: "q", PubMedQuery: pmGERD, NoScreen: true}
	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, opts))

	assert.Equal(t, []model.Source(channelOrder), st.Discovery.Sources)

	byDOI := map[string]Candidate{}
	for _, c := range st.Discovery.Pool {
		byDOI[c.DOI] = c
	}

	require.Len(t, byDOI, 4, "the shared paper is one candidate")
	assert.Equal(t, []FoundBy{
		{Source: model.SourceConsensus, Query: 1, Rank: 3},
		{Source: model.SourcePubMed, Search: SearchRCTFilter, Rank: 2},
	}, byDOI[pmTrialDOI].Sources)
	assert.Equal(t, "2", byDOI[pmTrialDOI].PMID, "the PMID joins the Consensus record")
	assert.Equal(t, model.SourceConsensus, byDOI[pmTrialDOI].paper.Source, "the richer first record is the corpus paper")

	// best ranks: p1 = 1 (PubMed), c1 = 1 (Consensus), shared = 2 (two channels), c2 = 2
	// at rank 1 the channel count ties, the Consensus query number breaks it; at rank 2 two channels win
	assert.Equal(t, []string{pmConsDOI, "10.7/p1", pmTrialDOI, "10.7/c2"}, dois(st.Papers))
}

func TestDiscoverPubMedFailureIsAWarningWhenOtherChannelsDeliver(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		return pageOf(true, hit(pmConsDOI, "C1")), nil
	}}
	pm := &fakePubMed{err: errors.New("HTTP 500 from pubmed")}
	log, buf := logBuffer()
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10}, Clients{Search: search, PubMed: pm}, log)

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{Question: "q", PubMedQuery: pmGERD}))

	assert.Equal(t, []string{pmConsDOI}, dois(st.Papers), "the Consensus candidates are kept")
	assert.Contains(t, st.Discovery.PubMed.Error, "HTTP 500")
	assert.Contains(t, strings.Join(st.Warnings, "\n"), "pubmed: discovery failed")
	assert.Contains(t, buf.String(), "PubMed discovery failed")
}

// With PubMed as the only search channel its failure is the run's failure.
func TestDiscoverPubMedFailureFailsAPubMedOnlyRun(t *testing.T) {
	pm := &fakePubMed{err: errors.New("HTTP 500 from pubmed")}
	p := newTest(t, Settings{}, Clients{PubMed: pm}, quietLog())

	opts := pubmedOnly()
	opts.PubMedQuery = pmGERD

	require.ErrorContains(t, p.discoverWith(t.Context(), &State{}, opts), "HTTP 500")
}

// A model that cannot write a usable query costs the channel, not the run.
func TestDiscoverPubMedBadWriterAnswer(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		return pageOf(true, hit(pmConsDOI, "C1")), nil
	}}
	pm := &fakePubMed{}
	plan := &planFake{writer: `{"concepts":[{"name":"outcome","terms":["pain[tiab]"]}]}`, screen: includeAll(t)}
	clients := Clients{Search: search, PubMed: pm, LLMPlan: plan}
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10}, clients, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{Question: "q"}))

	assert.Zero(t, pm.calls.total(), "no query, no search")
	assert.Equal(t, []string{pmConsDOI}, dois(st.Papers))
	assert.Contains(t, st.Discovery.PubMed.Error, pmNoTerms)
	assert.Equal(t, 1, st.Cost.QueryWriting.Calls, "the call was made and is counted")
}

// --discovery-sources switches the channels off one by one; a run without
// Consensus is mode B even with no Consensus key.
func TestDiscoverySourcesSwitch(t *testing.T) {
	newSearch := func() *fakeSearch {
		return &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
			return pageOf(true, hit(pmConsDOI, "C1")), nil
		}}
	}
	newPubMed := func() *fakePubMed {
		return &fakePubMed{
			hits:    func(string) model.PubMedHits { return model.PubMedHits{PMIDs: []string{"1"}, Total: 1} },
			records: map[string]model.PubMedRecord{"1": pmRec("1", pmTrialDOI, "P", pmTypeRCT)},
		}
	}

	tests := []struct {
		name        string
		sources     DiscoverySources
		wantSearch  int
		wantPubMed  bool
		wantPapers  []string
		wantSources []model.Source
		withoutKey  bool
	}{
		{
			name: "consensus only", sources: DiscoverySources{model.SourceConsensus},
			wantSearch: 1, wantPapers: []string{pmConsDOI}, wantSources: []model.Source{model.SourceConsensus},
		},
		{
			name: "pubmed only", sources: DiscoverySources{model.SourcePubMed},
			wantPubMed: true, wantPapers: []string{pmTrialDOI}, wantSources: []model.Source{model.SourcePubMed},
		},
		{
			name:       "pubmed and review references without a Consensus key",
			sources:    DiscoverySources{model.SourcePubMed, model.SourceReviewRefs},
			wantPubMed: true, wantPapers: []string{pmTrialDOI}, withoutKey: true,
			wantSources: []model.Source{model.SourcePubMed, model.SourceReviewRefs},
		},
		{
			name: "defaults: PubMed auto-on over a pool without reviews", sources: nil, wantSearch: 1, wantPubMed: true,
			wantPapers:  []string{pmConsDOI, pmTrialDOI},
			wantSources: []model.Source{model.SourceConsensus, model.SourcePubMed, model.SourceReviewRefs},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			search, pm := newSearch(), newPubMed()
			clients := Clients{Search: search, PubMed: pm}

			if tt.withoutKey {
				clients.Search = nil
			}

			p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10}, clients, quietLog())

			st := &State{}
			require.NoError(t, p.discoverWith(t.Context(), st,
				Options{Question: "q", Sources: tt.sources, PubMedQuery: pmGERD, NoScreen: true}))

			assert.Equal(t, tt.wantSearch, search.calls.total(), "Consensus calls")
			assert.Equal(t, tt.wantPubMed, pm.calls.total() > 0, "PubMed calls")
			assert.Equal(t, tt.wantPapers, dois(st.Papers))
			assert.Equal(t, EngineConsensus, st.Discovery.Engine)
			assert.Equal(t, tt.wantSources, st.Discovery.Sources)
		})
	}
}

// Consensus wanted (the default) but not configured is a user error that
// names the way out.
func TestDiscoverConsensusNotConfigured(t *testing.T) {
	p := newTest(t, Settings{}, Clients{PubMed: &fakePubMed{}}, quietLog())

	var ue *UserError

	require.ErrorAs(t, p.discoverWith(t.Context(), &State{}, Options{Question: "q", Engine: EngineConsensus}), &ue)
	assert.Contains(t, ue.Msg, "--discovery-sources")
}

// The reviews PubMed found are mined like Consensus's: their references join
// the pool as review_refs candidates.
func TestDiscoverMinesTheReviewsPubMedFound(t *testing.T) {
	pm := &fakePubMed{
		hits: func(q string) model.PubMedHits {
			if strings.Contains(q, "systematic[sb]") {
				return model.PubMedHits{PMIDs: []string{"9"}, Total: 1}
			}

			return model.PubMedHits{}
		},
		records: map[string]model.PubMedRecord{"9": pmRec("9", pmReviewDOI, "Surgery for reflux", pmTypeMeta)},
	}
	works := reviewWorks(map[string][]string{pmReviewDOI: {"W1", "W2"}})
	p := newTest(t, Settings{MaxPapers: 10}, Clients{PubMed: pm, Works: works}, quietLog())

	opts := Options{
		Question: "q", PubMedQuery: pmGERD, NoScreen: true,
		Sources: DiscoverySources{model.SourcePubMed, model.SourceReviewRefs},
	}
	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, opts))

	d := st.Discovery
	assert.Equal(t, 1, d.ReviewsMined)
	assert.Equal(t, 2, d.RefsAdded)
	require.Len(t, d.Pool, 3)
	assert.Equal(t, []FoundBy{{Source: model.SourceReviewRefs, CitingReviews: 1}}, d.Pool[1].Sources)
	assert.Equal(t, []string{"10.5/w1", "10.5/w2", pmReviewDOI}, dois(st.Papers),
		"trials-or-primary first, the review after")
}

// Without screening the references stay out of the corpus (an unscreened
// PubMed-only candidate is not eligible either).
func TestEligibleWithoutScreening(t *testing.T) {
	tests := []struct {
		name string
		c    Candidate
		want bool
	}{
		{"an unscreened Consensus hit", cons("10.1/a", 1, 1, ""), true},
		{"an unscreened PubMed hit", pub("10.1/a", SearchRCTFilter, 1, pmTagRCT), false},
		{"a Consensus hit PubMed also found", alsoBy(cons("10.1/a", 1, 1, ""),
			FoundBy{Source: model.SourcePubMed, Search: SearchRCTFilter, Rank: 1}), true},
		{"an unscreened reference", mine("10.1/a", "T", 2, nil), false},
		{"a hit with no DOI", withScreen(pub("", SearchRCTFilter, 1, pmTagRCT), *includeAs(ScreenRCT)), false},
		{"a screened-in PubMed hit", withScreen(pub("10.1/a", SearchRCTFilter, 1, ""), *includeAs(ScreenRCT)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.c.eligible())
		})
	}
}

func TestDesignTagFromPubTypes(t *testing.T) {
	tests := []struct {
		name  string
		types []string
		want  string
	}{
		{"a randomized trial", []string{"Journal Article", pmTypeRCT}, pmTagRCT},
		{"a clinical trial", []string{pmTypeClin}, pmTagRCT},
		{"a phase III trial", []string{"Clinical Trial, Phase III"}, pmTagRCT},
		{"a trial protocol is no trial", []string{"Clinical Trial Protocol"}, ""},
		{"a meta-analysis beats the trial type", []string{pmTypeRCT, pmTypeMeta}, pmTagMeta},
		{"a systematic review", []string{pmTypeSR, pmTypeReview}, discTagSystematic},
		{"a narrative review says nothing", []string{pmTypeReview}, ""},
		{"a systematic review of trials is a review", []string{pmTypeSR, pmTypeClin}, discTagSystematic},
		{"case-insensitive", []string{"randomized controlled trial"}, pmTagRCT},
		{"nothing", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, designTagFromPubTypes(tt.types))
		})
	}
}

// The Cochrane filter is the one the Handbook gives; a change to it must be a
// deliberate one.
func TestSearchQueries(t *testing.T) {
	assert.Equal(t, "(T) AND ("+cochraneHSSS+")", SearchRCTFilter.query("T"))
	assert.Equal(t, "(T) AND (systematic[sb] OR meta-analysis[pt])", SearchReviews.query("T"))
	assert.Equal(t, `(randomized controlled trial[pt] OR controlled clinical trial[pt] OR randomized[tiab] OR `+
		`placebo[tiab] OR drug therapy[sh] OR randomly[tiab] OR trial[tiab] OR groups[tiab]) `+
		`NOT (animals[mh] NOT humans[mh])`, cochraneHSSS)
	assert.Equal(t, 300, SearchRCTFilter.limit())
	assert.Equal(t, 50, SearchReviews.limit())
}
