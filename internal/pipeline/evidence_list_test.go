package pipeline

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	titleCohort = "Creatine intake and cognition: a prospective cohort"
	titleBig    = "Creatine supplementation and working memory in healthy adults: a randomized trial"
	doiSolo     = "10.1/solo"
	oddValue    = "odd"
	titleSolo   = "Solo trial"
	titleNoDOI  = "Pooled, no DOI"
)

// listExample is a small evidence base with every kind of row: a cohort, three
// trials (one with an unknown size), and two reviews.
func listExample() *model.EvidenceBase {
	return &model.EvidenceBase{
		Indirect: 28, NotAssessed: 2,
		Studies: []model.EvidenceStudy{
			{
				Title: titleBig, Year: 2021, Design: model.DesignRCT, Blinding: model.BlindingDouble,
				Participants: 1105, Direction: model.DirectionSupports, Pooled: true,
			},
			{
				Title: "Creatine and sleep deprivation", Year: 2019, Design: model.DesignRCT, Blinding: model.BlindingOpen,
				Participants: 380, Direction: model.DirectionNull, Pooled: true,
			},
			{
				Title: "Creatine in vegetarians", Year: 2017, Design: model.DesignCrossoverRCT,
				Blinding: model.BlindingSingle, Direction: model.DirectionOpposite,
			},
			{
				Title: titleCohort, Year: 2020, Design: model.DesignCohort, Participants: 9800,
				Direction: model.DirectionMixed,
			},
			{
				Title: "Creatine for cognition: a Cochrane review", Year: 2022, Design: model.DesignSystematicReview,
				Cochrane: true, Direction: model.DirectionSupports,
			},
			{
				Title: "Creatine and the brain: a meta-analysis", Year: 2023, Design: model.DesignMetaAnalysis,
				Direction: model.DirectionUnclear,
			},
		},
	}
}

// The whole block in English: the largest study first, the unknown size after
// the known ones, the reviews in a group of their own, the footer last.
func TestStudyListEnglish(t *testing.T) {
	want := "Direct studies by number of participants (bar: log scale)\n\n" +
		"```\n" +
		"  9800 ████████████████ · cohort                       · 2020 · mixed\n" +
		"       " + titleCohort + "\n" +
		"  1105 ████████████     · RCT, double-blind            · 2021 · supports  · pooled\n" +
		"       Creatine supplementation and working memory in healthy adults: a rand…\n" +
		"   380 ██████████       · RCT, open-label              · 2019 · no effect · pooled\n" +
		"       Creatine and sleep deprivation\n" +
		"     —                  · crossover RCT, single-blind  · 2017 · opposite\n" +
		"       Creatine in vegetarians\n" +
		"\n" +
		"review                  · systematic review (Cochrane) · 2022 · supports\n" +
		"       Creatine for cognition: a Cochrane review\n" +
		"review                  · meta-analysis                · 2023 · unclear\n" +
		"       Creatine and the brain: a meta-analysis\n" +
		"\n" +
		"Indirect or off-topic: 28 · not assessed: 2\n" +
		"```\n\n"

	assert.Equal(t, want, english().studyList(listExample()))
}

// The same block in Russian: the words of the columns and the header are
// translated, the bars and the numbers are the same.
func TestStudyListRussian(t *testing.T) {
	want := "Прямые исследования по числу участников (полоса — логарифмическая шкала)\n\n" +
		"```\n" +
		" 9800 ████████████████ · когортное                          · 2020 · смешанный\n" +
		"      " + titleCohort + "\n" +
		" 1105 ████████████     · РКИ, двойное слепое                · 2021 · за          · в объединённой оценке\n" +
		"      Creatine supplementation and working memory in healthy adults: a rand…\n" +
		"  380 ██████████       · РКИ, открытое                      · 2019 · нет эффекта · в объединённой оценке\n" +
		"      Creatine and sleep deprivation\n" +
		"    —                  · перекрёстное РКИ, одинарное слепое · 2017 · против\n" +
		"      Creatine in vegetarians\n" +
		"\n" +
		"обзор                  · систематический обзор (Cochrane)   · 2022 · за\n" +
		"      Creatine for cognition: a Cochrane review\n" +
		"обзор                  · метаанализ                         · 2023 · неясно\n" +
		"      Creatine and the brain: a meta-analysis\n" +
		"\n" +
		"Косвенные или не по теме: 28 · не оценены: 2\n" +
		"```\n\n"

	assert.Equal(t, want, russian().studyList(listExample()))
}

