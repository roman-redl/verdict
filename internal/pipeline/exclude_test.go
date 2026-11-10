package pipeline

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// Fixture DOIs of the exclusion tests: a Cochrane review in three versions,
// and the review a Consensus query finds next to it.
const (
	cochraneNoPub   = "10.1002/14651858.cd012345"
	cochranePub1    = "10.1002/14651858.cd012345.pub1"
	cochranePub2    = "10.1002/14651858.cd012345.pub2"
	cochranePub3    = "10.1002/14651858.cd012345.pub3"
	otherReviewDOI  = "10.1/other-review"
	consTrialDOI    = "10.1/cons-trial"
	excludedViaURL  = "https://doi.org/10.1002/14651858.CD012345.pub2"
	cochraneOtherCD = "10.1002/14651858.cd012346"
	urlPub4         = "https://doi.org/10.1002/14651858.CD012345.PUB4"
)

func TestDOIExclusionMatches(t *testing.T) {
	tests := []struct {
		name      string
		excluded  []string
		candidate string
		want      bool
	}{
		{"plain equal", []string{doiA}, doiA, true},
		{"plain other", []string{doiA}, doiB, false},
		{"case and URL prefix of the list", []string{"https://doi.org/" + strings.ToUpper(doiA)}, doiA, true},
		{"case and doi: prefix of the candidate", []string{doiA}, "DOI:10.1/A", true},
		{"empty candidate", []string{doiA}, "", false},
		{"cochrane listed with a version, the candidate has none", []string{cochranePub2}, cochraneNoPub, true},
		{"cochrane listed with a version, an earlier one", []string{cochranePub2}, cochranePub1, true},
		{"cochrane listed with a version, a later one", []string{cochranePub2}, cochranePub3, true},
		{"cochrane listed with a version, the candidate in URL form", []string{cochranePub2}, urlPub4, true},
		{"cochrane listed without a version", []string{cochraneNoPub}, cochranePub2, true},
		{"cochrane listed in URL form", []string{excludedViaURL}, cochraneNoPub, true},
		{"another CD number", []string{cochranePub2}, cochraneOtherCD, false},
		{"a CD number that only starts the same", []string{cochranePub2}, "10.1002/14651858.cd0123450", false},
		{"the CD number of another prefix", []string{cochranePub2}, "10.1/cd012345", false},
		{"not a cochrane DOI keeps exact matching", []string{"10.1002/jcsm.12345"}, "10.1002/jcsm.12345.pub2", false},
		{"one of several", []string{doiA, doiB}, doiB, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newDOIExclusion(tt.excluded)
			require.NotNil(t, e)
			assert.Equal(t, tt.want, e.matches(tt.candidate))
		})
	}
}

func TestNilExclusionExcludesNothing(t *testing.T) {
	var e *doiExclusion

	assert.False(t, e.matches(doiA))
	assert.False(t, e.drop(doiA))

	var info DiscoveryInfo

	e.record(&info)
	assert.Equal(t, DiscoveryInfo{}, info)
}

func TestNewDOIExclusionParsesTheFlagValues(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   []string // nil: no exclusion
	}{
		{"nothing", nil, nil},
		{"blank values", []string{"", " ", ",,"}, nil},
		{"one DOI", []string{doiA}, []string{doiA}},
		{"repeated flag", []string{doiA, doiB}, []string{doiA, doiB}},
		{"comma-separated", []string{"10.1/a, 10.1/b,"}, []string{doiA, doiB}},
		{"both forms, deduplicated", []string{"https://doi.org/10.1/A,10.1/b", "doi:10.1/a"}, []string{doiA, doiB}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newDOIExclusion(tt.values)
			if tt.want == nil {
				assert.Nil(t, e)
				return
			}

			require.NotNil(t, e)
			assert.Equal(t, tt.want, e.listed)
		})
	}
}

func TestDOIExclusionRecordsDistinctDrops(t *testing.T) {
	e := newDOIExclusion([]string{cochranePub2})

	assert.True(t, e.drop(cochranePub3))
	assert.True(t, e.drop("https://doi.org/"+strings.ToUpper(cochranePub3)), "the same DOI again")
	assert.True(t, e.drop(cochraneNoPub))
	assert.False(t, e.drop(doiA))

	var info DiscoveryInfo

	e.record(&info)

	assert.Equal(t, []string{cochranePub2}, info.ExcludeDOIs)
	assert.Equal(t, []string{cochranePub3, cochraneNoPub}, info.ExcludedDOIs)
	assert.Equal(t, 2, info.ExcludedCount)
}

