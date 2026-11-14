package pipeline

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	followUpQuestion = "Does X reduce wound infection?"
	// followUpEffectReply is a valid answer: the arms' counts of a binary outcome.
	followUpEffectReply = `{"effect":{"outcome":"wound infection","outcome_match":"same",` +
		`"intervention":{"events":3,"total":50},"control":{"events":9,"total":50}}}`
	followUpNullReply = `{"effect":null}`
)

// followUpRun is the tail of the critique stage over ready critiques: the
// pipeline, the state and the cache path of the run directory.
type followUpRun struct {
	p         *Pipeline
	st        *State
	rp        runParams
	cachePath string
}

func newFollowUpRun(
	t *testing.T, s Completer, log *slog.Logger, papers []model.EnrichedPaper, crits []model.Critique,
) followUpRun {
	t.Helper()

	tmp := t.TempDir()
	opts := Options{RunDir: filepath.Join(tmp, "run")}

	return followUpRun{
		p: newTest(t, Settings{LLMConcurrency: 2, LLMChunk: 5, CritiqueCacheTTL: ttl30d}, Clients{LLM: s}, log),
		st: &State{
			Question: followUpQuestion, Enriched: papers, Critiques: crits,
		},
		rp:        params(opts),
		cachePath: critiqueCachePath(opts),
	}
}

func (r followUpRun) cached(t *testing.T, e model.EnrichedPaper) (model.Critique, bool) {
	t.Helper()

	c := LoadCritiqueCache(r.cachePath, ttl30d, testNow, quietLog())
	entry, ok := c[critiqueCacheKey(e, followUpQuestion)]

	return entry.Critique, ok
}

// directNoEffect is a critique of a direct study with a direction and no numbers.
func directNoEffect(doi string) model.Critique {
	return crit(doi, qualityHigh, relevanceDirect, directionPositive)
}

// repliesBy answers every follow-up with the reply, counting the calls.
func repliesBy(reply string) *stopCompleter {
	return &stopCompleter{reply: func(int, string) (string, error) { return reply, nil }}
}

// The missing effect of a direct primary study is asked for and filled; the
// call is counted in the critique cost, and the critique with the effect is
// written to the cache.
func TestFollowUpFillsMissingEffect(t *testing.T) {
	s := repliesBy(followUpEffectReply)
	papers := []model.EnrichedPaper{ep(doiA)}
	r := newFollowUpRun(t, s, quietLog(), papers, []model.Critique{directNoEffect(doiA)})

	require.NoError(t, r.p.followUpEffects(t.Context(), r.rp, r.st))

	require.Equal(t, 1, s.count())

	got := r.st.Critiques[0]
	require.NotNil(t, got.Effect, "the numbers must be filled in")
	require.NotNil(t, got.Effect.Intervention)
	assert.InDelta(t, 3, *got.Effect.Intervention.Events, 0)
	assert.InDelta(t, 50, *got.Effect.Control.Total, 0)
	assert.True(t, got.EffectAsked)

	require.NotNil(t, r.st.Cost)
	require.NotNil(t, r.st.Cost.Critique)
	assert.Equal(t, 1, r.st.Cost.Critique.Calls, "the follow-up is a call of the critic")

	cached, ok := r.cached(t, papers[0])
	require.True(t, ok, "the filled critique must reach the cache")
	require.NotNil(t, cached.Effect, "a resume must not ask again")

	// the prompt: the effect instructions of the critic, the question and the paper's text only
	prompt := s.prompts[0]
	assert.Contains(t, prompt, followUpQuestion)
	assert.Contains(t, prompt, ep(doiA).Abstract)
	assert.NotContains(t, prompt, "cited_by_count")
	assert.NotContains(t, prompt, "scite_tally")
}

