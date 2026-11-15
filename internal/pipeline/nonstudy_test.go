package pipeline

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// Publication types as the metadata sources give them, in lower case.
const (
	pubErratum = "published erratum" // PubMed
	oaErratum  = "erratum"           // OpenAlex
	pubComment = "comment"
	pubLetter  = "letter"
	pubArticle = "article"
	pubRetract = "retraction"
	pubTrial   = "randomized controlled trial"
	// kindErratum is what model.NonStudyKind says of both erratum types.
	kindErratum = "erratum or correction"
)

// typed gives a fixture candidate the publication types its source reported.
func typed(c Candidate, types ...string) Candidate {
	c.PublicationTypes = model.NormalizePublicationTypes(types)
	c.paper.PublicationTypes = c.PublicationTypes

	return c
}

// A paper whose types show it is not a study report takes no corpus slot, even
// when the screening let it in and trials are waiting for the place.
func TestSelectionSkipsPapersThatAreNotStudyReports(t *testing.T) {
	pool := []Candidate{
		typed(cons("10.1/erratum", 1, 1, "rct"), pubErratum),
		cons(doiA, 1, 2, "rct"),
		typed(withScreen(pub("10.1/comment", SearchRCTFilter, 1, ""), ScreenDecision{Include: true}), pubComment),
		cons(doiB, 1, 3, "rct"),
	}

	assert.False(t, pool[0].Eligible())
	assert.False(t, pool[2].Eligible())

	got := selectCandidates(pool, 2)

	assert.Equal(t, []string{doiA, doiB}, dois(got), "the slots go to the studies")
	assert.False(t, pool[0].Selected)
	assert.False(t, pool[2].Selected)
}

// A paper typed as a trial and as a letter stays a study.
func TestSelectionKeepsAResearchLetter(t *testing.T) {
	c := typed(cons(doiA, 1, 1, "rct"), pubLetter, pubTrial)

	assert.True(t, c.Eligible())
}

// The types any channel gave the paper reach the pool record and the corpus
// paper, and the audit file carries them.
func TestCandidatePoolMergesPublicationTypes(t *testing.T) {
	cp := newCandidatePool(nil)

	cp.add(cons(doiA, 1, 1, "rct"))
	cp.add(typed(pub(doiA, SearchRCTFilter, 3, ""), pubErratum))

	require.Len(t, cp.list, 1)
	assert.Equal(t, []string{pubErratum}, cp.list[0].PublicationTypes)
	assert.Equal(t, []string{pubErratum}, cp.list[0].paper.PublicationTypes, "the corpus paper too")
	assert.False(t, cp.list[0].Eligible())

	b, err := json.Marshal(cp.list[0])
	require.NoError(t, err)
	assert.Contains(t, string(b), `"publication_types":["`+pubErratum+`"]`)

	var back Candidate

	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, cp.list[0].PublicationTypes, back.PublicationTypes)
}

// A pool written before the field existed loads; its candidates are studies.
func TestCandidateWithoutPublicationTypesLoads(t *testing.T) {
	const old = `{"doi":"10.1/a","title":"T","sources":[{"source":"consensus","rank":1}],"selected":true}`

	var c Candidate

	require.NoError(t, json.Unmarshal([]byte(old), &c))
	assert.Empty(t, c.PublicationTypes)
	assert.True(t, c.Eligible())
}

// PubMed's own publication types come with the record the PubMed channel found.
func TestPubmedCandidateCarriesThePublicationTypes(t *testing.T) {
	by := FoundBy{Source: model.SourcePubMed, Search: SearchRCTFilter, Rank: 1}

	c := pubmedCandidate(model.PubMedRecord{
		PMID: "1", DOI: doiA, Title: "T", PubTypes: []string{"Randomized Controlled Trial", "Comment"},
	}, by)

	assert.Equal(t, []string{pubComment, pubTrial}, c.PublicationTypes)
	assert.Equal(t, discTagRCT, c.Design)
	assert.Empty(t, model.NonStudyKind(c.PublicationTypes), "typed as a trial, so a study")

	erratum := pubmedCandidate(model.PubMedRecord{PMID: "2", DOI: doiB, PubTypes: []string{"Published Erratum"}}, by)

	assert.False(t, erratum.Eligible())
}

// The corpus selection logs what the types keep out, one line per kind.
func TestLogNonStudyCandidates(t *testing.T) {
	var logs bytes.Buffer

	p := newTest(t, Settings{}, Clients{}, slog.New(slog.NewTextHandler(&logs, nil)))

	p.logNonStudyCandidates([]Candidate{
		typed(cons(doiA, 1, 1, ""), oaErratum),
		typed(cons(doiB, 1, 2, ""), "Published Erratum"),
		typed(cons(doiC, 1, 3, ""), "Editorial"),
		cons("10.1/d", 1, 4, ""),
	})

	assert.Contains(t, logs.String(), `kind="`+kindErratum+`" count=2`)
	assert.Contains(t, logs.String(), "kind=editorial count=1")
	assert.NotContains(t, logs.String(), "10.1/d")
}

