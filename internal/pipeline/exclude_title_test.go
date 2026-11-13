package pipeline

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// The source review of the question-263 case and the reprints of it that kept
// reaching the critic after its DOI was excluded.
const (
	sourceReviewTitle = "Fluid therapy for acute bacterial meningitis"
	sourceReviewDOI   = "10.1002/14651858.cd004786.pub3"
	ebchReprintDOI    = "10.1002/ebch.313"
	ebchSummaryDOI    = "10.1002/ebch.300"
	ebchCommentaryDOI = "10.1002/ebch.304"
	ebchReprintTitle  = "Cochrane review: Fluid therapy for acute bacterial meningitis"
	ebchSummaryTitle  = "Summary of 'Fluid therapy for acute bacterial meningitis', " +
		"including tables of key findings and quality of included trials"
	ebchCommentaryTitle = "Commentaries on ‘Fluid therapy for acute bacterial meningitis’"
	restrictedTrial     = "Restricted fluid therapy in children with bacterial meningitis: a randomized trial"

	// A Cochrane title is "intervention for condition": a real trial can contain it whole.
	corticosteroidsReview = "Corticosteroids for acute bacterial meningitis"
	corticosteroidsTrial  = "Adjunctive corticosteroids for acute bacterial meningitis in adults: " +
		"a randomized controlled trial"

	// the same title with a tag-free ampersand and with an escaped one
	htmlExcluded        = "Beta-blockers & statins for acute bacterial meningitis"
	htmlExcludedEscaped = "Beta-blockers &amp; statins for acute bacterial meningitis"

	// candidate titles with a marker, as the sources might spell them
	shoutedTitle    = "COCHRANE: FLUID THERAPY, for acute bacterial-meningitis!"
	taggedTitle     = "Cochrane: <i>Beta-blockers</i> &amp; <scp>statins</scp> for acute bacterial meningitis"
	escapedTitle    = "Cochrane: Beta-blockers &amp; statins for acute bacterial meningitis"
	splitWordTitle  = "Commentary: Fluid therapy for acute bacterial <i>menin</i>gitis"
	taggedWordTitle = "Commentary: Fluid therapy for acute <i>bacterial</i> meningitis"
	lessThanTitle   = "Risk <5 for acute bacterial meningitis"
)

func TestDOIExclusionMatchesTitle(t *testing.T) {
	tests := []struct {
		name     string
		excluded string
		title    string
		want     bool
	}{
		{"a reprint with a prefix", sourceReviewTitle, ebchReprintTitle, true},
		{"a summary with ASCII quotes", sourceReviewTitle, ebchSummaryTitle, true},
		{"commentaries with Unicode quotes", sourceReviewTitle, ebchCommentaryTitle, true},
		{"case and punctuation with a marker", sourceReviewTitle, shoutedTitle, true},
		{"the same title", sourceReviewTitle, sourceReviewTitle, true},
		{"a trial that only shares words", sourceReviewTitle, restrictedTrial, false},
		{"a trial that contains the whole title, no marker", corticosteroidsReview, corticosteroidsTrial, false},
		{"the same trial title with Cochrane in it", corticosteroidsReview, corticosteroidsTrial + " (Cochrane)", true},
		{"no marker in a longer title", sourceReviewTitle, "Notes on " + sourceReviewTitle + " in adults", false},
		{"HTML tags are stripped", htmlExcluded, taggedTitle, true},
		{"entities are unescaped in both", htmlExcludedEscaped, escapedTitle, true},
		{"markup inside a word is removed", sourceReviewTitle, splitWordTitle, true},
		{"markup inside the title", sourceReviewTitle, taggedWordTitle, true},
		{"a less-than sign is not a tag", lessThanTitle, "Summary of " + lessThanTitle, true},
		{"straight double quotes", sourceReviewTitle, `Evidence: "` + sourceReviewTitle + `" in children`, true},
		{"typographic double quotes", sourceReviewTitle, "Evidence: “" + sourceReviewTitle + "” in children", true},
		{"typographic single quotes", sourceReviewTitle, "Evidence: ‘" + sourceReviewTitle + "’ in children", true},
		{"straight single quotes", sourceReviewTitle, "Evidence: '" + sourceReviewTitle + "' in children", true},
		{"an apostrophe is not a quote", sourceReviewTitle, "Children's " + sourceReviewTitle + " in adult's trials", false},
		{"quotes round something else", sourceReviewTitle, "A 'randomized' trial of " + sourceReviewTitle, false},
		{"an unclosed quote opens nothing", sourceReviewTitle, "Evidence: ‘" + sourceReviewTitle + " in children", false},
		{"the title inside a longer quotation", sourceReviewTitle, "On “" + sourceReviewTitle + ": a review”", true},
		{"the title between two quotations", sourceReviewTitle, "‘A’ " + sourceReviewTitle + " ‘B’", false},
		{"a word that starts like the commentary", sourceReviewTitle, sourceReviewTitle + ": commentary", true},
		{"a summary of the title", sourceReviewTitle, "Summary of " + sourceReviewTitle, true},
		{"summary without of", sourceReviewTitle, sourceReviewTitle + ": summary", false},
		{"the words in another order", sourceReviewTitle, "Bacterial meningitis, acute: fluid therapy for", false},
		{"a word that only starts the same", sourceReviewTitle, "Fluid therapy for acute bacterial meningitidis", false},
		{"an empty candidate title", sourceReviewTitle, "", false},
		{"three words are too few", "Bacterial meningitis therapy", "Cochrane: Bacterial meningitis therapy", false},
		{"a title of two words is too short", "Bacterial meningitis", "Cochrane: Bacterial meningitis in children", false},
		{"four words are enough", "Fluid therapy bacterial meningitis", "Cochrane: Fluid therapy bacterial meningitis", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newDOIExclusion([]string{sourceReviewDOI})
			require.NotNil(t, e)

			e.addTitle(tt.excluded)
			assert.Equal(t, tt.want, e.matchesTitle(tt.title))
		})
	}
}

