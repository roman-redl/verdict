package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var testNow = time.Date(2026, 11, 6, 12, 30, 0, 0, time.UTC)

// fakeRunner is a handwritten pipeline port: reply scripts the answer of each
// call, calls records the options it was given.
type fakeRunner struct {
	reply func(opts pipeline.Options) (*pipeline.State, error)
	calls []pipeline.Options
}

func (f *fakeRunner) Run(_ context.Context, opts pipeline.Options) (*pipeline.State, string, error) {
	f.calls = append(f.calls, opts)

	st, err := f.reply(opts)

	return st, opts.RunDir, err
}

// reportState is a finished run: the verdict level and the directions of the
// direct studies.
func reportState(level string, directions ...string) *pipeline.State {
	rows := make([]model.MatrixRow, len(directions))
	for i, d := range directions {
		rows[i] = model.MatrixRow{DOI: fmt.Sprintf("10.1/%d", i), Relevance: "direct", Direction: d}
	}

	return &pipeline.State{
		Report: &model.Report{Verdict: level, Matrix: rows},
		Cost: &pipeline.CostReport{
			Critique:  &pipeline.StageCost{Calls: 3, InputTokens: 300, OutputTokens: 30},
			Synthesis: &pipeline.StageCost{Calls: 1, InputTokens: 100, OutputTokens: 10},
		},
	}
}

func newTestEvaluator(t *testing.T, r Runner) *Evaluator {
	t.Helper()

	ev, err := New(r, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return testNow })
	require.NoError(t, err)

	return ev
}

func testQuestions(t *testing.T) []Question {
	t.Helper()

	qs, err := LoadSet(filepath.Join("testdata", "set.yaml"))
	require.NoError(t, err)

	return qs
}