// The columns line up: in every study line the separators sit at the same
// character position, in both languages.
func TestStudyListColumnsAlign(t *testing.T) {
	for _, lx := range []lexicon{english(), russian()} {
		var seps [][]int

		for line := range strings.SplitSeq(lx.studyList(listExample()), "\n") {
			if !strings.Contains(line, listSep) || strings.HasPrefix(line, lx.sentence.indirect) {
				continue // a title line or the footer
			}

			var at []int

			runes := []rune(line)
			for i := range runes {
				if strings.HasPrefix(string(runes[i:]), listSep) {
					at = append(at, i)
				}
			}

			seps = append(seps, at[:3]) // after the bar, the design and the year
		}

		require.Len(t, seps, 6, lx.lang)

		for _, s := range seps[1:] {
			assert.Equal(t, seps[0], s, lx.lang)
		}
	}
}

func TestBarBlocks(t *testing.T) {
	for _, tc := range []struct {
		name       string
		n, largest int
		want       int
	}{
		{"the largest study gets the full bar", 9800, 9800, 16},
		{"a log scale: a hundredth of the size, half the bar", 100, 10000, 8},
		{"ten times smaller, one quarter less", 1000, 10000, 12},
		{"a small study still gets one block", 1, 10000, 1},
		{"between zero and one block rounds up to one", 2, 10000, 1},
		{"a size of one among sizes of one", 1, 1, 16},
		{"a lone study is the largest", 40, 40, 16},
		{"no size, no bar", 0, 5000, 0},
		{"a study above the largest is capped", 20000, 10000, 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, barBlocks(tc.n, tc.largest))
		})
	}
}

// Sizes are ordered large to small with the unknown ones last, in the order of
// the corpus among equals; the bars shrink with the logarithm.
func TestStudyListOrder(t *testing.T) {
	study := func(title string, n int) model.EvidenceStudy {
		return model.EvidenceStudy{Title: title, Design: model.DesignRCT, Participants: n, Direction: model.DirectionNull}
	}
	eb := &model.EvidenceBase{Studies: []model.EvidenceStudy{
		study("unknown one", 0), study("small", 50), study("unknown two", 0), study("large", 4000), study("middle", 500),
		study("same as middle", 500),
	}}

	lines := strings.Split(english().studyList(eb), "\n")

	var titles, sizes []string

	var bars []int

	for i, line := range lines {
		if strings.Contains(line, listSep) && !strings.HasPrefix(line, "Indirect") {
			sizes = append(sizes, strings.Fields(line)[0])
			bars = append(bars, strings.Count(line, "█"))
			titles = append(titles, strings.TrimSpace(lines[i+1]))
		}
	}

	assert.Equal(t, []string{"large", "middle", "same as middle", "small", "unknown one", "unknown two"}, titles)
	assert.Equal(t, []int{16, 12, 12, 8, 0, 0}, bars, "log scale: 4000 → 16, 500 → 12, 50 → 8; unknown sizes have no bar")
	assert.Equal(t, []string{"4000", "500", "500", "50", "—", "—"}, sizes, "an unknown size is a dash")
}

// A study of a known size has a bar, whatever its size.
func TestStudyListEveryKnownSizeHasABar(t *testing.T) {
	eb := &model.EvidenceBase{Studies: []model.EvidenceStudy{
		{Title: "huge", Design: model.DesignCohort, Participants: 1_000_000},
		{Title: "tiny", Design: model.DesignCaseSeries, Participants: 3},
	}}

	for line := range strings.SplitSeq(english().studyList(eb), "\n") {
		if strings.Contains(line, "case series") {
			assert.Positive(t, strings.Count(line, "█"), line)
		}
	}
}