func TestNormalizeTitle(t *testing.T) {
	assert.Equal(t, "summary of fluid therapy for acute bacterial meningitis including tables",
		normalizeTitle("Summary of ‘Fluid  therapy’ for ACUTE bacterial-meningitis, including tables"))
	assert.Empty(t, normalizeTitle(" — : "))
	assert.Equal(t, "beta blockers statins for p 0 05",
		normalizeTitle("<i>Beta</i>-blockers &amp; <sub>statins</sub> for p &lt; 0.05"))
}

// A candidate is dropped by title only when it has a DOI to record.
func TestDropByTitleNeedsADOI(t *testing.T) {
	e := newDOIExclusion([]string{sourceReviewDOI})
	e.addTitle(sourceReviewTitle)

	assert.True(t, e.drop(ebchReprintDOI, ebchReprintTitle))
	assert.False(t, e.drop("", ebchReprintTitle))
	assert.False(t, e.drop(doiA, restrictedTrial))
	assert.Equal(t, []string{ebchReprintDOI}, e.dropped)
}

// Excluded titles come from the metadata port, one lookup per listed DOI.
func TestNewExclusionLoadsTitles(t *testing.T) {
	works := &fakeWorks{byDOI: func(doi string) (*model.Work, error) {
		switch doi {
		case sourceReviewDOI:
			return work(doi, sourceReviewTitle), nil
		case doiB:
			return work(doi, "Short title"), nil // too short to exclude by title
		}

		return nil, nil //nolint:nilnil // the port's "unknown DOI"
	}}
	p := newTest(t, Settings{}, Clients{Works: works}, quietLog())

	e := p.newExclusion(t.Context(), []string{sourceReviewDOI + "," + doiB, doiA})

	require.NotNil(t, e)
	assert.Equal(t, []string{normalizeTitle(sourceReviewTitle)}, e.titles)
	assert.Equal(t, 3, works.calls.total(), "one lookup per listed DOI")
	assert.True(t, e.matchesTitle(ebchReprintTitle))
	assert.Nil(t, p.newExclusion(t.Context(), nil))
	assert.Equal(t, 3, works.calls.total(), "no exclusion, no lookup")
}

// Mode B: the reprints of the excluded review stay out of the pool and are
// listed as excluded; a trial that shares words with the title stays.
func TestDiscoverExcludesReprintsByTitle(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		return pageOf(true,
			hit(ebchReprintDOI, ebchReprintTitle),
			hit(ebchSummaryDOI, ebchSummaryTitle),
			hit(ebchCommentaryDOI, ebchCommentaryTitle),
			hit(consTrialDOI, restrictedTrial),
		), nil
	}}
	works := &fakeWorks{byDOI: func(doi string) (*model.Work, error) { return work(doi, sourceReviewTitle), nil }}
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10}, Clients{Search: search, Works: works}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{
		Question: "q", Sources: DiscoverySources{model.SourceConsensus}, ExcludeDOIs: []string{sourceReviewDOI},
	}))

	d := st.Discovery

	assert.Equal(t, []string{consTrialDOI}, poolDOIs(d.Pool))
	assert.Equal(t, []string{ebchReprintDOI, ebchSummaryDOI, ebchCommentaryDOI}, d.ExcludedDOIs)
	assert.Equal(t, 3, d.ExcludedCount)
	assert.Equal(t, []string{sourceReviewDOI}, d.ExcludeDOIs)
}

