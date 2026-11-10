package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

const (
	doiFirst  = "10.1001/jamanetworkopen.2021.21505"
	doiSecond = "10.1016/j.ejrad.2018.01.022"
)

// searchState is a finished search run of question 10: the first study is
// found, screened in and in the corpus; the second is only in the pool.
func searchState() *pipeline.State {
	st := reportState("moderate", "positive", "positive", "null")
	st.Discovery = &pipeline.DiscoveryInfo{Engine: pipeline.EngineConsensus, Pool: []pipeline.Candidate{
		cand(doiFirst, model.SourceConsensus, new(true)),
		cand("10.1016/J.EJRAD.2018.01.022", model.SourceReviewRefs, new(false)),
		cand(doiOther, model.SourceConsensus, new(true)),
	}}
	st.Papers = []model.Paper{paper(doiFirst), paper(doiOther)}

	return st
}

func searchParams(t *testing.T) Params {
	t.Helper()

	p := testParams(t)
	p.Mode = ModeSearch

	return p
}

func TestRunSearchBuildsOptionsAndMeasuresRecall(t *testing.T) {
	fr := &fakeRunner{reply: func(pipeline.Options) (*pipeline.State, error) { return searchState(), nil }}
	p := searchParams(t)
	qs := testQuestions(t)[:1]

	sum, err := newTestEvaluator(t, fr).Run(t.Context(), qs, p)
	require.NoError(t, err)

	require.Len(t, fr.calls, 1)

	opts := fr.calls[0]
	assert.Equal(t, qs[0].Claim, opts.Question)
	assert.Equal(t, []string{qs[0].Question, qs[0].Claim}, opts.Queries)
	assert.Equal(t, []string{"10.1002/14651858.CD009632.pub3"}, opts.ExcludeDOIs)
	assert.Equal(t, pipeline.EngineConsensus, opts.Engine, "never the keyword fallback of an unset engine")
	assert.Empty(t, opts.DOIFile, "no DOI list: the studies are searched for")
	assert.Equal(t, filepath.Join(p.Out, "10-1", "run"), opts.RunDir)
	assert.Equal(t, "eval-10-1", opts.Topic)
	assert.Equal(t, pipeline.TextHybrid, opts.TextMode, "the pass-through settings stay")
	assert.Equal(t, "sonnet", opts.LLMModel)
	assert.NoFileExists(t, filepath.Join(p.Out, "10-1", doiListFile))
	assert.FileExists(t, filepath.Join(p.Out, "10-1", modeFile))

	require.Len(t, sum.Results, 1)
	assert.Equal(t, ModeSearch, sum.Mode)
	assert.True(t, sum.Results[0].Scored)
	assert.Equal(t, &Recall{Total: 2, Pool: 2, Screened: 1, Corpus: 1}, sum.Results[0].Recall)
	assert.Equal(t, Recall{Total: 2, Pool: 2, Screened: 1, Corpus: 1}, sum.Totals().Recall)

	md, err := os.ReadFile(filepath.Join(p.Out, summaryMD))
	require.NoError(t, err)
	assert.Contains(t, string(md), "| 2/2 | 1/2 | 1/2 |")
}

func TestRunDOIModeHasNoRecall(t *testing.T) {
	fr := &fakeRunner{reply: func(pipeline.Options) (*pipeline.State, error) { return searchState(), nil }}
	p := testParams(t)

	sum, err := newTestEvaluator(t, fr).Run(t.Context(), testQuestions(t)[:1], p)
	require.NoError(t, err)

	assert.Nil(t, sum.Results[0].Recall)
	assert.Empty(t, fr.calls[0].Queries)
	assert.Empty(t, fr.calls[0].ExcludeDOIs)
	assert.Empty(t, fr.calls[0].Engine)

	mode, err := recordedMode(filepath.Join(p.Out, "10-1"))
	require.NoError(t, err)
	assert.Equal(t, ModeDOIs, mode)

	md, err := os.ReadFile(filepath.Join(p.Out, summaryMD))
	require.NoError(t, err)
	assert.NotContains(t, string(md), "Recall")
}

