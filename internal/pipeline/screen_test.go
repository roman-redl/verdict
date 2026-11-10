package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// screenFake is the screening model: it counts its calls and answers from a
// function of the call number and the prompt. Safe for parallel batches.
type screenFake struct {
	mu      sync.Mutex
	calls   int
	prompts []string
	fn      func(call int, user string) (string, error)
}

var _ Completer = (*screenFake)(nil)

func (f *screenFake) Name() string { return "screen-fake" }

func (f *screenFake) Complete(_ context.Context, _, user string, _ CompleteOptions) (string, Usage, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.prompts = append(f.prompts, user)
	f.mu.Unlock()

	text, err := f.fn(call, user)

	return text, Usage{InputTokens: 10, OutputTokens: 5}, err
}

// batchItems reads the candidates the prompt carries.
func batchItems(t *testing.T, user string) []screenItem {
	t.Helper()

	_, payload, ok := strings.Cut(user, "Candidates:\n")
	require.True(t, ok, "the prompt lists the candidates")

	payload, _, _ = strings.Cut(payload, "\n\nRespond")

	var items []screenItem
	require.NoError(t, json.Unmarshal([]byte(payload), &items))

	return items
}

// answerBy builds the model's reply from a rule over each candidate: whether
// to include it and which design to guess.
func answerBy(t *testing.T, rule func(screenItem) (bool, string)) func(int, string) (string, error) {
	t.Helper()

	return func(_ int, user string) (string, error) {
		items := batchItems(t, user)
		decisions := make([]map[string]any, 0, len(items))

		for _, it := range items {
			include, design := rule(it)
			decisions = append(decisions, map[string]any{"id": it.ID, "include": include, "design": design, "reason": "r"})
		}

		b, err := json.Marshal(map[string]any{"decisions": decisions})

		return string(b), err
	}
}

func pooled(n int) []Candidate {
	pool := make([]Candidate, n)
	for i := range pool {
		pool[i] = newCandidate(model.Paper{
			DOI: fmt.Sprintf("10.1/p%d", i), Title: fmt.Sprintf("Title %d", i),
			Abstract: abstractText, Source: model.SourceConsensus,
		}, FoundBy{Source: model.SourceConsensus, Query: 1, Rank: i + 1})
	}

	return pool
}

func screenPipeline(t *testing.T, fake *screenFake, concurrency int) *Pipeline {
	t.Helper()

	return newTest(t, Settings{LLMConcurrency: concurrency}, Clients{LLMPlan: fake}, quietLog())
}

func TestScreenPoolAppliesTheDecisions(t *testing.T) {
	fake := &screenFake{fn: answerBy(t, func(it screenItem) (bool, string) {
		switch {
		case strings.Contains(it.Title, "editorial"):
			return false, guessOther
		case strings.Contains(it.Title, "review"):
			return true, "Review"
		}

		return true, guessRCT
	})}
	pool := pooled(3)
	pool[0].Title = "A trial"
	pool[1].Title = "An editorial"
	pool[2].Title = "A review of trials"
	st := &State{Question: "does creatine help?"}
	info := &DiscoveryInfo{Pool: pool}

	require.NoError(t, screenPipeline(t, fake, 1).screenPool(t.Context(), st, info))

	require.Equal(t, 1, fake.calls)
	assert.Equal(t, &ScreenDecision{Include: true, Design: ScreenRCT, Reason: "r"}, pool[0].Screen)
	assert.False(t, pool[1].Screen.Include)
	assert.Equal(t, ScreenOther, pool[1].Screen.Design)
	assert.Equal(t, ScreenReview, pool[2].Screen.Design, "the design is parsed leniently")

	// the model sees the question, the title, the year and a short snippet — nothing else
	prompt := fake.prompts[0]
	assert.Contains(t, prompt, "does creatine help?")
	assert.Contains(t, prompt, "A trial")
	assert.Contains(t, prompt, `"snippet":"the abstract"`)
	assert.NotContains(t, prompt, "10.1/p0", "the DOI is not part of the judgment")

	require.NotNil(t, st.Cost)
	assert.Equal(t, &StageCost{Calls: 1, InputTokens: 10, OutputTokens: 5}, st.Cost.Screening, "the cost record")
	assert.Empty(t, st.Warnings)
}