// At most 30 studies are listed; the rest are counted. The reviews come after
// the primary studies, so a long list cuts them first.
func TestStudyListCap(t *testing.T) {
	studies := make([]model.EvidenceStudy, 0, 34)

	for i := range 33 {
		studies = append(studies, model.EvidenceStudy{
			Title: fmt.Sprintf("Trial %02d", i), Design: model.DesignRCT, Participants: 1000 - i, Direction: model.DirectionNull,
		})
	}

	studies = append(studies, model.EvidenceStudy{Title: "Pooled analysis", Design: model.DesignMetaAnalysis})
	eb := &model.EvidenceBase{Studies: studies}

	for _, tc := range []struct {
		lx   lexicon
		more string
	}{
		{english(), "… and 4 more"},
		{russian(), "… и ещё 4"},
	} {
		out := tc.lx.studyList(eb)

		assert.Equal(t, 30, strings.Count(out, "Trial "), tc.lx.lang)
		assert.NotContains(t, out, "Trial 30", "the largest 30 are shown")
		assert.NotContains(t, out, "Pooled analysis", "the review is cut with the rest")
		assert.Contains(t, out, "\n"+tc.more+"\n", tc.lx.lang)
		assert.Equal(t, 1, strings.Count(out, tc.more))
	}

	// exactly at the cap nothing is left out
	eb.Studies = studies[:30]
	assert.NotContains(t, english().studyList(eb), "more")

	// the cap counts the reviews: 29 primary studies and 2 reviews show 1 review
	eb.Studies = append(slices.Clone(studies[:29]),
		model.EvidenceStudy{Title: "Review one", Design: model.DesignSystematicReview},
		model.EvidenceStudy{Title: "Review two", Design: model.DesignMetaAnalysis})

	out := english().studyList(eb)
	assert.Contains(t, out, "Review one")
	assert.NotContains(t, out, "Review two")
	assert.Contains(t, out, "… and 1 more")
}

// A block is a code fence that nothing inside can close, and a report from
// before the list has no block.
func TestStudyListFenceAndEmpty(t *testing.T) {
	eb := &model.EvidenceBase{Studies: []model.EvidenceStudy{
		{Title: "A ```fenced``` title\nover two lines", Design: model.DesignRCT, Participants: 10},
	}}

	out := english().studyList(eb)

	assert.Equal(t, 2, strings.Count(out, "```"), "only the fence of the block")
	assert.Contains(t, out, "A '''fenced''' title over two lines")
	assert.True(t, strings.HasSuffix(out, "```\n\n"), "a blank line after the block")
	assert.Empty(t, english().studyList(&model.EvidenceBase{}), "no studies, no block")
	assert.Empty(t, english().studyList(&model.EvidenceBase{Direct: 3, Summary: "old"}), "an old artifact has no list")
}

func TestStudyListTitleIsCut(t *testing.T) {
	long := strings.Repeat("word ", 30)
	eb := &model.EvidenceBase{Studies: []model.EvidenceStudy{{Title: long, Design: model.DesignRCT, Participants: 10}}}

	for line := range strings.SplitSeq(english().studyList(eb), "\n") {
		if strings.Contains(line, "word") {
			title := strings.TrimSpace(line)
			assert.Len(t, []rune(title), listTitleChars)
			assert.True(t, strings.HasSuffix(title, "…"))
		}
	}
}