// The follow-up system prompt carries the critic's effect field and extraction
// rules verbatim.
func TestFollowUpPromptReusesCriticInstructions(t *testing.T) {
	assert.Contains(t, effectFollowUpSystemPrompt, critiqueEffectField)
	assert.Contains(t, effectFollowUpSystemPrompt, critiqueEffectExtractionRules)
	assert.NotContains(t, effectFollowUpSystemPrompt, "Result direction from numbers",
		"the direction rule is not part of an effect-only answer")
	assert.Contains(t, critiqueEffectRules, critiqueEffectExtractionRules, "the critic prompts keep the rules")
}

// A study that is not worth the question is not asked: not direct, no
// direction, a review, a numbers-bearing critique, a failed critique, a study
// without a text, one asked before, a retracted one and a non-study report.
func TestFollowUpAsksOnlyEligibleStudies(t *testing.T) {
	review := ep("10.1/review")
	review.StudyDesign = tagMetaAnalysis

	noText := ep("10.1/notext")
	noText.Abstract = ""

	retracted := ep("10.1/retracted")
	retracted.Flags.Retracted = true

	erratum := ep("10.1/erratum")
	erratum.PublicationTypes = []string{"Published Erratum"}

	indirect := crit("10.1/indirect", qualityHigh, relevanceIndirect, directionPositive)
	unclear := crit("10.1/unclear", qualityHigh, relevanceDirect, valueUnclear)
	hasEffect := directNoEffect("10.1/has")
	hasEffect.Effect = &model.Effect{Outcome: "x"}
	failed := directNoEffect("10.1/failed")
	failed.Error = "boom"
	asked := directNoEffect("10.1/asked")
	asked.EffectAsked = true

	papers := []model.EnrichedPaper{
		ep("10.1/indirect"), ep("10.1/unclear"), review, ep("10.1/has"), ep("10.1/failed"), noText,
		ep("10.1/asked"), retracted, erratum,
	}
	crits := []model.Critique{
		indirect, unclear, directNoEffect("10.1/review"), hasEffect, failed, directNoEffect("10.1/notext"),
		asked, directNoEffect("10.1/retracted"), directNoEffect("10.1/erratum"),
	}

	s := repliesBy(followUpEffectReply)
	r := newFollowUpRun(t, s, quietLog(), papers, crits)

	require.NoError(t, r.p.followUpEffects(t.Context(), r.rp, r.st))

	assert.Zero(t, s.count(), "no study of the corpus qualifies")
	assert.Nil(t, r.st.Cost, "no call, no cost")
}

// Every result direction with a finding qualifies; "unclear" or a missing one
// does not.
func TestFollowUpDirections(t *testing.T) {
	for dir, want := range map[string]bool{
		directionPositive: true, directionNull: true, directionNegative: true, directionMixed: true,
		valueUnclear: false, "": false,
	} {
		t.Run("direction "+dir, func(t *testing.T) {
			c := crit(doiA, qualityHigh, relevanceDirect, dir)
			assert.Equal(t, want, needsEffectFollowUp(&model.EnrichedPaper{Abstract: "a", StudyDesign: designRCT}, c))
		})
	}
}

// A design that only the publication types tell (the critic said unclear) is
// primary: the study is asked.
func TestFollowUpDesignFromPublicationTypes(t *testing.T) {
	e := model.EnrichedPaper{DOI: doiA, Abstract: "a", PublicationTypes: []string{"randomized controlled trial"}}
	c := directNoEffect(doiA)
	c.StudyDesign = model.DesignUnclear

	assert.True(t, needsEffectFollowUp(&e, c))

	e.PublicationTypes = nil

	assert.False(t, needsEffectFollowUp(&e, c), "a design nobody can tell is not a primary one")
}

// An answer that is not JSON leaves the effect empty with a warning, and the
// study is not marked as asked (a later pass may try again).
func TestFollowUpInvalidReplyLeavesNil(t *testing.T) {
	s := repliesBy("this is not json at all")
	log, logs := logBuffer()
	r := newFollowUpRun(t, s, log, []model.EnrichedPaper{ep(doiA)}, []model.Critique{directNoEffect(doiA)})

	require.NoError(t, r.p.followUpEffects(t.Context(), r.rp, r.st))

	assert.Nil(t, r.st.Critiques[0].Effect)
	assert.False(t, r.st.Critiques[0].EffectAsked)
	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), "effect follow-up failed")

	_, ok := r.cached(t, ep(doiA))
	assert.False(t, ok, "nothing was learned, nothing is cached")
}

