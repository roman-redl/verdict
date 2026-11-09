package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// Consensus study_type values seen in real runs, as the tests write them.
const (
	tagMetaAnalysis      = "meta-analysis"
	tagSystematicReview  = "systematic review"
	tagCohort            = "cohort study"
	tagOther             = "other"
	titleTrial           = "Creatine and memory: a randomized trial"
	titleMetaAnalysisRCT = "Creatine and memory: a systematic review and meta-analysis of randomized trials"
)

// studyOf is a study with the given title and discovery tag.
func studyOf(title, tag string) model.EnrichedPaper {
	var e model.EnrichedPaper

	e.Title, e.StudyDesign = title, tag

	return e
}

func paperOf(doi, title, tag string) model.Paper {
	return model.Paper{DOI: doi, Title: title, StudyDesign: tag}
}

func TestResolveDesign(t *testing.T) {
	tests := []struct {
		name   string
		paper  model.EnrichedPaper
		critic model.StudyDesign
		want   model.StudyDesign
	}{
		{
			name:   "the critic wins over the tag",
			paper:  studyOf("A trial", designRCT),
			critic: model.DesignMetaAnalysis,
			want:   model.DesignMetaAnalysis,
		},
		{
			name:   "the critic wins over the title",
			paper:  studyOf(titleMetaAnalysisRCT, ""),
			critic: model.DesignRCT,
			want:   model.DesignRCT,
		},
		{
			name:   "an unclear critic falls back to the tag",
			paper:  studyOf("", tagMetaAnalysis),
			critic: model.DesignUnclear,
			want:   model.DesignMetaAnalysis,
		},
		{
			name:  "no critic design (an old artifact): the tag",
			paper: studyOf("", tagCohort),
			want:  model.DesignCohort,
		},
		{
			name:  "no tag: the title of a meta-analysis",
			paper: studyOf(titleMetaAnalysisRCT, ""),
			want:  model.DesignMetaAnalysis,
		},
		{
			name:  "the catch-all tag does not hide a review title",
			paper: studyOf("An umbrella review of statins", tagOther),
			want:  model.DesignSystematicReview,
		},
		{
			name:  "a trial title says nothing",
			paper: studyOf(titleTrial, ""),
			want:  model.DesignUnclear,
		},
		{
			name:   "an unparseable stored value falls through",
			paper:  studyOf("", tagSystematicReview),
			critic: "randomised",
			want:   model.DesignSystematicReview,
		},
		{name: "nothing known", paper: studyOf("", ""), want: model.DesignUnclear},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveDesign(&tt.paper, model.Critique{StudyDesign: tt.critic})
			assert.Equal(t, tt.want, got)
		})
	}
}

// Every Consensus study_type found in the run artifacts maps onto the set.
func TestDesignFromTag(t *testing.T) {
	tests := []struct {
		tag  string
		want model.StudyDesign
	}{
		{designRCT, model.DesignRCT},
		{" RCT ", model.DesignRCT},
		{tagMetaAnalysis, model.DesignMetaAnalysis},
		{tagSystematicReview, model.DesignSystematicReview},
		{"literature review", model.DesignNarrativeReview},
		{"non-randomized experimental study", model.DesignNonrandomizedTrial},
		{tagCohort, model.DesignCohort},
		{"cross-sectional study", model.DesignCrossSectional},
		{"case report", model.DesignCaseSeries},
		{"field study", model.DesignOther},
		{"theoretical, modeling, or simulation study", model.DesignOther},
		{"bench experiment", model.DesignOther},
		{tagOther, model.DesignUnclear},
		{"something new", model.DesignUnclear},
		{"", model.DesignUnclear},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			assert.Equal(t, tt.want, designFromTag(tt.tag))
		})
	}
}

func TestDesignFromTitle(t *testing.T) {
	tests := []struct {
		title string
		want  model.StudyDesign
	}{
		{"Statins: a Systematic Review", model.DesignSystematicReview},
		{"An umbrella review of exercise", model.DesignSystematicReview},
		{"Cochrane review: zinc for colds", model.DesignSystematicReview},
		{"Zinc for colds: a meta-analysis", model.DesignMetaAnalysis},
		{"Network meta analyses of statins", model.DesignMetaAnalysis},
		{"A pooled analysis of two trials", model.DesignMetaAnalysis},
		{"A systematic review and meta-analysis", model.DesignMetaAnalysis},
		{"A randomized trial of zinc", model.DesignUnclear},
		{"", model.DesignUnclear},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			assert.Equal(t, tt.want, designFromTitle(tt.title))
		})
	}
}

func TestDisplayDesign(t *testing.T) {
	trial := studyOf(titleTrial, "")
	tagged := studyOf("", tagCohort)
	review := studyOf(titleMetaAnalysisRCT, "")

	assert.Equal(t, tagCohort, displayDesign(&tagged, model.Critique{StudyDesign: model.DesignRCT}),
		"the discovery tag is shown as the source wrote it")
	assert.Equal(t, "rct", displayDesign(&trial, model.Critique{StudyDesign: model.DesignRCT}),
		"no tag: the critic's design")
	assert.Equal(t, "meta_analysis", displayDesign(&review, model.Critique{}), "no tag, no critic: the title")
	assert.Empty(t, displayDesign(&trial, model.Critique{StudyDesign: model.DesignUnclear}), "nothing known: empty")
}