func TestDesignLabel(t *testing.T) {
	en, ru := english(), russian()

	for _, tc := range []struct {
		s      model.EvidenceStudy
		en, ru string
	}{
		{
			model.EvidenceStudy{Design: model.DesignRCT, Blinding: model.BlindingDouble},
			"RCT, double-blind", "РКИ, двойное слепое",
		},
		{model.EvidenceStudy{Design: model.DesignRCT, Blinding: model.BlindingOpen}, "RCT, open-label", "РКИ, открытое"},
		{
			model.EvidenceStudy{Design: model.DesignClusterRCT, Blinding: model.BlindingSingle},
			"cluster RCT, single-blind", "кластерное РКИ, одинарное слепое",
		},
		{
			model.EvidenceStudy{Design: model.DesignRCT, Blinding: model.BlindingUnclear},
			"RCT, blinding not stated", "РКИ, ослепление не указано",
		},
		{model.EvidenceStudy{Design: model.DesignRCT}, "RCT", "РКИ"},
		{
			model.EvidenceStudy{Design: model.DesignCohort, Blinding: model.BlindingDouble},
			"cohort", "когортное",
		},
		{
			model.EvidenceStudy{Design: model.DesignNonrandomizedTrial},
			"non-randomized trial", "нерандомизированное исследование",
		},
		{model.EvidenceStudy{Design: model.DesignCaseControl}, "case-control", "случай-контроль"},
		{model.EvidenceStudy{Design: model.DesignCrossSectional}, "cross-sectional", "поперечное"},
		{model.EvidenceStudy{Design: model.DesignCaseSeries}, "case series", "серия случаев"},
		{model.EvidenceStudy{Design: model.DesignSystematicReview}, "systematic review", "систематический обзор"},
		{
			model.EvidenceStudy{Design: model.DesignMetaAnalysis, Cochrane: true},
			"meta-analysis (Cochrane)", "метаанализ (Cochrane)",
		},
		{model.EvidenceStudy{Design: model.DesignNarrativeReview}, "narrative review", "нарративный обзор"},
		{model.EvidenceStudy{Design: model.DesignUnclear}, "design unclear", "дизайн неясен"},
		{model.EvidenceStudy{Design: oddValue}, oddValue, oddValue},
	} {
		assert.Equal(t, tc.en, en.designLabel(tc.s))
		assert.Equal(t, tc.ru, ru.designLabel(tc.s))
	}

	assert.Equal(t, "unclear", en.directionWord(""), "a missing direction reads as unclear")
	assert.Equal(t, "no effect", en.directionWord(model.DirectionNull))
	assert.Equal(t, "нет эффекта", ru.directionWord(model.DirectionNull))
}

func TestStudyDirection(t *testing.T) {
	for raw, want := range map[string]model.StudyDirection{
		"positive": model.DirectionSupports, "Positive": model.DirectionSupports, "null": model.DirectionNull,
		"negative": model.DirectionOpposite, "mixed": model.DirectionMixed, "unclear": model.DirectionUnclear,
		"": model.DirectionUnclear, oddValue: model.DirectionUnclear,
	} {
		assert.Equal(t, want, studyDirection(raw), raw)
	}
}