// A reference-list work with the review's title is skipped like one with its DOI.
func TestMergeReferencesSkipsReprintByTitle(t *testing.T) {
	refs := []reviewRefs{{works: []model.Work{
		{ID: "W1", DOI: "https://doi.org/" + ebchReprintDOI, Title: ebchReprintTitle},
		refWork("W2"),
	}, ok: true}}

	excl := newDOIExclusion([]string{sourceReviewDOI})
	excl.addTitle(sourceReviewTitle)

	pool, added := mergeReferences(nil, refs, excl)

	require.Equal(t, 1, added)
	require.Len(t, pool, 1)
	assert.Equal(t, "10.5/w2", pool[0].DOI)
	assert.Equal(t, []string{ebchReprintDOI}, excl.dropped)
}

// Mode A has titles too.
func TestDiscoverOpenAlexExcludesReprintByTitle(t *testing.T) {
	works := &fakeWorks{
		byDOI: func(doi string) (*model.Work, error) { return work(doi, sourceReviewTitle), nil },
		search: func(string, int) ([]model.Paper, error) {
			return []model.Paper{{DOI: ebchReprintDOI, Title: ebchReprintTitle}, {DOI: doiA, Title: restrictedTrial}}, nil
		},
	}
	p := newTest(t, Settings{MaxPapers: 5}, Clients{Works: works}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{
		Question: "q", Engine: EngineOpenAlex, ExcludeDOIs: []string{sourceReviewDOI},
	}))

	assert.Equal(t, []string{doiA}, dois(st.Papers))
	assert.Equal(t, []string{ebchReprintDOI}, st.Discovery.ExcludedDOIs)
}

// A failed or empty title lookup never stops the run: the DOI exclusion works
// as before, the title rule is off for that DOI, and a warning says so.
func TestExclusionSurvivesTitleLookupFailure(t *testing.T) {
	fails := func(string) (*model.Work, error) { return nil, errors.New("openalex is down") }
	unknown := func(string) (*model.Work, error) {
		return nil, nil //nolint:nilnil // the port's "unknown DOI"
	}

	tests := []struct {
		name  string
		byDOI func(string) (*model.Work, error)
		warn  string
	}{
		{"the lookup fails", fails, "openalex is down"},
		{"the source does not know the DOI", unknown, "no title"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
				return pageOf(true,
					hit(sourceReviewDOI, sourceReviewTitle),
					hit(ebchReprintDOI, ebchReprintTitle),
				), nil
			}}
			log, buf := logBuffer()
			p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10},
				Clients{Search: search, Works: &fakeWorks{byDOI: tt.byDOI}}, log)

			st := &State{}
			require.NoError(t, p.discoverWith(t.Context(), st, Options{
				Question: "q", Sources: DiscoverySources{model.SourceConsensus}, ExcludeDOIs: []string{sourceReviewDOI},
			}))

			assert.Equal(t, []string{ebchReprintDOI}, poolDOIs(st.Discovery.Pool), "only the DOI exclusion works")
			assert.Equal(t, []string{sourceReviewDOI}, st.Discovery.ExcludedDOIs)
			assert.Contains(t, buf.String(), "level=WARN")
			assert.Contains(t, buf.String(), "excluding it by DOI only")
			assert.Contains(t, buf.String(), tt.warn)
		})
	}
}

// Without a metadata source the exclusion is by DOI alone, with a warning.
func TestExclusionWithoutMetadataSourceWarns(t *testing.T) {
	log, buf := logBuffer()
	p := newTest(t, Settings{}, Clients{}, log)

	e := p.newExclusion(t.Context(), []string{sourceReviewDOI})

	require.NotNil(t, e)
	assert.True(t, e.matches(sourceReviewDOI))
	assert.Empty(t, e.titles)
	assert.Contains(t, buf.String(), "level=WARN")
}
