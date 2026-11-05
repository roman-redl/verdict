package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// writeRun stores a state as the artifact of the given stage in a new run
// directory under topic.
func writeRun(t *testing.T, topic, name, artifact string, st *State, age time.Duration) {
	t.Helper()

	dir := filepath.Join(topic, name)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, saveJSON(filepath.Join(dir, artifact), st))

	when := testNow.Add(-age)
	require.NoError(t, os.Chtimes(dir, when, when))
}

// The freshest artifact is the run's state: every stage file is a snapshot.
func TestLoadStatePrefersTheLatestStage(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, saveJSON(filepath.Join(dir, "1-discovery.json"), &State{Question: "early"}))
	require.NoError(t, saveJSON(filepath.Join(dir, "3-critique.json"), &State{Question: "later"}))

	st, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, "later", st.Question)

	_, err = LoadState(t.TempDir())
	require.ErrorIs(t, err, errNoArtifacts)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "4-synthesis.json"), []byte("{broken"), 0o600))

	_, err = LoadState(dir)
	require.ErrorContains(t, err, "4-synthesis.json", "a broken newest artifact is an error, not a silent fallback")
}

func TestLoadRunsSingleRunAndTopic(t *testing.T) {
	topic := t.TempDir()

	writeRun(t, topic, "old-run", "1-discovery.json", &State{Question: "old"}, 48*time.Hour)
	writeRun(t, topic, "new-run", "1-discovery.json", &State{Question: "new"}, time.Hour)
	require.NoError(t, os.MkdirAll(filepath.Join(topic, "archive", "x"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(topic, ".hidden"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(topic, "no-artifacts"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(topic, "cache.json"), []byte("{}"), 0o600))

	runs, err := LoadRuns(topic, quietLog())
	require.NoError(t, err)
	require.Len(t, runs, 2, "archive, dot-directories, files and artifact-less directories are not runs")
	assert.Equal(t, []string{"new-run", "old-run"}, []string{runs[0].Name, runs[1].Name}, "newest first")

	single, err := LoadRuns(filepath.Join(topic, "new-run"), quietLog())
	require.NoError(t, err)
	require.Len(t, single, 1)
	assert.Equal(t, "new-run", single[0].Name)
	assert.Equal(t, "new", single[0].State.Question)
}

func TestLoadRunsErrors(t *testing.T) {
	_, err := LoadRuns(filepath.Join(t.TempDir(), "missing"), quietLog())
	require.ErrorContains(t, err, "neither a run nor a topic")

	_, err = LoadRuns(t.TempDir(), quietLog())
	require.ErrorContains(t, err, "no loadable runs")
}

// A corrupt run among good ones is skipped with a warning, not fatal.
func TestLoadRunsSkipsCorruptRuns(t *testing.T) {
	topic := t.TempDir()

	writeRun(t, topic, "good", "1-discovery.json", &State{Question: "good"}, time.Hour)

	bad := filepath.Join(topic, "bad")
	require.NoError(t, os.MkdirAll(bad, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(bad, "1-discovery.json"), []byte("{broken"), 0o600))

	runs, err := LoadRuns(topic, quietLog())
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "good", runs[0].Name)
}

func TestCostFooter(t *testing.T) {
	var nilCost *CostReport

	assert.Empty(t, nilCost.Markdown())
	assert.Empty(t, (&CostReport{}).Markdown())
	assert.Empty(t, (&CostReport{Critique: &StageCost{}}).Markdown(), "an unused stage is not reported")

	got := (&CostReport{
		Synthesis: &StageCost{Calls: 1, InputTokens: 10, OutputTokens: 2},
		Critique:  &StageCost{Calls: 3, InputTokens: 30, OutputTokens: 6},
	}).Markdown()
	assert.Equal(t, "LLM usage — critique: 3 calls, 30 in / 6 out tokens; synthesis: 1 calls, 10 in / 2 out tokens", got,
		"the order is fixed")
}

// A backend that reports no usage still made the call.
func TestStageCostCountsAnUnreportedCall(t *testing.T) {
	var c StageCost

	c.add(Usage{})
	c.add(Usage{Calls: 2, InputTokens: 5, OutputTokens: 1})

	assert.Equal(t, StageCost{Calls: 3, InputTokens: 5, OutputTokens: 1}, c)
}

func TestJudgePrompt(t *testing.T) {
	st := &State{
		Question:      "q?",
		Enriched:      []model.EnrichedPaper{{DOI: doiA, Title: "T"}},
		VisionNotes:   []model.VisionNote{{DOI: doiA, Findings: []string{"axis truncated"}}},
		ChallengeNote: "you ignored the dropouts",
	}

	got, err := judgePrompt(st)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(got, "Build the evidence assessment"))
	assert.Contains(t, got, "Chart-reading notes from a vision analyst")
	assert.Contains(t, got, "axis truncated")
	assert.Contains(t, got, "you ignored the dropouts")
	assert.Contains(t, got, "It is an argument, not data")

	plain, err := judgePrompt(&State{Question: "q?"})
	require.NoError(t, err)
	assert.NotContains(t, plain, "vision analyst")
	assert.NotContains(t, plain, "challenges a previous assessment")
}

func TestBriefFromText(t *testing.T) {
	c := &scriptCompleter{replies: []string{`{"problem":"p","decision":"d","evidence_questions":[` +
		`{"question":"Does it work?","why":"core","priority":"high"}]}`}}

	b, err := BriefFromText(t.Context(), c, "a messy description")
	require.NoError(t, err)

	assert.Equal(t, "p", b.Problem)
	require.Len(t, b.Questions, 1)
	assert.Contains(t, c.prompts[0], "a messy description")

	_, err = BriefFromText(t.Context(), &scriptCompleter{replies: []string{"not json", "still not"}}, "x")
	require.Error(t, err)
}

func TestCappedAuthors(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, cappedAuthors([]string{"a", "b"}, 3))
	assert.Equal(t, []string{"a", "… and 2 more"}, cappedAuthors([]string{"a", "b", "c"}, 1))
}