// The data of the list comes from the stage data: one entry per trial, the
// reports of one trial merged, the excluded papers left out, the pooled studies
// marked.
func TestEvidenceBaseStudies(t *testing.T) {
	const doiLate = "10.1/trial-final"

	st := evState(
		evPaper{
			doi: doiSolo, title: titleSolo, year: 2020, design: model.DesignRCT, blinding: model.BlindingDouble,
			size: 100, direction: directionPositive,
		},
		// two reports of one trial: the later one is kept for the name and the direction, the size
		// comes from the first report that states one
		evPaper{
			doi: "10.1/trial-early", title: "Trial, early report", year: 2018, design: model.DesignRCT,
			regs: []string{regEvidence}, direction: directionNull,
		},
		evPaper{
			doi: doiLate, title: "Trial, final report", year: 2021, design: model.DesignRCT, blinding: model.BlindingOpen,
			size: 300, regs: []string{regEvidence}, direction: directionNegative,
		},
		evPaper{doi: "10.1/cohort", title: "Cohort", design: model.DesignCohort, size: 5000, direction: directionMixed},
		evPaper{doi: doiCochrane, title: "Cochrane review", design: model.DesignMetaAnalysis, size: 9000},
		// left out of the list
		evPaper{doi: "10.1/retracted", design: model.DesignRCT, size: 999, retracted: true},
		evPaper{doi: "10.1/off", relevance: relevanceIndirect, design: model.DesignRCT, size: 77},
		evPaper{doi: "10.1/failed", failed: true},
	)
	pe := &model.PooledEstimate{Studies: []string{doiSolo, doiLate}}

	eb := evidenceBaseFor(st, pe, model.LangEN)
	require.NotNil(t, eb)

	require.Len(t, eb.Studies, eb.Direct, "one entry per direct study, repeat reports and exclusions not counted")
	assert.Equal(t, 4, eb.Direct)
	assert.Equal(t, 1, eb.RepeatReports)

	byTitle := map[string]model.EvidenceStudy{}
	for _, s := range eb.Studies {
		byTitle[s.Title] = s
	}

	assert.Equal(t, model.EvidenceStudy{
		DOI: doiSolo, Title: titleSolo, Year: 2020, Design: model.DesignRCT, Blinding: model.BlindingDouble,
		Participants: 100, Direction: model.DirectionSupports, Pooled: true,
	}, byTitle[titleSolo])

	merged := byTitle["Trial, final report"]
	assert.Equal(t, doiLate, merged.DOI)
	assert.Equal(t, 2021, merged.Year)
	assert.Equal(t, 300, merged.Participants, "the size of the report that states one")
	assert.Equal(t, model.DirectionOpposite, merged.Direction, "the direction of the kept report")
	assert.Equal(t, model.BlindingOpen, merged.Blinding)
	assert.True(t, merged.Pooled)
	assert.NotContains(t, byTitle, "Trial, early report", "a repeat report is not listed again")

	cohort := byTitle["Cohort"]
	assert.False(t, cohort.Pooled)
	assert.Equal(t, model.DirectionMixed, cohort.Direction)
	assert.Equal(t, 5000, cohort.Participants)

	review := byTitle["Cochrane review"]
	assert.True(t, review.Cochrane)
	assert.Zero(t, review.Participants, "a review recounts the participants of its trials")

	out := english().studyList(eb)
	assert.Equal(t, 1, strings.Count(out, "Trial, final report"))
	assert.NotContains(t, out, "retracted")
	assert.Contains(t, out, "Indirect or off-topic: 1 · not assessed: 1")
}

// A study is pooled when any report of its trial is in the pool: the pool may
// have kept a report other than the one the list names.
func TestEvidenceBaseStudiesPoolMarksATrialByAnyReport(t *testing.T) {
	st := evState(
		evPaper{doi: "10.1/early", year: 2018, design: model.DesignRCT, size: 80, regs: []string{regEvidence}},
		evPaper{doi: "10.1/late", year: 2021, design: model.DesignRCT, size: 80, regs: []string{regEvidence}},
	)

	eb := buildEvidenceBase(st, &model.PooledEstimate{Studies: []string{"10.1/EARLY"}})
	require.Len(t, eb.Studies, 1)
	assert.True(t, eb.Studies[0].Pooled)
	assert.Equal(t, "10.1/late", eb.Studies[0].DOI)

	eb = buildEvidenceBase(st, nil)
	assert.False(t, eb.Studies[0].Pooled)
}

// A paper without a title is named as the summary names it.
func TestEvidenceBaseStudyWithoutATitle(t *testing.T) {
	st := evState(evPaper{doi: doiA, design: model.DesignRCT, size: 10})
	st.Enriched[0].Title = ""
	st.Enriched[0].Authors = []string{"Smith J"}
	st.Enriched[0].Year = 2019

	assert.Equal(t, "Smith J 2019", buildEvidenceBase(st, nil).Studies[0].Title)
}