func TestRunSearchReusesFinishedRunAndComputesRecall(t *testing.T) {
	fr := &fakeRunner{reply: func(pipeline.Options) (*pipeline.State, error) {
		return nil, assert.AnError
	}}
	p := searchParams(t)
	base := filepath.Join(p.Out, "10-1")
	runDir := filepath.Join(base, "run")
	require.NoError(t, os.MkdirAll(runDir, 0o750))
	require.NoError(t, writeMode(base, ModeSearch))

	b, err := json.Marshal(searchState())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(runDir, synthesisArtifact), b, 0o600))

	sum, err := newTestEvaluator(t, fr).Run(t.Context(), testQuestions(t)[:1], p)
	require.NoError(t, err)

	assert.Empty(t, fr.calls)
	require.NotNil(t, sum.Results[0].Recall)
	assert.Equal(t, Recall{Total: 2, Pool: 2, Screened: 1, Corpus: 1}, *sum.Results[0].Recall)
}

func TestRunSearchNeedsReviewDOI(t *testing.T) {
	fr := &fakeRunner{reply: func(pipeline.Options) (*pipeline.State, error) { return searchState(), nil }}
	p := searchParams(t)
	qs := testQuestions(t)[:2]
	qs[1].ReviewDOI = ""

	sum, err := newTestEvaluator(t, fr).Run(t.Context(), qs, p)

	var ue *pipeline.UserError

	require.ErrorAs(t, err, &ue)
	require.ErrorContains(t, err, "question 30 has no review_doi")
	assert.Nil(t, sum)
	assert.Empty(t, fr.calls, "nothing runs before the check passes")
	assert.NoDirExists(t, p.Out)
}

// TestRunRejectsRunDirectoryOfOtherMode covers the resume: a directory made in
// one mode is never scored as the other, and the check fails before any run
// and before the old summary is overwritten.
func TestRunRejectsRunDirectoryOfOtherMode(t *testing.T) {
	tests := []struct {
		name  string
		first Mode
		then  Mode
		// legacy removes the marker: a directory older than the marker holds
		// only dois.txt in the DOI-list mode
		legacy  bool
		wantErr string
	}{
		{name: "DOI list then search", first: ModeDOIs, then: ModeSearch, wantErr: "was made in the dois mode"},
		{name: "search then DOI list", first: ModeSearch, then: ModeDOIs, wantErr: "was made in the search mode"},
		{
			name: "DOI list before the marker existed, then search", first: ModeDOIs, then: ModeSearch,
			legacy: true, wantErr: "was made in the dois mode",
		},
		{name: "same mode resumes: DOI list", first: ModeDOIs, then: ModeDOIs},
		{name: "same mode resumes: search", first: ModeSearch, then: ModeSearch},
		{name: "legacy DOI list resumes in its mode", first: ModeDOIs, then: ModeDOIs, legacy: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := &fakeRunner{reply: func(pipeline.Options) (*pipeline.State, error) { return searchState(), nil }}
			ev := newTestEvaluator(t, fr)
			qs := testQuestions(t)[:1]
			p := testParams(t)
			p.Mode = tt.first

			_, err := ev.Run(t.Context(), qs, p)
			require.NoError(t, err)

			if tt.legacy {
				require.NoError(t, os.Remove(filepath.Join(p.Out, "10-1", modeFile)))
			}

			before, err := os.ReadFile(filepath.Join(p.Out, summaryMD))
			require.NoError(t, err)

			// the fake runner writes no artifacts: an empty run directory is an
			// interrupted run
			require.NoError(t, os.MkdirAll(filepath.Join(p.Out, "10-1", "run"), 0o750))

			calls := len(fr.calls)
			p.Mode = tt.then

			sum, err := ev.Run(t.Context(), qs, p)

			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.Len(t, fr.calls, calls+1)
				assert.True(t, fr.calls[calls].Resume)

				return
			}

			var ue *pipeline.UserError

			require.ErrorAs(t, err, &ue)
			require.ErrorContains(t, err, tt.wantErr)
			require.ErrorContains(t, err, "--out")
			assert.Nil(t, sum)
			assert.Len(t, fr.calls, calls, "nothing runs")

			after, err := os.ReadFile(filepath.Join(p.Out, summaryMD))
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after), "the summary of the first eval stays")
		})
	}
}

func TestRecordedMode(t *testing.T) {
	dir := t.TempDir()

	got, err := recordedMode(filepath.Join(dir, "missing"))
	require.NoError(t, err)
	assert.Empty(t, got, "a new directory records nothing")

	bad := filepath.Join(dir, "bad")
	require.NoError(t, os.MkdirAll(bad, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(bad, modeFile), []byte("sideways\n"), 0o600))

	_, err = recordedMode(bad)
	require.ErrorContains(t, err, "unknown eval mode")
}
