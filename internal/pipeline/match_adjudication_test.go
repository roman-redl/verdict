package pipeline

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	matchDOI      = "10.1161/circulationaha.115.019406"
	matchNotJSON  = "this is not json at all"
	matchBadLabel = `{"matches":[{"index":0,"outcome_match":"similar","timepoint_match":"yes"}]}`
)

// voteReply is one scripted answer of the vote calls: the same labels for every
// candidate, or a raw text.
type voteReply struct {
	outcome, timepoint string
	raw                string
}

func (v voteReply) render(n int) string {
	if v.raw != "" {
		return v.raw
	}

	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"index":%d,"outcome_match":%q,"timepoint_match":%q}`, i, v.outcome, v.timepoint)
	}

	return `{"matches":[` + strings.Join(items, ",") + `]}`
}

// scriptedVotes answers call k with the k-th reply; the candidates of the call
// are counted from its payload. Calls are numbered in arrival order, so a test
// that scripts them runs the stage with one worker.
func scriptedVotes(replies ...voteReply) func(int, string) (string, error) {
	return func(call int, user string) (string, error) {
		if call > len(replies) {
			return "", fmt.Errorf("unexpected call %d", call)
		}

		return replies[call-1].render(strings.Count(user, `"index":`)), nil
	}
}

func votes(outcome, timepoint string) voteReply {
	return voteReply{outcome: outcome, timepoint: timepoint}
}

// matchCrit is the critique of a direct trial with usable numbers and the
// critic's own labels.
func matchCrit(doi string, om model.OutcomeMatch, tm model.TimepointMatch) model.Critique {
	c := directNoEffect(doi)
	c.Effect = &model.Effect{
		Outcome: "Freedom from AF recurrence", Timepoint: "12 months", TimepointWeeks: new(52.0),
		OutcomeMatch: om, TimepointMatch: tm,
		Intervention: &model.Arm{Events: new(3.0), Total: new(50.0)},
		Control:      &model.Arm{Events: new(9.0), Total: new(50.0)},
	}

	return c
}

func newMatchRun(
	t *testing.T, s Completer, log *slog.Logger, papers []model.EnrichedPaper, crits []model.Critique,
) followUpRun {
	t.Helper()

	opts := Options{RunDir: filepath.Join(t.TempDir(), "run")}

	return followUpRun{
		p: newTest(t, Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d}, Clients{LLMPlan: s}, log),
		st: &State{
			Question: followUpQuestion, Enriched: papers, Critiques: crits,
		},
		rp:        params(opts),
		cachePath: critiqueCachePath(opts),
	}
}

func scripted(replies ...voteReply) *stopCompleter {
	return &stopCompleter{reply: scriptedVotes(replies...)}
}

// Two of three votes against the critic's label win, and the critic's own label
// is kept next to the new one; the call count, the cost and the cache follow.
func TestAdjudicationAppliesMajority(t *testing.T) {
	s := scripted(votes("same", "yes"), votes("related", "yes"), votes("same", "yes"))
	papers := []model.EnrichedPaper{ep(matchDOI)}
	r := newMatchRun(t, s, quietLog(), papers,
		[]model.Critique{matchCrit(matchDOI, model.OutcomeRelated, model.TimepointUnclear)})

	require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))

	assert.Equal(t, matchVotes, s.count())

	got := r.st.Critiques[0]
	assert.Equal(t, model.OutcomeSame, got.Effect.OutcomeMatch)
	assert.Equal(t, model.OutcomeRelated, got.Effect.OutcomeMatchCritic)
	assert.Equal(t, model.TimepointYes, got.Effect.TimepointMatch)
	assert.Equal(t, model.TimepointUnclear, got.Effect.TimepointMatchCritic)
	assert.True(t, got.MatchAdjudicated)

	require.NotNil(t, r.st.Cost)
	assert.Equal(t, matchVotes, r.st.Cost.Critique.Calls, "the votes are calls of the critique stage")

	cached, ok := r.cached(t, papers[0])
	require.True(t, ok, "the adjudicated critique must reach the cache")
	assert.True(t, cached.MatchAdjudicated)
	assert.Equal(t, model.OutcomeSame, cached.Effect.OutcomeMatch)

	// the payload: the question and the effect's own outcome and time point, no abstract, no critic labels
	prompt := s.prompts[0]
	assert.Contains(t, prompt, followUpQuestion)
	assert.Contains(t, prompt, "Freedom from AF recurrence")
	assert.Contains(t, prompt, "12 months")
	assert.Contains(t, prompt, `"timepoint_weeks":52`)
	assert.Contains(t, prompt, matchDOI)
	assert.NotContains(t, prompt, ep(matchDOI).Abstract)
	assert.NotContains(t, prompt, "related", "the critic's label must not anchor the vote")
}

// The majority is counted per candidate and per label: a candidate whose
// label the votes confirm keeps no "critic" field.
func TestAdjudicationPerCandidateAndLabel(t *testing.T) {
	reply := func(a, b, c, d string) voteReply {
		return voteReply{raw: `{"matches":[` +
			`{"index":0,"outcome_match":"` + a + `","timepoint_match":"` + b + `"},` +
			`{"index":1,"outcome_match":"` + c + `","timepoint_match":"` + d + `"}]}`}
	}
	s := scripted(reply("same", "no", "related", "yes"), reply("same", "yes", "related", "yes"),
		reply("different", "no", "different", "yes"))
	papers := []model.EnrichedPaper{ep(doiA), ep(doiB)}
	r := newMatchRun(t, s, quietLog(), papers, []model.Critique{
		matchCrit(doiA, model.OutcomeRelated, model.TimepointYes),
		matchCrit(doiB, model.OutcomeRelated, model.TimepointYes),
	})

	require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))

	a, b := r.st.Critiques[0].Effect, r.st.Critiques[1].Effect

	assert.Equal(t, model.OutcomeSame, a.OutcomeMatch)
	assert.Equal(t, model.TimepointNo, a.TimepointMatch)
	assert.Equal(t, model.TimepointYes, a.TimepointMatchCritic)

	assert.Equal(t, model.OutcomeRelated, b.OutcomeMatch, "the votes confirm the critic")
	assert.Empty(t, b.OutcomeMatchCritic, "nothing changed, nothing to show")
	assert.Equal(t, model.TimepointYes, b.TimepointMatch)
	assert.Empty(t, b.TimepointMatchCritic)
}

// Three different answers are no majority: the critic's label stays, and the
// study is still marked as adjudicated.
func TestAdjudicationNoMajorityKeepsCritic(t *testing.T) {
	s := scripted(votes("same", "yes"), votes("related", "no"), votes("different", "unclear"))
	r := newMatchRun(t, s, quietLog(), []model.EnrichedPaper{ep(doiA)},
		[]model.Critique{matchCrit(doiA, model.OutcomeRelated, model.TimepointYes)})

	require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))

	got := r.st.Critiques[0]
	assert.Equal(t, model.OutcomeRelated, got.Effect.OutcomeMatch)
	assert.Equal(t, model.TimepointYes, got.Effect.TimepointMatch)
	assert.Empty(t, got.Effect.OutcomeMatchCritic)
	assert.Empty(t, got.Effect.TimepointMatchCritic)
	assert.True(t, got.MatchAdjudicated)
}

// A missing critic label reads as "same" / "unclear": a vote that confirms the
// reading changes nothing.
func TestAdjudicationEmptyCriticLabel(t *testing.T) {
	s := scripted(votes("same", "unclear"), votes("same", "unclear"), votes("same", "unclear"))
	r := newMatchRun(t, s, quietLog(), []model.EnrichedPaper{ep(doiA)},
		[]model.Critique{matchCrit(doiA, "", "")})

	require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))

	got := r.st.Critiques[0].Effect
	assert.Empty(t, got.OutcomeMatch)
	assert.Empty(t, got.OutcomeMatchCritic)
	assert.Empty(t, got.TimepointMatchCritic)
}

// One malformed reply is no vote; the two valid replies that agree win. Each
// kind of malformed reply is tried: not JSON (the call is retried once, so it
// costs two calls), a label outside the vocabulary, a reply that misses a
// candidate.
func TestAdjudicationOneMalformedReply(t *testing.T) {
	for name, tc := range map[string]struct {
		bad   []voteReply // what the bad voter answers, call by call
		calls int
	}{
		"not json":      {bad: []voteReply{{raw: matchNotJSON}, {raw: matchNotJSON}}, calls: 4},
		"unknown label": {bad: []voteReply{{raw: matchBadLabel}}, calls: 3},
		"missing item":  {bad: []voteReply{{raw: `{"matches":[]}`}}, calls: 3},
	} {
		t.Run(name, func(t *testing.T) {
			replies := slices.Concat(tc.bad, []voteReply{votes("same", "yes"), votes("same", "yes")})
			s := scripted(replies...)
			log, logs := logBuffer()
			r := newMatchRun(t, s, log, []model.EnrichedPaper{ep(doiA)},
				[]model.Critique{matchCrit(doiA, model.OutcomeRelated, model.TimepointUnclear)})

			require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))

			assert.Equal(t, tc.calls, s.count())

			got := r.st.Critiques[0]
			assert.Equal(t, model.OutcomeSame, got.Effect.OutcomeMatch)
			assert.Equal(t, model.TimepointYes, got.Effect.TimepointMatch)
			assert.True(t, got.MatchAdjudicated)
			assert.Equal(t, tc.calls, r.st.Cost.Critique.Calls, "every call, the retry included, is counted")
			assert.Contains(t, logs.String(), "no vote")
		})
	}
}

// Two malformed replies leave fewer than two valid ones: the critic's labels
// stay, a warning is logged, and nothing is marked or cached, so a later pass
// may try again.
func TestAdjudicationTwoMalformedKeepCritic(t *testing.T) {
	s := scripted(voteReply{raw: matchBadLabel}, voteReply{raw: matchBadLabel}, votes("same", "yes"))
	log, logs := logBuffer()
	papers := []model.EnrichedPaper{ep(doiA)}
	r := newMatchRun(t, s, log, papers,
		[]model.Critique{matchCrit(doiA, model.OutcomeRelated, model.TimepointUnclear)})

	require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))

	got := r.st.Critiques[0]
	assert.Equal(t, model.OutcomeRelated, got.Effect.OutcomeMatch)
	assert.Equal(t, model.TimepointUnclear, got.Effect.TimepointMatch)
	assert.Empty(t, got.Effect.OutcomeMatchCritic)
	assert.False(t, got.MatchAdjudicated)
	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), "too few valid replies")

	_, ok := r.cached(t, papers[0])
	assert.False(t, ok, "nothing was learned, nothing is cached")

	// the study is a candidate again
	assert.True(t, needsMatchAdjudication(&papers[0], got))
}

// Two valid replies that disagree are no majority of anything.
func TestAdjudicationTwoValidRepliesDisagree(t *testing.T) {
	s := scripted(votes("same", "yes"), votes("related", "yes"), voteReply{raw: matchBadLabel})
	r := newMatchRun(t, s, quietLog(), []model.EnrichedPaper{ep(doiA)},
		[]model.Critique{matchCrit(doiA, model.OutcomeDifferent, model.TimepointNo)})

	require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))

	got := r.st.Critiques[0].Effect
	assert.Equal(t, model.OutcomeDifferent, got.OutcomeMatch)
	assert.Equal(t, model.TimepointYes, got.TimepointMatch, "the two valid replies agree on the time point")
	assert.Equal(t, model.TimepointNo, got.TimepointMatchCritic)
}

// Only direct primary studies with usable numbers are sent: not an indirect
// one, a review, a study without an effect or with placeholder numbers, a
// failed or retracted one, one voted on before.
func TestAdjudicationSendsOnlyEligibleStudies(t *testing.T) {
	review := ep("10.1/reviewed")
	review.StudyDesign = tagMetaAnalysis

	retracted := ep("10.1/retracted")
	retracted.Flags.Retracted = true

	indirect := matchCrit("10.1/indirect", model.OutcomeSame, model.TimepointYes)
	indirect.Relevance = relevanceIndirect
	noEffect := directNoEffect("10.1/noeffect")
	placeholder := matchCrit("10.1/zeros", model.OutcomeSame, model.TimepointYes)
	placeholder.Effect.Intervention = &model.Arm{Events: new(0.0), Total: new(0.0)}
	placeholder.Effect.Control = &model.Arm{Events: new(0.0), Total: new(0.0)}
	failed := matchCrit("10.1/failed", model.OutcomeSame, model.TimepointYes)
	failed.Error = "boom"
	done := matchCrit("10.1/done", model.OutcomeSame, model.TimepointYes)
	done.MatchAdjudicated = true

	papers := []model.EnrichedPaper{
		ep("10.1/indirect"), review, ep("10.1/noeffect"), ep("10.1/zeros"), ep("10.1/failed"), retracted,
		ep("10.1/done"), ep(doiA),
	}
	crits := []model.Critique{
		indirect, matchCrit("10.1/reviewed", model.OutcomeSame, model.TimepointYes), noEffect, placeholder, failed,
		matchCrit("10.1/retracted", model.OutcomeSame, model.TimepointYes), done,
		matchCrit(doiA, model.OutcomeSame, model.TimepointYes),
	}

	s := scripted(votes("same", "yes"), votes("same", "yes"), votes("same", "yes"))
	r := newMatchRun(t, s, quietLog(), papers, crits)

	require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))

	require.Equal(t, matchVotes, s.count())

	for _, p := range s.prompts {
		assert.Equal(t, 1, strings.Count(p, `"index":`), "only one study qualifies")
		assert.Contains(t, p, doiA)

		for _, other := range []string{"indirect", "reviewed", "noeffect", "zeros", "failed", "retracted", "done"} {
			assert.NotContains(t, p, "10.1/"+other)
		}
	}
}

// The marker keeps a second pass (a resume, a re-run served from the cache)
// from voting again.
func TestAdjudicationMarkerPreventsSecondRun(t *testing.T) {
	s := scripted(votes("same", "yes"), votes("same", "yes"), votes("same", "yes"))
	r := newMatchRun(t, s, quietLog(), []model.EnrichedPaper{ep(doiA)},
		[]model.Critique{matchCrit(doiA, model.OutcomeRelated, model.TimepointYes)})

	require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))
	require.Equal(t, matchVotes, s.count())

	require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))
	assert.Equal(t, matchVotes, s.count(), "voted once")
	assert.Equal(t, matchVotes, r.st.Cost.Critique.Calls)
}

// More than matchCandidatesMax candidates are split into rounds of
// matchVotes calls each; every round is voted on separately.
func TestAdjudicationRoundsOverTheCap(t *testing.T) {
	const total = matchCandidatesMax + 1

	papers := make([]model.EnrichedPaper, total)
	crits := make([]model.Critique, total)

	for i := range papers {
		doi := fmt.Sprintf("10.1/round-%02d", i)
		papers[i], crits[i] = ep(doi), matchCrit(doi, model.OutcomeRelated, model.TimepointUnclear)
	}

	same := votes("same", "yes")
	s := scripted(same, same, same, same, same, same)
	r := newMatchRun(t, s, quietLog(), papers, crits)

	require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))

	assert.Equal(t, 2*matchVotes, s.count())
	assert.Equal(t, 2*matchVotes, r.st.Cost.Critique.Calls)

	sizes := map[int]int{}
	for _, p := range s.prompts {
		sizes[strings.Count(p, `"index":`)]++
	}

	assert.Equal(t, map[int]int{matchCandidatesMax: matchVotes, 1: matchVotes}, sizes)

	for i := range crits {
		assert.Equal(t, model.OutcomeSame, r.st.Critiques[i].Effect.OutcomeMatch, "study %d", i)
		assert.True(t, r.st.Critiques[i].MatchAdjudicated, "study %d", i)
	}
}

// A backend out of quota stops the stage with the user-facing error: a half
// vote is not applied, the calls made are counted, nothing is marked.
func TestAdjudicationStopsOnLLMStop(t *testing.T) {
	s := &stopCompleter{reply: func(call int, user string) (string, error) {
		if call == 1 {
			return votes("same", "yes").render(strings.Count(user, `"index":`)), nil
		}

		return "", fmt.Errorf("usage limit reached: %w", ErrLLMStop)
	}}
	papers := []model.EnrichedPaper{ep(doiA)}
	r := newMatchRun(t, s, quietLog(), papers,
		[]model.Critique{matchCrit(doiA, model.OutcomeRelated, model.TimepointUnclear)})

	err := r.p.adjudicateMatches(t.Context(), r.rp, r.st)

	requireStopError(t, err)

	got := r.st.Critiques[0]
	assert.Equal(t, model.OutcomeRelated, got.Effect.OutcomeMatch, "a half vote is not applied")
	assert.False(t, got.MatchAdjudicated, "a resume votes again")
	assert.Equal(t, 2, s.count(), "the third call never started")
	assert.Equal(t, 2, r.st.Cost.Critique.Calls, "the calls made are counted")

	_, ok := r.cached(t, papers[0])
	assert.False(t, ok)
}

// Without a plan-role model there is no vote: the critic's labels stay, with a
// warning, and no call is made.
func TestAdjudicationWithoutPlanClient(t *testing.T) {
	log, logs := logBuffer()
	r := newMatchRun(t, nil, log, []model.EnrichedPaper{ep(doiA)},
		[]model.Critique{matchCrit(doiA, model.OutcomeRelated, model.TimepointYes)})
	r.p.clients.LLMPlan = nil

	require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))

	assert.Equal(t, model.OutcomeRelated, r.st.Critiques[0].Effect.OutcomeMatch)
	assert.Nil(t, r.st.Cost)
	assert.Contains(t, logs.String(), "outcome adjudication skipped")
}

// No candidate, no call and no cost.
func TestAdjudicationWithoutCandidates(t *testing.T) {
	s := scripted()
	r := newMatchRun(t, s, quietLog(), []model.EnrichedPaper{ep(doiA)}, []model.Critique{directNoEffect(doiA)})

	require.NoError(t, r.p.adjudicateMatches(t.Context(), r.rp, r.st))

	assert.Zero(t, s.count())
	assert.Nil(t, r.st.Cost)
}

// The vote prompt shares the critic's definitions of the two labels.
func TestAdjudicationPromptReusesCriticDefinitions(t *testing.T) {
	assert.Contains(t, matchAdjudicationSystemPrompt, critiqueOutcomeMatchDef)
	assert.Contains(t, matchAdjudicationSystemPrompt, critiqueTimepointMatchDef)
	assert.Contains(t, critiqueEffectField, critiqueOutcomeMatchDef)
	assert.Contains(t, critiqueEffectField, critiqueTimepointMatchDef)
	assert.Contains(t, batchCritiqueSystemPrompt, critiqueOutcomeMatchDef)
}

// The whole stage: the critic labels the effect "related", the vote of the
// plan-role model turns it into "same" before the artifact is saved, the
// critic's call is not used for the vote, and a second stage over the same
// corpus (a resume served from the cache) votes nothing.
func TestCritiqueStageAdjudicatesLabels(t *testing.T) {
	batch := `{"critiques":[{"doi":"` + doiA + `","design_quality":"high","relevance":"direct",` +
		`"result_direction":"positive","effect":{"outcome":"Freedom from AF recurrence","outcome_match":"related",` +
		`"intervention":{"events":3,"total":50},"control":{"events":9,"total":50}}}]}`
	critic := &stopCompleter{reply: func(int, string) (string, error) { return batch, nil }}
	papers := []model.EnrichedPaper{ep(doiA)}
	opts := Options{RunDir: filepath.Join(t.TempDir(), "run"), TextMode: TextLean}

	run := func(plan *stopCompleter) *State {
		p := newTest(t, Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d},
			Clients{LLM: critic, LLMPlan: plan}, quietLog())
		st := &State{Question: followUpQuestion, Enriched: papers}

		require.NoError(t, p.critiqueStage(t.Context(), params(opts), st))

		return st
	}

	plan := scripted(votes("same", "yes"), votes("same", "yes"), votes("same", "yes"))
	st := run(plan)

	assert.Equal(t, 1, critic.count(), "the critic is called once, for the critique")
	assert.Equal(t, matchVotes, plan.count())
	assert.Equal(t, model.OutcomeSame, st.Critiques[0].Effect.OutcomeMatch)
	assert.Equal(t, model.OutcomeRelated, st.Critiques[0].Effect.OutcomeMatchCritic)
	assert.Equal(t, 1+matchVotes, st.Cost.Critique.Calls)

	plan2 := scripted()
	critic2Before := critic.count()
	st2 := run(plan2)

	assert.Equal(t, critic2Before, critic.count(), "the cache serves the critique")
	assert.Zero(t, plan2.count(), "the cache carries the marker: no second vote")
	assert.Equal(t, model.OutcomeSame, st2.Critiques[0].Effect.OutcomeMatch)
}