func TestScreenPoolBatches(t *testing.T) {
	fake := &screenFake{fn: answerBy(t, func(screenItem) (bool, string) { return true, guessRCT })}
	pool := pooled(2*screenBatchSize + 20)
	st := &State{Question: "q"}
	log, buf := logBuffer()
	p := newTest(t, Settings{LLMConcurrency: 3}, Clients{LLMPlan: fake}, log)

	require.NoError(t, p.screenPool(t.Context(), st, &DiscoveryInfo{Pool: pool}))

	assert.Equal(t, 3, fake.calls, "50 + 50 + 20 candidates")
	assert.Equal(t, 3, st.Cost.Screening.Calls)
	assert.Contains(t, buf.String(), "llm_calls=3", "the number of screening calls is logged")
	assert.Contains(t, buf.String(), "candidates=120")

	sizes := map[int]bool{}

	for _, user := range fake.prompts {
		items := batchItems(t, user)
		sizes[len(items)] = true
		assert.Equal(t, 1, items[0].ID, "ids restart in every batch")
	}

	assert.Equal(t, map[int]bool{screenBatchSize: true, 20: true}, sizes)

	for i := range pool {
		require.NotNil(t, pool[i].Screen, "candidate %d", i)
	}
}

// A decision the pool cannot use leaves its candidate unscreened.
func TestScreenPoolDropsUnusableDecisions(t *testing.T) {
	reply := `{"decisions":[
		{"id":1,"include":true,"design":"rct","reason":"ok"},
		{"id":1,"include":false,"design":"other","reason":"a repeat of id 1"},
		{"id":2,"design":"rct","reason":"no include field"},
		{"id":99,"include":true,"design":"rct","reason":"no such candidate"},
		{"id":3,"include":true,"design":"cohort","reason":"a design outside the list"},
		{"id":4,"include":false,"design":"weird","reason":"excluded: the design does not matter"}
	]}`
	fake := &screenFake{fn: func(int, string) (string, error) { return reply, nil }}
	pool := pooled(5)
	st := &State{Question: "q"}

	require.NoError(t, screenPipeline(t, fake, 1).screenPool(t.Context(), st, &DiscoveryInfo{Pool: pool}))

	require.NotNil(t, pool[0].Screen)
	assert.True(t, pool[0].Screen.Include, "the first decision wins")
	assert.Nil(t, pool[1].Screen, "no include: no decision")
	require.NotNil(t, pool[2].Screen)
	assert.Equal(t, ScreenOtherPrimary, pool[2].Screen.Design, "an included candidate with an unreadable design")
	assert.Equal(t, ScreenOther, pool[3].Screen.Design)
	assert.Nil(t, pool[4].Screen, "the model skipped it")
	assert.Contains(t, fmt.Sprint(st.Warnings), "2 of 5 candidates unscreened")
}

// A failed call never fails the stage: the batch stays unscreened, the cost
// of the call is still recorded.
func TestScreenPoolSurvivesAFailedBatch(t *testing.T) {
	fake := &screenFake{fn: func(call int, user string) (string, error) {
		if call == 1 {
			return "", errors.New("exec: claude-max: exit status 1")
		}

		return answerBy(t, func(screenItem) (bool, string) { return false, guessOther })(call, user)
	}}
	pool := pooled(screenBatchSize + 5)
	st := &State{Question: "q"}
	log, buf := logBuffer()
	p := newTest(t, Settings{LLMConcurrency: 1}, Clients{LLMPlan: fake}, log)

	require.NoError(t, p.screenPool(t.Context(), st, &DiscoveryInfo{Pool: pool}))

	assert.Nil(t, pool[0].Screen, "the first batch failed")
	require.NotNil(t, pool[screenBatchSize].Screen, "the second batch was screened")
	assert.Contains(t, buf.String(), "screening batch failed")
	assert.Contains(t, fmt.Sprint(st.Warnings), "50 of 55 candidates unscreened")
	assert.Equal(t, 2, st.Cost.Screening.Calls, "the failed call is accounted like the one that worked")
}

// A usage limit does not fail discovery either — the critique stage stops on
// it, and the saved artifact spares a repeat of the paid search — but the
// remaining batches are not tried.
func TestScreenPoolStopsTryingAfterTheUsageLimit(t *testing.T) {
	fake := &screenFake{fn: func(int, string) (string, error) { return "", fmt.Errorf("limit: %w", ErrLLMStop) }}
	pool := pooled(3 * screenBatchSize)
	st := &State{Question: "q"}

	require.NoError(t, screenPipeline(t, fake, 1).screenPool(t.Context(), st, &DiscoveryInfo{Pool: pool}))

	assert.Equal(t, 1, fake.calls, "the limit ends the screening")
	assert.Equal(t, 1, st.Cost.Screening.Calls)

	for i := range pool {
		assert.Nil(t, pool[i].Screen)
	}
}

func TestScreenPoolWithoutAModelDoesNothing(t *testing.T) {
	pool := pooled(2)
	st := &State{Question: "q"}
	p := newTest(t, Settings{}, Clients{}, quietLog())

	require.NoError(t, p.screenPool(t.Context(), st, &DiscoveryInfo{Pool: pool}))

	assert.Nil(t, pool[0].Screen)
	assert.Nil(t, st.Cost, "no model, no cost")
	assert.Empty(t, st.Warnings, "skipping the screening is a configuration, not a failure")
}