func TestNormalizeEffect(t *testing.T) {
	var logs bytes.Buffer

	log := slog.New(slog.NewTextHandler(&logs, nil))

	t.Run("valid labels are normalized", func(t *testing.T) {
		ef := &model.Effect{
			OutcomeMatch: " Same ", TimepointMatch: "NO", TimepointWeeks: new(52.0),
			Cluster: &model.Cluster{Clusters: new(10.0), MeanSize: new(30.0), ICC: new(0.05)},
		}

		normalizeEffect(ef, doiA, log)

		assert.Equal(t, model.OutcomeSame, ef.OutcomeMatch)
		assert.Equal(t, model.TimepointNo, ef.TimepointMatch)
		assert.NotNil(t, ef.TimepointWeeks)
		assert.NotNil(t, ef.Cluster.Clusters)
		assert.NotNil(t, ef.Cluster.MeanSize)
		assert.NotNil(t, ef.Cluster.ICC)
		assert.Empty(t, logs.String())
	})

	t.Run("a word outside the vocabulary is treated as missing, with a warning", func(t *testing.T) {
		ef := &model.Effect{OutcomeMatch: "similar", TimepointMatch: "maybe"}

		normalizeEffect(ef, doiA, log)

		assert.Empty(t, ef.OutcomeMatch, "missing reads as the same outcome: never excluded by a garbled answer")
		assert.Empty(t, ef.TimepointMatch)
		assert.True(t, ef.OutcomeMatch.IsSame())
		assert.False(t, ef.TimepointMatch.IsOff())
		assert.Contains(t, logs.String(), "unknown outcome match")
		assert.Contains(t, logs.String(), "unknown timepoint match")
	})

	t.Run("numbers that cannot be real are dropped", func(t *testing.T) {
		ef := &model.Effect{
			TimepointWeeks: new(-4.0),
			Cluster:        &model.Cluster{Clusters: new(0.0), MeanSize: new(-3.0), ICC: new(1.5)},
		}

		normalizeEffect(ef, doiA, log)

		assert.Nil(t, ef.TimepointWeeks)
		assert.Nil(t, ef.Cluster.Clusters)
		assert.Nil(t, ef.Cluster.MeanSize)
		assert.Nil(t, ef.Cluster.ICC)
	})

	t.Run("no effect, nothing to do", func(t *testing.T) {
		assert.NotPanics(t, func() { normalizeEffect(nil, doiA, log) })
	})
}

// The critic's answer goes through normalizeCritique: a garbled label never
// reaches the pool as "different".
func TestNormalizeCritiqueNormalizesTheEffect(t *testing.T) {
	c := normalizeCritique(model.Critique{
		DOI: doiA, Effect: &model.Effect{OutcomeMatch: "RELATED", TimepointMatch: "n/a"},
	}, ep(doiA), quietLog())

	assert.Equal(t, model.OutcomeRelated, c.Effect.OutcomeMatch)
	assert.Empty(t, c.Effect.TimepointMatch)
}

// Both critique forms ask for the matching fields, the adjusted estimate of a
// non-randomized study and the cluster description.
func TestPromptsCarryTheMatchingInstructions(t *testing.T) {
	for _, prompt := range []string{critiqueSystemPrompt, batchCritiqueSystemPrompt} {
		assert.Contains(t, prompt, `outcome_match: "same" | "related" | "different"`)
		assert.Contains(t, prompt, "timepoint_weeks")
		assert.Contains(t, prompt, `timepoint_match: "yes" | "no" | "unclear"`)
		assert.Contains(t, prompt, "adjusted (true for a non-randomized study")
		assert.Contains(t, prompt, "adjusted_for_clustering")
		assert.Contains(t, prompt, "ADJUSTED estimate")
		assert.Contains(t, prompt, "In a randomized trial prefer counts or means")
		assert.Contains(t, prompt, "icc (intracluster correlation, only if reported)")
	}
}

// An enrichment cache entry written before the types were kept still applies,
// and a new one carries the types.
func TestEnrichCacheCarriesThePublicationTypes(t *testing.T) {
	e := ep(doiA)
	e.PublicationTypes = nil

	applyCached(&e, CacheEntry{Types: []string{oaErratum}})
	assert.Equal(t, []string{oaErratum}, e.PublicationTypes)

	var old CacheEntry

	require.NoError(t, json.Unmarshal([]byte(`{"abstract":"x","retracted":false}`), &old))
	assert.Empty(t, old.Types)

	e2 := ep(doiB)
	applyCached(&e2, old)
	assert.Empty(t, e2.PublicationTypes)
}

// OpenAlex's type, found at the enrichment of a Consensus paper, marks it.
func TestEnrichmentReadsTheOpenAlexType(t *testing.T) {
	works := &fakeWorks{byDOI: func(string) (*model.Work, error) {
		return &model.Work{DOI: doiA, Title: "T", Types: []string{oaErratum}}, nil
	}}
	p := newTest(t, Settings{}, Clients{Works: works}, quietLog())

	e := model.EnrichedPaper{}
	e.DOI, e.Source = doiA, model.SourceConsensus

	p.checkIntegrity(t.Context(), &e)

	assert.Equal(t, []string{oaErratum}, e.PublicationTypes)
	assert.Equal(t, kindErratum, model.NonStudyKind(e.PublicationTypes))
}

// With no pooled estimate the studies that were kept out are named in a
// warning, so that the reader sees why the numbers are not pooled.
func TestNoPoolWarning(t *testing.T) {
	assert.Empty(t, noPoolWarning(poolResult{}), "nothing had numbers")
	assert.Empty(t, noPoolWarning(poolResult{est: &model.PooledEstimate{}, leftOut: []leftOut{{kind: leftOtherOutcome}}}),
		"a pooled estimate lists its own left_out")

	got := noPoolWarning(poolResult{leftOut: []leftOut{
		{kind: leftNonRandomized}, {kind: leftOtherTime}, {kind: leftNonRandomized},
	}})

	assert.Equal(t,
		"no pooled estimate; studies with numbers that were not pooled: 2 non-randomized, 1 other time point", got)
}