// Numbers that are placeholders (zeros) are no effect: nil, with a warning.
func TestFollowUpUnusableEffectLeavesNil(t *testing.T) {
	s := repliesBy(`{"effect":{"outcome":"x","intervention":{"events":0,"total":0},"control":{"events":0,"total":0}}}`)
	log, logs := logBuffer()
	r := newFollowUpRun(t, s, log, []model.EnrichedPaper{ep(doiA)}, []model.Critique{directNoEffect(doiA)})

	require.NoError(t, r.p.followUpEffects(t.Context(), r.rp, r.st))

	assert.Nil(t, r.st.Critiques[0].Effect)
	assert.Contains(t, logs.String(), "no usable numbers")
}

// An out-of-vocabulary label of the effect goes through the normalization of a
// normal critique: dropped with a warning, the numbers stay.
func TestFollowUpNormalizesTheEffect(t *testing.T) {
	s := repliesBy(`{"effect":{"outcome_match":"similar","timepoint_weeks":-3,` +
		`"intervention":{"events":3,"total":50},"control":{"events":9,"total":50}}}`)
	log, logs := logBuffer()
	r := newFollowUpRun(t, s, log, []model.EnrichedPaper{ep(doiA)}, []model.Critique{directNoEffect(doiA)})

	require.NoError(t, r.p.followUpEffects(t.Context(), r.rp, r.st))

	got := r.st.Critiques[0].Effect
	require.NotNil(t, got)
	assert.Empty(t, got.OutcomeMatch)
	assert.Nil(t, got.TimepointWeeks)
	assert.Contains(t, logs.String(), "unknown outcome match")
}

// The labels the vote leaves in an effect (the critic's own ones) are the
// pipeline's to write: a follow-up reply that carries them loses them.
func TestFollowUpDropsTheCriticLabelsOfTheReply(t *testing.T) {
	s := repliesBy(`{"effect":{"outcome_match":"related","timepoint_match":"no",` +
		`"outcome_match_critic":"same","timepoint_match_critic":"yes",` +
		`"intervention":{"events":3,"total":50},"control":{"events":9,"total":50}}}`)
	r := newFollowUpRun(t, s, quietLog(), []model.EnrichedPaper{ep(doiA)}, []model.Critique{directNoEffect(doiA)})

	require.NoError(t, r.p.followUpEffects(t.Context(), r.rp, r.st))

	got := r.st.Critiques[0].Effect
	require.NotNil(t, got)
	assert.Equal(t, model.OutcomeRelated, got.OutcomeMatch, "the labels themselves stay")
	assert.Equal(t, model.TimepointNo, got.TimepointMatch)
	assert.Empty(t, got.OutcomeMatchCritic)
	assert.Empty(t, got.TimepointMatchCritic)
}

// Writing a changed critique through to the cache keeps the CheckedAt of an
// entry that exists (the critique was made then); a new entry gets the time
// of the write.
func TestFollowUpKeepsTheCheckedAtOfACachedCritique(t *testing.T) {
	earlier := testNow.Add(-5 * 24 * time.Hour)
	cachedPaper, newPaper := ep(doiA), ep(doiB)

	s := repliesBy(followUpEffectReply)
	r := newFollowUpRun(t, s, quietLog(), []model.EnrichedPaper{cachedPaper, newPaper},
		[]model.Critique{directNoEffect(doiA), directNoEffect(doiB)})

	seeded := CritiqueCache{
		critiqueCacheKey(cachedPaper, followUpQuestion): {CheckedAt: earlier, Critique: directNoEffect(doiA)},
	}
	require.NoError(t, seeded.Save(r.cachePath))

	require.NoError(t, r.p.followUpEffects(t.Context(), r.rp, r.st))

	cache := LoadCritiqueCache(r.cachePath, ttl30d, testNow, quietLog())

	old := cache[critiqueCacheKey(cachedPaper, followUpQuestion)]
	assert.Equal(t, earlier, old.CheckedAt, "the cached critique is as old as it was")
	assert.True(t, old.Critique.EffectAsked, "but it carries the change")
	assert.NotNil(t, old.Critique.Effect)

	fresh := cache[critiqueCacheKey(newPaper, followUpQuestion)]
	assert.Equal(t, testNow, fresh.CheckedAt, "a new entry is stamped now")
}