func testParams(t *testing.T) Params {
	t.Helper()

	return Params{
		SetFile: "testdata/set.yaml", Out: filepath.Join(t.TempDir(), "out"), Repeat: 1,
		Options: pipeline.Options{TextMode: pipeline.TextHybrid, LLMModel: "sonnet"},
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	run := &fakeRunner{}
	now := func() time.Time { return testNow }

	_, err := New(nil, log, now)
	require.Error(t, err)

	_, err = New(run, nil, now)
	require.Error(t, err)

	_, err = New(run, log, nil)
	require.Error(t, err)
}

func TestRunBuildsOptionsAndScores(t *testing.T) {
	fr := &fakeRunner{reply: func(pipeline.Options) (*pipeline.State, error) {
		return reportState("moderate", "positive", "positive", "null"), nil
	}}
	p := testParams(t)
	qs := testQuestions(t)[:1]

	sum, err := newTestEvaluator(t, fr).Run(t.Context(), qs, p)
	require.NoError(t, err)

	require.Len(t, fr.calls, 1)

	opts := fr.calls[0]
	assert.Equal(t, qs[0].Claim, opts.Question)
	assert.Equal(t, filepath.Join(p.Out, "10-1", "run"), opts.RunDir)
	assert.Equal(t, filepath.Join(p.Out, "10-1", "dois.txt"), opts.DOIFile)
	assert.Equal(t, "eval-10-1", opts.Topic)
	assert.Equal(t, pipeline.TextHybrid, opts.TextMode)
	assert.Equal(t, "sonnet", opts.LLMModel)
	assert.False(t, opts.Resume)

	dois, err := os.ReadFile(opts.DOIFile)
	require.NoError(t, err)
	assert.Equal(t, "10.1001/jamanetworkopen.2021.21505\n10.1016/j.ejrad.2018.01.022\n", string(dois))

	require.Len(t, sum.Results, 1)

	r := sum.Results[0]
	assert.True(t, r.Scored)
	assert.True(t, r.Match())
	assert.Equal(t, OutcomeWorks, r.Got)
	assert.Equal(t, "direct 3: positive 2, null 1", r.Tally)
	assert.Equal(t, ConsensusUnknown, r.Consensus, "the fake report carries no consensus status")
	assert.Equal(t, LevelModerate, r.Level)
	assert.Equal(t, Strength{Scored: true, Within1: true}, r.Strength, "high certainty vs a moderate verdict")
	assert.Equal(t, 2, r.Given)
	assert.Equal(t, 3, r.Reached)
	assert.Equal(t, &Usage{Calls: 4, InputTokens: 400, OutputTokens: 40}, r.Usage)
	assert.Equal(t, "10-1/run/report.md", r.Report)
	assert.Empty(t, r.Err)

	assert.FileExists(t, filepath.Join(p.Out, summaryMD))
	assert.FileExists(t, filepath.Join(p.Out, summaryCSV))
	assert.Equal(t, testNow, sum.Date)
	assert.Equal(t, "llm-model=sonnet", sum.Models)
}

func TestRunRepeatsUseSeparateRunDirs(t *testing.T) {
	outcomes := map[string]string{"10-1": pos, "10-2": pos, "30-1": nul, "30-2": neg}
	fr := &fakeRunner{reply: func(o pipeline.Options) (*pipeline.State, error) {
		return reportState("weak", outcomes[o.Topic[len("eval-"):]]), nil
	}}
	p := testParams(t)
	p.Repeat = 2

	sum, err := newTestEvaluator(t, fr).Run(t.Context(), testQuestions(t)[:2], p)
	require.NoError(t, err)

	require.Len(t, fr.calls, 4)

	dirs := map[string]bool{}
	for _, c := range fr.calls {
		dirs[c.RunDir] = true
	}

	assert.Len(t, dirs, 4, "every repeat has its own run directory, so critiques are never reused")
	assert.Equal(t, 1, sum.Totals().Stable, "question 10 repeats alike, question 30 does not")
}

func TestRunLoadsFinishedRunWithoutCallingRunner(t *testing.T) {
	fr := &fakeRunner{reply: func(pipeline.Options) (*pipeline.State, error) {
		return nil, errors.New("the runner must not be called")
	}}
	p := testParams(t)
	runDir := filepath.Join(p.Out, "10-1", "run")
	require.NoError(t, os.MkdirAll(runDir, 0o750))

	b, err := json.Marshal(reportState("strong", "positive"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(runDir, synthesisArtifact), b, 0o600))

	sum, err := newTestEvaluator(t, fr).Run(t.Context(), testQuestions(t)[:1], p)
	require.NoError(t, err)

	assert.Empty(t, fr.calls)
	require.Len(t, sum.Results, 1)
	assert.Equal(t, OutcomeWorks, sum.Results[0].Got)
	assert.Equal(t, LevelStrong, sum.Results[0].Level)
	assert.Empty(t, sum.Results[0].Err)
}

func TestRunResumesStartedRunDirectory(t *testing.T) {
	fr := &fakeRunner{reply: func(pipeline.Options) (*pipeline.State, error) {
		return reportState("weak", "null"), nil
	}}
	p := testParams(t)
	require.NoError(t, os.MkdirAll(filepath.Join(p.Out, "30-1", "run"), 0o750))

	_, err := newTestEvaluator(t, fr).Run(t.Context(), testQuestions(t)[1:2], p)
	require.NoError(t, err)

	require.Len(t, fr.calls, 1)
	assert.True(t, fr.calls[0].Resume, "a run directory without the synthesis artifact resumes")
}

func TestRunRecordsErrorAndContinues(t *testing.T) {
	fr := &fakeRunner{reply: func(o pipeline.Options) (*pipeline.State, error) {
		if o.Topic == "eval-30-1" {
			return nil, fmt.Errorf("stage 2-enrichment: %w", errors.New("OpenAlex\nis down"))
		}

		return reportState("weak", "null"), nil
	}}
	p := testParams(t)

	sum, err := newTestEvaluator(t, fr).Run(t.Context(), testQuestions(t), p)
	require.NoError(t, err)

	require.Len(t, sum.Results, 3)
	assert.Len(t, fr.calls, 3, "the failure does not stop the eval")

	failed := sum.Results[1]
	assert.False(t, failed.Scored)
	assert.False(t, failed.Match())
	assert.Equal(t, "running the pipeline: stage 2-enrichment: OpenAlex is down", failed.Err)
	assert.Equal(t, OutcomeNoEffect, failed.Expected)

	tot := sum.Totals()
	assert.Equal(t, 1, tot.Failed)
	assert.Equal(t, 2, tot.Scored)

	md, err := os.ReadFile(filepath.Join(p.Out, summaryMD))
	require.NoError(t, err)
	assert.Contains(t, string(md), "stage 2-enrichment: OpenAlex is down |")
}

func TestRunRecordsRunWithoutReport(t *testing.T) {
	fr := &fakeRunner{reply: func(pipeline.Options) (*pipeline.State, error) {
		return &pipeline.State{}, nil
	}}

	sum, err := newTestEvaluator(t, fr).Run(t.Context(), testQuestions(t)[:1], testParams(t))
	require.NoError(t, err)

	require.Len(t, sum.Results, 1)
	assert.Equal(t, "the run has no report", sum.Results[0].Err)
	assert.Empty(t, sum.Results[0].Report)
}

func TestRunRecordsUnknownVerdictLevel(t *testing.T) {
	fr := &fakeRunner{reply: func(pipeline.Options) (*pipeline.State, error) {
		return reportState("splendid", "positive"), nil
	}}

	sum, err := newTestEvaluator(t, fr).Run(t.Context(), testQuestions(t)[:1], testParams(t))
	require.NoError(t, err)

	assert.Contains(t, sum.Results[0].Err, "unknown verdict level")
}

func TestRunStopsOnUsageLimitAndKeepsSummary(t *testing.T) {
	fr := &fakeRunner{reply: func(o pipeline.Options) (*pipeline.State, error) {
		if o.Topic == "eval-30-1" {
			return nil, &pipeline.UserError{Msg: "usage limit", Cause: pipeline.ErrLLMStop}
		}

		return reportState("weak", "null"), nil
	}}
	p := testParams(t)

	sum, err := newTestEvaluator(t, fr).Run(t.Context(), testQuestions(t), p)
	require.ErrorIs(t, err, pipeline.ErrLLMStop)
	require.ErrorContains(t, err, "rerun the same command with the same --out")
	require.ErrorContains(t, err, p.Out)

	assert.Len(t, fr.calls, 2, "nothing runs after the limit")
	require.NotNil(t, sum)
	assert.Len(t, sum.Results, 1, "only the finished run is in the summary")
	assert.FileExists(t, filepath.Join(p.Out, summaryMD))
	assert.FileExists(t, filepath.Join(p.Out, summaryCSV))
}

func TestRunStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	fr := &fakeRunner{reply: func(pipeline.Options) (*pipeline.State, error) {
		cancel()

		return nil, errors.New("signal: interrupt")
	}}

	sum, err := newTestEvaluator(t, fr).Run(ctx, testQuestions(t), testParams(t))
	require.Error(t, err)
	assert.Len(t, fr.calls, 1)
	assert.Empty(t, sum.Results)
}