// The studies ride in the report artifact; a report without them (written
// before the list) carries no key and renders without the block.
func TestEvidenceBaseStudiesJSON(t *testing.T) {
	st := evState(evPaper{doi: doiA, design: model.DesignRCT, blinding: model.BlindingDouble, size: 100, year: 2020})
	r := model.Report{Verdict: verdictWeakEv, EvidenceBase: evidenceBaseFor(st, nil, model.LangEN)}

	b, err := json.Marshal(r)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"studies":[{"doi":"10.1/a","title":"Study 10.1/a","year":2020,"design":"rct",`+
		`"blinding":"double_blind","participants":100,"direction":"unclear"}]`)

	var back model.Report

	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, r.EvidenceBase.Studies, back.EvidenceBase.Studies)

	r.EvidenceBase.Studies = nil
	old, err := json.Marshal(r)
	require.NoError(t, err)
	assert.NotContains(t, string(old), `"studies":[{`)
}

// A single study with numbers is not pooled, so the list does not mark it,
// although the estimate names it.
func TestEvidenceBaseSingleStudyIsNotMarkedPooled(t *testing.T) {
	st := evState(evPaper{doi: doiSolo, title: titleSolo, design: model.DesignRCT, size: 100})
	pe := &model.PooledEstimate{Method: model.PoolSingleStudy, K: 1, Studies: []string{doiSolo}}

	eb := evidenceBaseFor(st, pe, model.LangEN)
	require.NotNil(t, eb)
	require.Len(t, eb.Studies, 1)
	assert.False(t, eb.Studies[0].Pooled)

	pe.Method = model.PoolRandomEffects
	assert.True(t, evidenceBaseFor(st, pe, model.LangEN).Studies[0].Pooled, "the same estimate, pooled")
}

// The pool keys a study by its normalized DOI, else its title: a pooled study
// without a DOI is marked by the title, and no other paper without a DOI is
// marked with it (an empty key matches nothing).
func TestEvidenceBaseMarksPooledStudiesWithoutDOI(t *testing.T) {
	st := evState(
		evPaper{title: titleNoDOI, design: model.DesignRCT, size: 50},
		evPaper{title: "Other, no DOI", design: model.DesignRCT, size: 60},
		evPaper{doi: "https://doi.org/10.1/Upper", title: "Pooled by DOI", design: model.DesignRCT, size: 70},
	)
	pe := &model.PooledEstimate{
		Method: model.PoolRandomEffects, K: 2,
		Studies: []string{titleNoDOI, "10.1/upper", ""},
	}

	assert.Equal(t, titleNoDOI, studyKey(&st.Enriched[0]))
	assert.Equal(t, "10.1/upper", studyKey(&st.Enriched[2]))
	assert.Empty(t, studyKey(&model.EnrichedPaper{}), "neither a DOI nor a title: no key")

	keys := pooledKeys(pe)
	assert.True(t, keys[titleNoDOI])
	assert.True(t, keys["10.1/upper"])
	assert.NotContains(t, keys, "", "no empty key")

	pooled := map[string]bool{}
	for _, s := range evidenceBaseFor(st, pe, model.LangEN).Studies {
		pooled[s.Title] = s.Pooled
	}

	assert.Equal(t, map[string]bool{titleNoDOI: true, "Other, no DOI": false, "Pooled by DOI": true}, pooled)

	// a paper with neither a DOI nor a title is not matched by an empty key
	blank := evState(evPaper{design: model.DesignRCT, size: 10})
	blank.Enriched[0].Title = ""

	emptyKey := &model.PooledEstimate{Method: model.PoolRandomEffects, Studies: []string{""}}
	blankStudies := evidenceBaseFor(blank, emptyKey, model.LangEN).Studies
	require.Len(t, blankStudies, 1)
	assert.False(t, blankStudies[0].Pooled)
}

// The block sits right under the evidence-base line in the report, in the
// language of the report, and an old report has none.
func TestRenderPlacesTheStudyList(t *testing.T) {
	md := RenderMarkdown(reportState())
	line := strings.Index(md, "**Evidence base:**")
	header := strings.Index(md, "Direct studies by number of participants (bar: log scale)\n\n```\n")
	consensus := strings.Index(md, "### Scientific consensus")

	require.Positive(t, line)
	assert.Greater(t, header, line)
	assert.Less(t, header, consensus)
	assert.Contains(t, md[line:header], "Indirect or off-topic: 1.\n\n", "the block follows the one-line summary")

	ru := RenderMarkdown(reportStateRU())
	assert.Contains(t, ru, "Прямые исследования по числу участников (полоса — логарифмическая шкала)\n\n```\n")
	assert.NotContains(t, ru, "Direct studies by number")

	old := reportState()
	old.Report.EvidenceBase.Studies = nil
	assert.NotContains(t, RenderMarkdown(old), "```")
	assert.Contains(t, RenderMarkdown(old), "**Evidence base:** 3 papers reviewed")
}