// The pool drops an excluded DOI whichever channel brings it, and keeps the
// rest.
func TestCandidatePoolDropsExcluded(t *testing.T) {
	cp := newCandidatePool(newDOIExclusion([]string{cochranePub2}))

	cp.add(cons(cochranePub3, 1, 1, tagSystematicReview))
	cp.add(cons(doiA, 1, 2, designRCT))
	cp.add(pub(cochraneNoPub, SearchReviews, 1, tagSystematicReview))
	cp.add(pub(doiB, SearchRCTFilter, 1, designRCT))

	require.Len(t, cp.list, 2)
	assert.Equal(t, doiA, cp.list[0].DOI)
	assert.Equal(t, doiB, cp.list[1].DOI)
	assert.Equal(t, []string{cochranePub3, cochraneNoPub}, cp.excl.dropped)
}

func TestMergeReferencesSkipsExcluded(t *testing.T) {
	refs := []reviewRefs{{works: []model.Work{
		{ID: "W1", DOI: "https://doi.org/10.1002/14651858.CD012345.pub1", Title: "The excluded review"},
		refWork("W2"),
	}, ok: true}}

	excl := newDOIExclusion([]string{cochranePub2})
	pool, added := mergeReferences(nil, refs, excl)

	require.Equal(t, 1, added)
	require.Len(t, pool, 1)
	assert.Equal(t, "10.5/w2", pool[0].DOI)
	assert.Equal(t, []string{cochranePub1}, excl.dropped)
}

// The excluded review never reaches the mining: its reference list is not
// read, its studies do not join the pool.
func TestExcludedReviewIsNeverMined(t *testing.T) {
	tests := []struct {
		name      string
		exclude   []string
		wantMined bool
	}{
		{"without the exclusion the review is mined", nil, true},
		{"with it the review is not", []string{cochranePub2}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
				review := hit(cochranePub3, "Surgery for reflux: a Cochrane review")
				review.StudyDesign = tagSystematicReview

				return pageOf(true, review, hit(consTrialDOI, "A trial")), nil
			}}
			works := reviewWorks(map[string][]string{cochranePub3: {"W7"}})
			plan := &planFake{screen: includeAll(t)}
			p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10, LLMConcurrency: 1},
				Clients{Search: search, Works: works, LLMPlan: plan}, quietLog())

			st := &State{}
			sources := DiscoverySources{model.SourceConsensus, model.SourceReviewRefs}
			require.NoError(t, p.discoverWith(t.Context(), st,
				Options{Question: "q", Sources: sources, ExcludeDOIs: tt.exclude}))

			d := st.Discovery

			if tt.wantMined {
				assert.Equal(t, 1, works.calls.get("ByDOI:"+cochranePub3))
				assert.Equal(t, 1, d.ReviewsMined)
				assert.ElementsMatch(t, []string{consTrialDOI, cochranePub3, "10.5/w7"}, poolDOIs(d.Pool))

				return
			}

			assert.Zero(t, works.calls.get("ByDOI:"+cochranePub3), "the excluded review's references are never read")
			assert.Zero(t, works.calls.total(), "no review is left to mine")
			assert.Zero(t, d.ReviewsMined)
			assert.Zero(t, d.RefsAdded)
			assert.Equal(t, []string{consTrialDOI}, poolDOIs(d.Pool))
			assert.Equal(t, []string{consTrialDOI}, dois(st.Papers))
		})
	}
}

func poolDOIs(pool []Candidate) []string {
	out := make([]string, len(pool))
	for i := range pool {
		out[i] = pool[i].DOI
	}

	return out
}