func TestRunRejectsBadParams(t *testing.T) {
	ev := newTestEvaluator(t, &fakeRunner{})
	p := testParams(t)
	p.Repeat = 0

	_, err := ev.Run(t.Context(), nil, p)
	require.ErrorContains(t, err, "repeat")

	p = testParams(t)
	p.Out = ""

	_, err = ev.Run(t.Context(), nil, p)
	require.ErrorContains(t, err, "output directory")
}

func TestModelFlags(t *testing.T) {
	assert.Equal(t, "defaults", modelFlags(&pipeline.Options{}))
	assert.Equal(t, "llm-command=claude-max, llm-model=opus, model-critique=haiku, model-deep=sonnet, "+
		"model-synthesis=fable, deep",
		modelFlags(&pipeline.Options{
			LLMCommand: "claude-max", LLMModel: "opus", ModelCritique: "haiku", ModelDeep: "sonnet",
			ModelSynthesis: "fable", Deep: true,
		}))
}

func TestUsageOf(t *testing.T) {
	assert.Nil(t, usageOf(nil))
	assert.Nil(t, usageOf(&pipeline.CostReport{}))
	assert.Equal(t, &Usage{Calls: 2, InputTokens: 5, OutputTokens: 7},
		usageOf(&pipeline.CostReport{Synthesis: &pipeline.StageCost{Calls: 2, InputTokens: 5, OutputTokens: 7}}))
	assert.Equal(t, &Usage{Calls: 3, InputTokens: 15, OutputTokens: 9},
		usageOf(&pipeline.CostReport{
			Screening: &pipeline.StageCost{Calls: 1, InputTokens: 10, OutputTokens: 2},
			Synthesis: &pipeline.StageCost{Calls: 2, InputTokens: 5, OutputTokens: 7},
		}), "the discovery screening is part of the run's spend")
}