// An explicit null is an answer: the study keeps no effect, is marked as
// asked, and neither a second pass nor a resume asks again.
func TestFollowUpNullAnswerIsRemembered(t *testing.T) {
	s := repliesBy(followUpNullReply)
	papers := []model.EnrichedPaper{ep(doiA)}
	r := newFollowUpRun(t, s, quietLog(), papers, []model.Critique{directNoEffect(doiA)})

	require.NoError(t, r.p.followUpEffects(t.Context(), r.rp, r.st))

	assert.Nil(t, r.st.Critiques[0].Effect)
	assert.True(t, r.st.Critiques[0].EffectAsked)
	assert.Equal(t, 1, s.count())

	cached, ok := r.cached(t, papers[0])
	require.True(t, ok)
	assert.True(t, cached.EffectAsked, "the cache entry carries the mark")

	require.NoError(t, r.p.followUpEffects(t.Context(), r.rp, r.st))

	assert.Equal(t, 1, s.count(), "asked once")
}

// The follow-ups of one run are capped; the rest keep no effect, with a warning.
func TestFollowUpCap(t *testing.T) {
	const total = effectFollowUpMax + 3

	papers := make([]model.EnrichedPaper, total)
	crits := make([]model.Critique, total)

	for i := range papers {
		doi := fmt.Sprintf("10.1/cap-%02d", i)
		papers[i], crits[i] = ep(doi), directNoEffect(doi)
	}

	s := repliesBy(followUpEffectReply)
	log, logs := logBuffer()
	r := newFollowUpRun(t, s, log, papers, crits)

	require.NoError(t, r.p.followUpEffects(t.Context(), r.rp, r.st))

	assert.Equal(t, effectFollowUpMax, s.count())
	assert.Equal(t, effectFollowUpMax, r.st.Cost.Critique.Calls)
	assert.Contains(t, logs.String(), "effect follow-up capped")

	for i := range crits {
		if i < effectFollowUpMax {
			assert.NotNil(t, r.st.Critiques[i].Effect, "study %d is within the cap", i)
		} else {
			assert.Nil(t, r.st.Critiques[i].Effect, "study %d is past the cap", i)
		}
	}
}

// A backend out of quota stops the stage with the user-facing error; what the
// finished calls filled stays in the critiques and the cache.
func TestFollowUpStopsOnLLMStop(t *testing.T) {
	s := &stopCompleter{reply: func(call int, _ string) (string, error) {
		if call == 1 {
			return followUpEffectReply, nil
		}

		return "", fmt.Errorf("usage limit reached: %w", ErrLLMStop)
	}}
	papers := []model.EnrichedPaper{ep(doiA), ep(doiB)}
	crits := []model.Critique{directNoEffect(doiA), directNoEffect(doiB)}
	p := newFollowUpRun(t, s, quietLog(), papers, crits)
	p.p = newTest(t, Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d}, Clients{LLM: s}, quietLog())

	err := p.p.followUpEffects(t.Context(), p.rp, p.st)

	requireStopError(t, err)

	assert.NotNil(t, p.st.Critiques[0].Effect, "the finished call is kept")
	assert.Nil(t, p.st.Critiques[1].Effect)
	assert.False(t, p.st.Critiques[1].EffectAsked, "a stopped study is asked again on a resume")

	_, ok := p.cached(t, papers[0])
	assert.True(t, ok, "the finished follow-up is cached")
}

