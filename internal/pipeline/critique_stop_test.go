package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// stopCompleter replies from reply(call, user); a nil reply is a backend
// that always answers with ErrLLMStop (an exhausted quota). Safe for the
// parallel critique workers.
type stopCompleter struct {
	reply func(call int, user string) (string, error)

	mu      sync.Mutex
	prompts []string
}

var _ Completer = (*stopCompleter)(nil)

func (s *stopCompleter) Name() string { return "stop" }

func (s *stopCompleter) Complete(_ context.Context, _, user string, _ CompleteOptions) (string, Usage, error) {
	s.mu.Lock()
	s.prompts = append(s.prompts, user)
	call := len(s.prompts)
	s.mu.Unlock()

	if s.reply == nil {
		return "", Usage{}, fmt.Errorf("usage limit reached: %w", ErrLLMStop)
	}

	text, err := s.reply(call, user)

	return text, Usage{Calls: 1, InputTokens: 10, OutputTokens: 5}, err
}

func (s *stopCompleter) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.prompts)
}

func (s *stopCompleter) singles() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0

	for _, p := range s.prompts {
		if strings.Contains(p, "Assess this study") {
			n++
		}
	}

	return n
}

func studies(dois ...string) []model.EnrichedPaper {
	out := make([]model.EnrichedPaper, len(dois))
	for i, d := range dois {
		out[i] = ep(d)
	}

	return out
}

func stopBatchReply(dois ...string) string {
	items := make([]string, len(dois))
	for i, d := range dois {
		items[i] = `{"doi":"` + d + `","design_quality":"high","relevance":"direct","result_direction":"positive"}`
	}

	return `{"critiques":[` + strings.Join(items, ",") + `]}`
}

func requireStopError(t *testing.T, err error) {
	t.Helper()

	ue, ok := errors.AsType[*UserError](err)
	require.True(t, ok, "the stage must return a user-facing error, got %v", err)
	assert.Contains(t, ue.Msg, "--resume")
	assert.Contains(t, ue.Msg, "usage limit")
}

func critiqueStopRun(t *testing.T, s *stopCompleter, concurrency, chunk int, papers []model.EnrichedPaper) (
	cachePath string, st *State, err error,
) {
	t.Helper()

	tmp := t.TempDir()
	cachePath = filepath.Join(tmp, "critique-cache.json")
	p := newTest(t, Settings{LLMConcurrency: concurrency, LLMChunk: chunk, CritiqueCacheTTL: ttl30d},
		Clients{LLM: s}, quietLog())
	st = &State{Question: "q?", Enriched: papers}

	err = p.critique(t.Context(), params(Options{RunDir: filepath.Join(tmp, "run")}), st)

	return cachePath, st, err
}

// A backend out of quota stops the stage at the first call: no single-call
// fallback, the remaining chunks never start, nothing blank reaches the cache.
func TestCritiqueStopsOnLLMStop(t *testing.T) {
	s := &stopCompleter{}
	cachePath, st, err := critiqueStopRun(t, s, 1, 2, studies(doiA, doiB, "10.1/c", "10.1/d", "10.1/e", "10.1/f"))

	requireStopError(t, err)
	assert.Equal(t, 1, s.count(), "one call for the first chunk, none for the rest")
	assert.Zero(t, s.singles(), "no single-call fallback after a stop")
	assert.Nil(t, st.Critiques, "a stopped stage publishes no critiques")
	assert.Empty(t, LoadCritiqueCache(cachePath, ttl30d, testNow, quietLog()), "blank placeholders must not be cached")
}

// With parallel workers every started chunk makes at most its one batch call.
func TestCritiqueStopParallelChunks(t *testing.T) {
	s := &stopCompleter{}
	_, _, err := critiqueStopRun(t, s, 3, 2, studies(doiA, doiB, "10.1/c", "10.1/d", "10.1/e", "10.1/f"))

	requireStopError(t, err)
	assert.LessOrEqual(t, s.count(), 3, "at most one call per started chunk")
	assert.Zero(t, s.singles(), "no single-call fallback after a stop")
}

// The stop comes from a single-call fallback: the studies already assessed
// stay in the cache, the rest are neither cached nor asked.
func TestCritiqueStopInSingleFallback(t *testing.T) {
	s := &stopCompleter{reply: func(call int, _ string) (string, error) {
		if call == 1 {
			return stopBatchReply(doiA), nil // the batch drops B and C
		}

		return "", fmt.Errorf("weekly limit: %w", ErrLLMStop)
	}}
	cachePath, _, err := critiqueStopRun(t, s, 1, 3, studies(doiA, doiB, "10.1/c"))

	requireStopError(t, err)
	assert.Equal(t, 2, s.count(), "the batch plus the one single call that hit the stop")
	assert.Equal(t, 1, s.singles())

	cache := LoadCritiqueCache(cachePath, ttl30d, testNow, quietLog())
	assert.Contains(t, cache, critiqueCacheKey(ep(doiA), "q?"), "the finished study is committed")
	assert.Len(t, cache, 1, "the failed and the unasked studies are not")
}

// Chunks finished before the stop reach the cache; the stopped one does not.
func TestCritiqueStopCommitsFinishedChunks(t *testing.T) {
	s := &stopCompleter{reply: func(call int, _ string) (string, error) {
		if call == 1 {
			return stopBatchReply(doiA, doiB), nil
		}

		return "", fmt.Errorf("limit reached: %w", ErrLLMStop)
	}}
	cachePath, _, err := critiqueStopRun(t, s, 1, 2, studies(doiA, doiB, "10.1/c", "10.1/d", "10.1/e", "10.1/f"))

	requireStopError(t, err)
	assert.Equal(t, 2, s.count(), "the first chunk, then the stop; the third chunk never starts")

	cache := LoadCritiqueCache(cachePath, ttl30d, testNow, quietLog())
	assert.Len(t, cache, 2)
	assert.Contains(t, cache, critiqueCacheKey(ep(doiA), "q?"))
	assert.Contains(t, cache, critiqueCacheKey(ep(doiB), "q?"))
}

// An ordinary failure still degrades per study; only a stop ends the stage.
func TestCritiqueOrdinaryFailureStillFallsBack(t *testing.T) {
	s := &stopCompleter{reply: func(int, string) (string, error) { return "", errors.New("flaky") }}
	_, st, err := critiqueStopRun(t, s, 1, 2, studies(doiA, doiB))

	require.NoError(t, err)
	assert.Equal(t, 3, s.count(), "the failed batch plus one single call per study")
	require.Len(t, st.Critiques, 2)
	assert.NotEmpty(t, st.Critiques[0].Error)
}

// The synthesis stage turns a stop into the same user-facing error.
func TestSynthesisStopIsAUserError(t *testing.T) {
	p := newTest(t, Settings{}, Clients{LLM: &stopCompleter{}}, quietLog())
	st := &State{Question: "q?", Enriched: studies(doiA), Critiques: []model.Critique{
		crit(doiA, qualityHigh, relevanceDirect, directionPositive),
	}}

	requireStopError(t, p.synthesize(t.Context(), params(Options{}), st))
}
