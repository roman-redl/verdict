package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/model"
)

// scriptCompleter replies from a script and records the prompts it got.
type scriptCompleter struct {
	replies []string
	prompts []string
	calls   int
}

func (s *scriptCompleter) Name() string { return "script" }
func (s *scriptCompleter) Complete(_ context.Context, _, user string, _ llm.Options) (string, llm.Usage, error) {
	s.prompts = append(s.prompts, user)
	s.calls++

	if s.calls > len(s.replies) {
		return "", llm.Usage{}, nil // script exhausted: an empty answer
	}

	return s.replies[s.calls-1], llm.Usage{InputTokens: 100, OutputTokens: 50}, nil
}

func ep(doi string) model.EnrichedPaper {
	return model.EnrichedPaper{
		DOI: doi, Title: "Study " + doi, Year: 2023, Abstract: "A randomized trial on " + doi,
		StudyDesign: "rct", Source: engineConsensus,
	}
}

func crit(doi, quality, relevance, direction string) model.Critique {
	return model.Critique{
		DOI: doi, DesignQuality: quality, SampleAdequacy: "adequate",
		Relevance: relevance, ResultDirection: direction, Strengths: []string{"x"}, Concerns: []string{"y"},
	}
}

// The batch must match critiques to studies by DOI, and studies the batch
// dropped must fall back to single calls.
func TestCritiqueBatchFallsBackForDroppedStudies(t *testing.T) {
	batch := `{"critiques":[` +
		`{"doi":"10.1/a","design_quality":"high","relevance":"direct","result_direction":"positive"},` +
		`{"doi":"10.1/b","design_quality":"moderate","relevance":"indirect","result_direction":"null"}]}` // c dropped
	single := `{"doi":"10.1/c","design_quality":"low","relevance":"tangential","result_direction":"unclear"}`
	s := &scriptCompleter{replies: []string{batch, single}}

	got, _, err := critiqueBatch(context.Background(), s, []model.EnrichedPaper{ep(doiA), ep(doiB)}, "q")
	require.NoError(t, err)
	require.Len(t, got, 2, "batch must return its two studies")

	fb, _, err := critiquePaper(context.Background(), s, ep("10.1/c"), "q")
	require.NoError(t, err)

	assert.Equal(t, qualityHigh, got[doiA].DesignQuality)
	assert.Equal(t, relevanceDirect, got[doiA].Relevance)

	assert.Equal(t, qualityLow, fb.DesignQuality, "fallback single call")

	require.Len(t, s.prompts, 2, "want one batch prompt then one single prompt")
	assert.Contains(t, s.prompts[0], `"studies"`, "want one batch prompt then one single prompt")

	assert.Contains(t, s.prompts[1], "Assess this study", "fallback must use the single-study form")
}

// A total batch failure degrades to single calls for the whole chunk.
func TestCritiqueBatchTotalFailureReturnsNil(t *testing.T) {
	s := &scriptCompleter{replies: []string{"complete garbage, not json at all", "still not json"}}

	got, _, err := critiqueBatch(context.Background(), s, []model.EnrichedPaper{ep(doiA)}, "q")
	require.Error(t, err, "the failure must be reported, not swallowed")
	require.Nil(t, got, "failed batch must return nil map")

	require.Equal(t, 2, s.calls, "the ask + the (payload-carrying) retry")
}

// The stage reuses cached critiques and writes new ones back.
func TestCritiqueStageUsesCache(t *testing.T) {
	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "critique-cache.json")
	seeded := CritiqueCache{
		critiqueCacheKey(ep(doiA), "q?"): {
			CheckedAt: time.Now(), Critique: crit(doiA, qualityHigh, relevanceDirect, directionPositive),
		},
	}

	require.NoError(t, seeded.Save(cachePath))

	batch := `{"critiques":[{"doi":"10.1/b","design_quality":"low",` +
		`"relevance":"tangential","result_direction":"unclear"}]}`
	s := &scriptCompleter{replies: []string{batch}}
	p := New(Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d},
		Clients{LLM: s}, quietLog())
	st := &State{Question: "q?", Enriched: []model.EnrichedPaper{ep(doiA), ep(doiB)}}

	require.NoError(t, p.critique(context.Background(), st, Options{RunDir: filepath.Join(tmp, "run-1")}))

	require.Equal(t, 1, s.calls, "cached study must not be re-asked")

	require.Equal(t, qualityHigh, st.Critiques[0].DesignQuality)
	require.Equal(t, qualityLow, st.Critiques[1].DesignQuality)

	require.NotNil(t, st.Cost, "cost must count only real calls")
	require.NotNil(t, st.Cost.Critique, "cost must count only real calls")
	require.Equal(t, 1, st.Cost.Critique.Calls, "cost must count only real calls")

	saved := LoadCritiqueCache(cachePath, ttl30d, quietLog())
	assert.Contains(t, saved, critiqueCacheKey(ep(doiB), "q?"), "fresh critique not written back")

	// A second critique stage over the same corpus: pure cache, zero calls.
	s2 := &scriptCompleter{}
	p2 := New(Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d},
		Clients{LLM: s2}, quietLog())
	st2 := &State{Question: "q?", Enriched: st.Enriched}

	require.NoError(t, p2.critique(context.Background(), st2, Options{RunDir: filepath.Join(tmp, "run-2")}))

	require.Equal(t, 0, s2.calls, "second stage must be cache-only")
	require.Equal(t, qualityHigh, st2.Critiques[0].DesignQuality, "second stage must be cache-only")

	// A dropped full text must MISS the cache: the abstract-only critique is
	// stale for a study the critic should now read in full.
	withFT := ep(doiA)
	withFT.FullText = "Methods and results of the full paper..."
	s3 := &scriptCompleter{}
	p3 := New(Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d},
		Clients{LLM: s3}, quietLog())
	st3 := &State{Question: "q?", Enriched: []model.EnrichedPaper{withFT}}

	require.NoError(t, p3.critique(context.Background(), st3, Options{RunDir: filepath.Join(tmp, "run-3")}))

	require.NotZero(t, s3.calls, "added full text must re-critique, not serve the cached abstract critique")
}