// No critic client: the stage tail is a no-op.
func TestFollowUpWithoutClient(t *testing.T) {
	r := newFollowUpRun(t, nil, quietLog(), []model.EnrichedPaper{ep(doiA)}, []model.Critique{directNoEffect(doiA)})
	r.p.clients.LLM = nil

	require.NoError(t, r.p.followUpEffects(t.Context(), r.rp, r.st))
	assert.Nil(t, r.st.Critiques[0].Effect)
}

// The whole stage: the critic's batch returns no effect for a direct study, the
// follow-up fills it before the artifact is saved, the cache carries it, and a
// second stage over the same corpus (a resume) asks nothing.
func TestCritiqueStageFollowsUpMissingEffect(t *testing.T) {
	reply := func(_ int, user string) (string, error) {
		if strings.Contains(user, "Extract the effect from this study") {
			return followUpEffectReply, nil
		}

		return stopBatchReply(doiA, doiB), nil
	}
	s := &stopCompleter{reply: reply}
	tmp := t.TempDir()
	opts := Options{RunDir: filepath.Join(tmp, "run"), TextMode: TextLean}
	papers := []model.EnrichedPaper{ep(doiA), ep(doiB)}

	run := func(s *stopCompleter) *State {
		p := newTest(t, Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d}, Clients{LLM: s}, quietLog())
		st := &State{Question: followUpQuestion, Enriched: papers}

		require.NoError(t, p.critiqueStage(t.Context(), params(opts), st))

		return st
	}

	st := run(s)

	assert.Equal(t, 3, s.count(), "one batch call and one follow-up per study without numbers")

	for i := range st.Critiques {
		require.NotNil(t, st.Critiques[i].Effect, "study %d", i)
	}

	assert.Equal(t, 3, st.Cost.Critique.Calls)

	s2 := &stopCompleter{reply: reply}
	st2 := run(s2)

	assert.Zero(t, s2.count(), "the cache serves the critiques with their effects")
	require.NotNil(t, st2.Critiques[0].Effect)
}

// The follow-up runs after the hybrid deepening: a deep re-critique that drops
// the effect is asked again for it.
func TestCritiqueStageFollowsUpAfterHybrid(t *testing.T) {
	deepReply := `{"critiques":[{"doi":"` + doiA + `","design_quality":"high","relevance":"direct",` +
		`"result_direction":"positive"}]}`
	abstractReply := `{"critiques":[{"doi":"` + doiA + `","design_quality":"high","relevance":"direct",` +
		`"result_direction":"positive","effect":{"intervention":{"events":3,"total":50},` +
		`"control":{"events":9,"total":50}}}]}`

	s := &stopCompleter{reply: func(call int, user string) (string, error) {
		switch {
		case strings.Contains(user, "Extract the effect from this study"):
			return followUpEffectReply, nil
		case call == 1:
			return abstractReply, nil
		}

		return deepReply, nil
	}}

	tmp := t.TempDir()
	opts := Options{RunDir: filepath.Join(tmp, "run"), TextMode: TextHybrid}
	p := newTest(t, Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d, HybridPivotalMax: 3},
		Clients{LLM: s, FullTexts: &fakeFullTexts{texts: map[string]string{doiA: "FULL TEXT OF A"}}}, quietLog())
	st := &State{Question: followUpQuestion, Enriched: []model.EnrichedPaper{ep(doiA)}}

	require.NoError(t, p.critiqueStage(t.Context(), params(opts), st))

	require.NotNil(t, st.Critiques[0].Effect, "the deep critique dropped the effect, the follow-up restored it")
	assert.True(t, st.HybridDone)
	assert.Contains(t, s.prompts[len(s.prompts)-1], "Extract the effect from this study",
		"the follow-up is the last call")
}