func TestNormalizeDesign(t *testing.T) {
	tests := []struct {
		name     string
		raw      model.StudyDesign
		want     model.StudyDesign
		wantWarn bool
	}{
		{name: "valid", raw: model.DesignCohort, want: model.DesignCohort},
		{name: "case and hyphen", raw: "Meta-Analysis", want: model.DesignMetaAnalysis},
		{name: "missing", raw: "", want: model.DesignUnclear},
		{name: "unknown", raw: "randomised trial", want: model.DesignUnclear, wantWarn: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer

			log := slog.New(slog.NewTextHandler(&logs, nil))

			assert.Equal(t, tt.want, normalizeDesign(tt.raw, doiA, log))
			assert.Equal(t, tt.wantWarn, strings.Contains(logs.String(), "level=WARN"), logs.String())
		})
	}
}

// The critic's design survives the batch and single forms; an invalid word
// becomes "unclear" with a warning, an omitted field "unclear" without one.
func TestCritiqueNormalizesTheDesign(t *testing.T) {
	batch := `{"critiques":[{"doi":"10.1/a","study_design":"Meta-Analysis"},` +
		`{"doi":"10.1/b","study_design":"quasi-experiment"}]}`
	single := `{"doi":"10.1/c"}`
	s := &scriptCompleter{replies: []string{batch, single}}

	var logs bytes.Buffer

	log := slog.New(slog.NewTextHandler(&logs, nil))

	got, _, err := critiqueBatch(context.Background(), s,
		[]model.EnrichedPaper{ep(doiA), ep(doiB)}, "q", log)
	require.NoError(t, err)

	assert.Equal(t, model.DesignMetaAnalysis, got[doiA].StudyDesign)
	assert.Equal(t, model.DesignUnclear, got[doiB].StudyDesign)
	assert.Equal(t, 1, strings.Count(logs.String(), "level=WARN"), "one warning, for the unknown word")
	assert.Contains(t, logs.String(), "quasi-experiment")

	fb, _, err := critiquePaper(context.Background(), s, ep("10.1/c"), "q", log)
	require.NoError(t, err)
	assert.Equal(t, model.DesignUnclear, fb.StudyDesign)
	assert.Equal(t, 1, strings.Count(logs.String(), "level=WARN"), "an omitted design is not a warning")
}

// Both critique prompts ask for study_design and name every design.
func TestCritiquePromptsAskForTheDesign(t *testing.T) {
	for name, prompt := range map[string]string{
		"single": critiqueSystemPrompt, "batch": batchCritiqueSystemPrompt,
	} {
		assert.Contains(t, prompt, "study_design:", name)
		assert.Contains(t, prompt, "the design of THIS paper as stated in its text", name)

		for _, d := range model.StudyDesigns() {
			assert.Contains(t, prompt, `"`+string(d)+`"`, name+": "+string(d))
		}
	}
}

// A critique written before study_design existed loads, and the design comes
// from the discovery tag or the title.
func TestOldCritiqueWithoutDesignFallsBack(t *testing.T) {
	const old = `{
	  "enriched": [
	    {"doi": "10.1/rev", "title": "Zinc: a review", "study_design": "meta-analysis", "source": "consensus"},
	    {"doi": "10.1/ttl", "title": "Zinc for colds: a systematic review", "source": "manual"},
	    {"doi": "10.1/trl", "title": "Zinc for colds: a randomized trial", "source": "manual"}
	  ],
	  "critiques": [
	    {"doi": "10.1/rev", "design_quality": "high", "relevance": "direct"},
	    {"doi": "10.1/ttl", "design_quality": "high", "relevance": "direct"},
	    {"doi": "10.1/trl", "design_quality": "high", "relevance": "direct"}
	  ]
	}`

	var st State

	require.NoError(t, json.Unmarshal([]byte(old), &st))

	crits := critiquesFor(&st)
	require.Len(t, crits, 3)

	for _, c := range crits {
		assert.Empty(t, c.StudyDesign, "the artifact has no critic design")
	}

	assert.Equal(t, model.DesignMetaAnalysis, resolveDesign(&st.Enriched[0], crits[0]), "from the tag")
	assert.Equal(t, model.DesignSystematicReview, resolveDesign(&st.Enriched[1], crits[1]), "from the title")
	assert.Equal(t, model.DesignUnclear, resolveDesign(&st.Enriched[2], crits[2]), "nothing says")

	rows := buildMatrix(&st, nil)
	assert.Equal(t, tagMetaAnalysis, rows[0].Design, "the tag as the source wrote it")
	assert.Equal(t, "systematic_review", rows[1].Design, "no tag: the title")
	assert.Empty(t, rows[2].Design)
}

// With no discovery tag (--dois, OpenAlex) the Design column and the
// synthesis input carry the critic's design.
func TestMatrixAndSynthesisInputUseTheCriticsDesign(t *testing.T) {
	st := &State{
		Question: "q?",
		Enriched: []model.EnrichedPaper{
			{Paper: paperOf(doiA, "Trial A", "")},
			{Paper: paperOf(doiB, "Trial B", designRCT)},
		},
		Critiques: []model.Critique{
			{DOI: doiA, Relevance: relevanceDirect, StudyDesign: model.DesignCohort},
			{DOI: doiB, Relevance: relevanceDirect, StudyDesign: model.DesignCohort},
		},
	}

	rows := buildMatrix(st, nil)
	assert.Equal(t, string(model.DesignCohort), rows[0].Design, "no tag: the critic's design")
	assert.Equal(t, designRCT, rows[1].Design, "the tag stays as is")

	prompt, err := synthesisUserPrompt(st)
	require.NoError(t, err)
	assert.Contains(t, prompt, `"design":"cohort"`)
	assert.Contains(t, prompt, `"design":"rct"`)
}

func TestSynthesisPromptKeepsReviewsOutOfTheTally(t *testing.T) {
	assert.Contains(t, synthesisSystemPrompt, "not independent evidence")
	assert.Contains(t, synthesisSystemPrompt, "The pooled_estimate never includes them")
}