// Every channel brings a version of the excluded review: Consensus one, PubMed
// another, a mined reference list a third. None enters the pool, the review is
// not mined, the artifact names what was dropped.
func TestDiscoverExcludesAcrossChannels(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		excluded := hit(cochranePub3, "Surgery for reflux: a Cochrane review")
		excluded.StudyDesign = tagSystematicReview
		other := hit(otherReviewDOI, "Another review of reflux surgery")
		other.StudyDesign = tagSystematicReview

		return pageOf(true, excluded, other, hit(consTrialDOI, "A consensus trial")), nil
	}}
	pm := &fakePubMed{
		hits: func(q string) model.PubMedHits {
			if strings.Contains(q, "systematic[sb]") {
				return model.PubMedHits{PMIDs: []string{"2", "3"}, Total: 2}
			}

			return model.PubMedHits{PMIDs: []string{"1"}, Total: 1}
		},
		records: map[string]model.PubMedRecord{
			"1": pmRec("1", pmTrialDOI, "Fundoplication trial", pmTypeRCT),
			"2": pmRec("2", "10.1002/14651858.CD012345", "Surgery for reflux", pmTypeSR),
			"3": pmRec("3", otherReviewDOI, "Another review of reflux surgery", pmTypeSR),
		},
	}
	works := reviewWorks(map[string][]string{otherReviewDOI: {"W1", "W2"}})
	works.byIDs = func([]string) ([]model.Work, error) {
		return []model.Work{
			{ID: "W1", DOI: "https://doi.org/10.1002/14651858.CD012345.pub1", Title: "Cochrane review, first version"},
			refWork("W2"),
		}, nil
	}
	plan := &planFake{writer: writerReply, screen: includeAll(t)}
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10, LLMConcurrency: 1},
		Clients{Search: search, PubMed: pm, Works: works, LLMPlan: plan}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{
		Question: "does fundoplication help?", ExcludeDOIs: []string{excludedViaURL},
	}))

	d := st.Discovery

	assert.ElementsMatch(t, []string{consTrialDOI, otherReviewDOI, pmTrialDOI, "10.5/w2"}, poolDOIs(d.Pool))
	assert.Equal(t, 1, works.calls.get("ByDOI:"+otherReviewDOI), "only the other review is mined")

	for _, doi := range []string{cochranePub3, cochraneNoPub, cochranePub1} {
		assert.Zero(t, works.calls.get("ByDOI:"+doi), "the excluded review %s is never looked up", doi)
	}

	assert.Equal(t, 1, d.ReviewsMined)

	for _, doi := range dois(st.Papers) {
		assert.False(t, strings.HasPrefix(doi, "10.1002/14651858"), "the corpus holds no version of the review: %s", doi)
	}

	assert.Equal(t, []string{cochranePub2}, d.ExcludeDOIs)
	assert.Equal(t, []string{cochranePub3, cochraneNoPub, cochranePub1}, d.ExcludedDOIs,
		"Consensus, PubMed, then the reference list")
	assert.Equal(t, 3, d.ExcludedCount)
}

// A run without the flag leaves the artifact fields empty.
func TestDiscoverWithoutExclusionRecordsNothing(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		return pageOf(true, hit(consTrialDOI, "A trial")), nil
	}}
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10}, Clients{Search: search}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st,
		Options{Question: "q", Sources: DiscoverySources{model.SourceConsensus}}))

	assert.Empty(t, st.Discovery.ExcludeDOIs)
	assert.Empty(t, st.Discovery.ExcludedDOIs)
	assert.Zero(t, st.Discovery.ExcludedCount)
}

// Mode C: a DOI both given and excluded is excluded, with a warning.
func TestDiscoverByDOIsExcludes(t *testing.T) {
	works := &fakeWorks{byDOI: func(doi string) (*model.Work, error) { return work(doi, "T"), nil }}
	log, buf := logBuffer()
	p := newTest(t, Settings{MaxPapers: 5}, Clients{Works: works}, log)

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{
		DOIFile:     doiList(t, listDOIA, listDOIB),
		ExcludeDOIs: []string{"https://doi.org/" + strings.ToUpper(listDOIB)},
	}))

	assert.Equal(t, []string{listDOIA}, dois(st.Papers))
	assert.Zero(t, works.calls.get("ByDOI:"+listDOIB), "an excluded DOI is not even looked up")
	assert.Contains(t, buf.String(), "level=WARN")
	assert.Contains(t, buf.String(), "also excluded")
	assert.Contains(t, buf.String(), listDOIB)
	assert.Equal(t, []string{listDOIB}, st.Discovery.ExcludeDOIs)
	assert.Equal(t, []string{listDOIB}, st.Discovery.ExcludedDOIs)
	assert.Equal(t, 1, st.Discovery.ExcludedCount)
}

func TestDiscoverByDOIsAllExcludedIsAUserError(t *testing.T) {
	p := newTest(t, Settings{MaxPapers: 5}, Clients{Works: &fakeWorks{}}, quietLog())

	err := p.discoverWith(t.Context(), &State{}, Options{
		DOIFile: doiList(t, listDOIA), ExcludeDOIs: []string{listDOIA},
	})

	var ue *UserError

	require.ErrorAs(t, err, &ue)
	assert.Contains(t, ue.Msg, "excluded")
}

// Mode A has no pool and no mining, but the flag still holds.
func TestDiscoverOpenAlexExcludes(t *testing.T) {
	works := &fakeWorks{search: func(string, int) ([]model.Paper, error) {
		return []model.Paper{{DOI: cochranePub3, Title: "Review"}, {DOI: doiA, Title: "Trial"}}, nil
	}}
	p := newTest(t, Settings{MaxPapers: 5}, Clients{Works: works}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{
		Question: "q", Engine: EngineOpenAlex, ExcludeDOIs: []string{cochranePub1},
	}))

	assert.Equal(t, []string{doiA}, dois(st.Papers))
	assert.Equal(t, []string{cochranePub3}, st.Discovery.ExcludedDOIs)
	assert.Equal(t, 1, st.Discovery.ExcludedCount)
}