// PDFs dropped into the run directory after enrichment (the addpdf recipe, a
// resume whose stage-2 artifact survived) enter at critique time and
// re-critique: the cache key includes the full text (regression: the pickup
// lived exclusively in stage 2, which resume skips).
func TestCritiquePicksUpDroppedPDF(t *testing.T) {
	restore := pdfTextExtractor
	pdfTextExtractor = func(b []byte, maxChars int) (string, error) {
		return "DROPPED PDF FULL TEXT", nil
	}

	defer func() { pdfTextExtractor = restore }()

	tmp := t.TempDir()

	runDir := filepath.Join(tmp, "run")
	require.NoError(t, os.MkdirAll(filepath.Join(runDir, "fulltext"), 0o750))

	require.NoError(t, os.WriteFile(filepath.Join(runDir, "fulltext", "10.1_a.pdf"),
		[]byte("%PDF-1.4 stub"), 0o600))

	cachePath := filepath.Join(tmp, "critique-cache.json")
	seeded := CritiqueCache{
		critiqueCacheKey(ep(doiA), "q?"): {
			CheckedAt: time.Now(), Critique: crit(doiA, qualityHigh, relevanceDirect, directionPositive),
		},
	}

	require.NoError(t, seeded.Save(cachePath))

	batch := `{"critiques":[{"doi":"10.1/a","design_quality":"high","relevance":"direct","result_direction":"positive"}]}`
	s := &scriptCompleter{replies: []string{batch}}
	p := New(Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d},
		Clients{LLM: s}, quietLog())
	p.dir = runDir
	st := &State{Question: "q?", Enriched: []model.EnrichedPaper{ep(doiA)}} // no FullText yet

	require.NoError(t, p.critique(context.Background(), st, Options{RunDir: runDir}))

	require.Equal(t, 1, s.calls, "the picked-up PDF must re-critique (cache miss)")

	assert.Contains(t, s.prompts[0], "DROPPED PDF FULL TEXT", "the critique prompt must carry the picked-up full text")
}

// The judge payload carries an abstract only for direct studies (and when
// the critique is missing); the critique alone carries the rest.
func TestSynthesisUserPromptAbstractDiet(t *testing.T) {
	longAbstract := strings.Repeat("word ", 400) // 2000 chars
	st := &State{Question: "q"}
	st.Enriched = []model.EnrichedPaper{
		{DOI: "10.1/direct", Title: "D", Abstract: longAbstract},
		{DOI: "10.1/indirect", Title: "I", Abstract: longAbstract},
		{DOI: "10.1/nocrit", Title: "N", Abstract: longAbstract},
	}
	st.Critiques = []model.Critique{
		crit("10.1/direct", qualityHigh, relevanceDirect, directionPositive),
		crit("10.1/indirect", qualityLow, relevanceIndirect, directionNull),
	}
	out, err := synthesisUserPrompt(st)
	require.NoError(t, err)

	var parsed struct {
		Studies []struct {
			DOI      string `json:"doi"`
			Abstract string `json:"abstract"`
		} `json:"studies"`
	}

	braceAt := strings.Index(out, "{")
	require.GreaterOrEqual(t, braceAt, 0, "synthesis prompt must carry JSON")

	require.NoError(t, json.Unmarshal([]byte(out[braceAt:]), &parsed))

	require.Len(t, parsed.Studies, 3)

	for _, s := range parsed.Studies {
		switch s.DOI {
		case "10.1/direct", "10.1/nocrit":
			assert.NotEmpty(t, s.Abstract, "%s: abstract must ride along (direct / no critique)", s.DOI)

			assert.LessOrEqual(t, len(s.Abstract), 1300, "%s: abstract must be capped at 1200 chars", s.DOI)
		case "10.1/indirect":
			assert.Empty(t, s.Abstract, "%s: abstract must be dropped", s.DOI)
		}
	}
}
