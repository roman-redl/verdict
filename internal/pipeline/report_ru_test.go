package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// headingModerateRU is the verdict heading of the Russian test report.
const headingModerateRU = "## Вердикт: УМЕРЕННЫЕ ДОКАЗАТЕЛЬСТВА"

// reportStateRU is the run of reportState with a Russian report: the same
// numbers and enum values, the evidence base worded in Russian, the labels
// left to the renderer.
func reportStateRU() *State {
	st := reportState()
	st.Report.Language = model.LangRU
	st.Report.EvidenceBase = evidenceBaseFor(st, st.Report.PooledEstimate, model.LangRU)

	return st
}

func TestRenderMarkdownGoldenRU(t *testing.T) {
	golden(t, "report.golden.ru.md", RenderMarkdown(reportStateRU()))
}

// A report without the language field, and one that names English, render the
// same: the English golden is the reference for both.
func TestRenderMarkdownEnglishByDefault(t *testing.T) {
	implicit := RenderMarkdown(reportState())

	st := reportState()
	st.Report.Language = model.LangEN

	assert.Equal(t, implicit, RenderMarkdown(st))
}

// The Russian report keeps what a program reads in English: the enum values in
// the matrix and the numbers; and it translates the labels and the sentences.
func TestRussianReportKeepsEnumsAndTranslatesLabels(t *testing.T) {
	md := RenderMarkdown(reportStateRU())

	for _, want := range []string{
		"# Отчёт о доказательствах", "**Вопрос:**", headingModerateRU,
		"**Направление эффекта:** подтверждает утверждение",
		"**Объединённый эффект прямых исследований (случайные эффекты, DerSimonian-Laird):** " +
			"SMD -0.220 (95% ДИ от -0.400 до -0.030), 3 исследования, 1234 участника",
		"**Неточность (расчёт):** оценка не понижена — ",
		"**Риск систематической ошибки из-за отсутствия ослепления (расчёт):** " +
			"серьёзный риск, оценка понижена на 1 уровень — ",
		"**База доказательств:** Просмотрено работ: 3; на вопрос напрямую отвечают 2 работы",
		"### Научный консенсус: спорный вопрос",
		"**Направление результатов прямых исследований:** положительных: 1 / смешанных: 1",
		"_Сформировано: 2026-11-01 12:30 UTC_", "### Обоснование", "### Таблица доказательств",
		"| Исследование | Год | Дизайн | N | Цитаты за/против | Направление | Качество | " +
			"Релевантность | Примечание |",
		"| 2021 | rct | 118 | 12/3 | positive | high | direct |",
		"### Противоречия", "### Пробелы и ограничения", "### Предупреждения по данным", "### Полные тексты не получены",
	} {
		assert.Contains(t, md, want)
	}

	for _, gone := range []string{"Evidence Report", "Verdict", "Rationale", "Scientific consensus", "Contradictions"} {
		assert.NotContains(t, md, gone)
	}
}

// Every verdict level has a Russian name; a value outside the vocabulary is
// shown in capitals, as in English.
func TestRussianLabels(t *testing.T) {
	ru := russian()

	assert.Equal(t, "СИЛЬНЫЕ ДОКАЗАТЕЛЬСТВА — устоявшиеся данные", ru.verdictLabel("Strong"))
	assert.Contains(t, ru.verdictLabel(verdictModerate), "УМЕРЕННЫЕ ДОКАЗАТЕЛЬСТВА")
	assert.Contains(t, ru.verdictLabel("weak"), "СЛАБЫЕ ДОКАЗАТЕЛЬСТВА")
	assert.Contains(t, ru.verdictLabel("insufficient"), "НЕДОСТАТОЧНЫЕ ДОКАЗАТЕЛЬСТВА")
	assert.Equal(t, "ODD", ru.verdictLabel("odd"))

	assert.Equal(t, "широкое согласие", ru.consensusLabel("broad_agreement"))
	assert.Equal(t, "—", ru.consensusLabel(""))
	assert.Equal(t, "подтверждает утверждение", ru.effectDirectionLabel(model.EffectSupports))
	assert.Equal(t, "значимого эффекта нет", ru.effectDirectionLabel(model.EffectNoEffect))
	assert.Equal(t, "обратный эффект", ru.effectDirectionLabel(model.EffectOpposite))
	assert.Equal(t, "определить нельзя", ru.effectDirectionLabel(model.EffectUnclear))
	assert.Equal(t, "odd", ru.effectDirectionLabel("odd"))
}

func TestRussianComputedLines(t *testing.T) {
	ru := russian()

	assert.Equal(t, "оценка не понижена — r", ru.imprecisionText(&model.Imprecision{Reason: "r"}))
	assert.Equal(t, "оценка понижена на 1 уровень — r", ru.imprecisionText(&model.Imprecision{Levels: 1, Reason: "r"}))
	assert.Equal(t, "оценка понижена на 2 уровня — r", ru.imprecisionText(&model.Imprecision{Levels: 2, Reason: "r"}))
	assert.Equal(t, "серьёзный риск, оценка понижена на 1 уровень — r",
		ru.blindingBiasText(&model.BlindingBias{Levels: 1, Reason: "r"}))

	pe := &model.PooledEstimate{
		Method: model.PoolRandomEffects, Measure: model.MeasureRR, Estimate: 0.49, CILow: 0.34, CIHigh: 0.7,
		K: 5, Participants: 400, Events: 21, I2: new(92.0), Stratum: model.StratumNonRandomized,
		Outcome: "Смертность", LeftOut: []string{"a", "b"},
	}
	assert.Equal(t, "RR 0.49 (95% ДИ от 0.34 до 0.70), 5 исследований, 400 участников, 21 событие, I² = 92%; "+
		"дизайн: нерандомизированные; исход: Смертность; не объединены (2 исследования): a, b", ru.pooledText(pe))
	assert.Contains(t, ru.pooledLabel(pe), "Объединённый эффект прямых исследований")
	assert.Contains(t, ru.pooledLabel(&model.PooledEstimate{Method: model.PoolSingleStudy}), "не объединён")
	assert.Contains(t, ru.pooledLabel(&model.PooledEstimate{Converted: true}), "стандартизованной шкале")

	pe.LeftOut = []string{"a", "b", "c", "d", "e", "f", "g"}
	assert.Contains(t, ru.pooledText(pe), "a, b, c, d, e и ещё 2")

	assert.Equal(t, "положительных: 1 / нулевых: 1", russian().directionTally(tallyState(
		tallyPaper{doi: doiA, relevance: relevanceDirect, direction: directionPositive},
		tallyPaper{doi: doiB, relevance: relevanceDirect, direction: directionNull},
	)))
}

// The summary sentence in Russian: the same facts as the English example, with
// the plural forms and the verb agreeing with the counts.
func TestRussianEvidenceText(t *testing.T) {
	ru := russian()
	eb := &model.EvidenceBase{
		Reviewed: 40, Direct: 12, Indirect: 28,
		Randomized: &model.EvidenceGroup{
			Studies: 5, DoubleBlind: 3, Open: 1, BlindingNotStated: 1,
			Participants: 2310, MedianSize: 380, Largest: 1105, LargestStudy: "Big trial",
		},
		Observational: &model.EvidenceGroup{
			Studies: 4, Participants: 18400, MedianSize: 3000, Largest: 9800, LargestStudy: "Big cohort",
		},
		Reviews: &model.EvidenceGroup{Studies: 3, Cochrane: 1},
	}
	pe := &model.PooledEstimate{
		Method: model.PoolRandomEffects, Stratum: model.StratumRandomized, K: 4, Participants: 2010, Events: 310,
	}

	assert.Equal(t, "Просмотрено работ: 40; на вопрос напрямую отвечают 12 работ: "+
		"5 рандомизированных исследований (двойное слепое: 3, открытое: 1, ослепление не указано: 1; "+
		"2310 участников, медиана 380, максимум 1105 — Big trial), "+
		"4 обсервационных исследования (18400 участников, медиана 3000, максимум 9800 — Big cohort), "+
		"3 систематических обзора или метаанализа (1 Cochrane). "+
		"Объединено: 4 РКИ, 2010 участников, 310 событий. Косвенные или не по теме: 28.", ru.evidenceText(eb, pe))

	eb.Reviewed, eb.NotAssessed, eb.Retracted, eb.RepeatReports = 38, 2, 1, 2
	text := ru.evidenceText(eb, pe)
	assert.Contains(t, text, "(повторных публикаций тех же исследований, учтённых один раз: 2)")
	assert.Contains(t, text, " Отозвано и исключено: 1.")
	assert.True(t, strings.HasSuffix(text, "Не оценены (сбой оценки): 2."), text)
}

func TestRussianEvidenceTextEdges(t *testing.T) {
	ru := russian()

	one := &model.EvidenceBase{Reviewed: 1, Direct: 1, Reviews: &model.EvidenceGroup{Studies: 1}}
	assert.Equal(t, "Просмотрено работ: 1; на вопрос напрямую отвечает 1 работа: 1 систематический обзор или метаанализ. "+
		"Не объединено: нет прямых исследований с пригодными числами по исходу. Косвенные или не по теме: 0.",
		ru.evidenceText(one, nil))

	none := &model.EvidenceBase{Reviewed: 2, Indirect: 2}
	assert.Equal(t, "Просмотрено работ: 2; ни одна работа не отвечает на вопрос напрямую. "+
		"Не объединено: нет прямых исследований с пригодными числами по исходу. Косвенные или не по теме: 2.",
		ru.evidenceText(none, nil))

	other := &model.EvidenceBase{
		Reviewed: 3, Direct: 2, Other: 1,
		Observational: &model.EvidenceGroup{Studies: 1, SizeNotStated: 1},
	}
	assert.Contains(t, ru.evidenceText(other, nil),
		"1 обсервационное исследование (размер не указан), 1 другая публикация (нарративные обзоры")

	single := &model.PooledEstimate{Method: model.PoolSingleStudy, Participants: 90}
	assert.Equal(t, "Не объединено: одно исследование с числами (90 участников).", ru.pooledSentence(single))
}

func TestPluralForms(t *testing.T) {
	for n, want := range map[int]int{
		0: 2, 1: 0, 2: 1, 4: 1, 5: 2, 10: 2, 11: 2, 12: 2, 14: 2, 15: 2, 20: 2,
		21: 0, 22: 1, 25: 2, 101: 0, 111: 2, 112: 2, 121: 0, -1: 0, -3: 1,
	} {
		assert.Equal(t, want, russianForm(n), "ru %d", n)
	}

	assert.Equal(t, 0, englishForm(1))
	assert.Equal(t, 2, englishForm(0))
	assert.Equal(t, 2, englishForm(2))

	ru := russian()
	assert.Equal(t, "21 участник", ru.count(21, ru.participants))
	assert.Equal(t, "3 участника", ru.count(3, ru.participants))
	assert.Equal(t, "11 участников", ru.count(11, ru.participants))
	assert.Equal(t, "отвечает", russianVerb(1, "отвечает", "отвечают"))
	assert.Equal(t, "отвечают", russianVerb(12, "отвечает", "отвечают"))

	en := english()
	assert.Equal(t, "1 study", en.count(1, en.studies))
	assert.Equal(t, "0 studies", en.count(0, en.studies))
}

func TestLexiconFor(t *testing.T) {
	assert.Equal(t, model.LangRU, lexiconFor(model.LangRU).lang)
	assert.Equal(t, model.LangEN, lexiconFor(model.LangEN).lang)
	assert.Equal(t, model.LangEN, lexiconFor("").lang, "a report from before the field is English")
}

// Both lexicons name every value a table is keyed by, so that no label falls
// back to a raw enum value in either language.
func TestLexiconsAreComplete(t *testing.T) {
	for _, lx := range []lexicon{english(), russian()} {
		for _, v := range []string{verdictStrong, verdictModerate, verdictWeak, verdictInsufficient} {
			assert.Contains(t, lx.verdictNames, v, lx.lang)
		}

		for _, c := range []string{"broad_agreement", "contested", "emerging", "no_consensus", "insufficient_data"} {
			assert.Contains(t, lx.consensusNames, c, lx.lang)
		}

		for _, d := range []model.EffectDirection{
			model.EffectSupports, model.EffectNoEffect, model.EffectOpposite, model.EffectUnclear,
		} {
			assert.Contains(t, lx.effectNames, d, lx.lang)
		}

		for _, w := range []string{resultPositive, resultNull, resultNegative, resultMixed, valueUnclear} {
			assert.Contains(t, lx.tallyNames, w, lx.lang)
		}

		for _, b := range []model.Blinding{
			model.BlindingDouble, model.BlindingSingle, model.BlindingOpen, model.BlindingUnclear, "",
		} {
			assert.Contains(t, lx.blindingNames, b, lx.lang)
		}

		for _, s := range []model.Stratum{model.StratumRandomized, model.StratumNonRandomized} {
			assert.Contains(t, lx.strata, s, lx.lang)
		}

		for _, d := range model.StudyDesigns() {
			assert.Contains(t, lx.list.designs, d, lx.lang)
		}

		for _, d := range []model.StudyDirection{
			model.DirectionSupports, model.DirectionNull, model.DirectionOpposite, model.DirectionMixed,
			model.DirectionUnclear,
		} {
			assert.Contains(t, lx.list.directions, d, lx.lang)
		}
	}
}

// The prompt of the synthesis carries the language of the report: hard rule 5
// is swapped, the rest of the prompt is the same.
func TestSynthesisPromptLanguage(t *testing.T) {
	require.Equal(t, 1, strings.Count(synthesisSystemPrompt, languageRuleEN), "the rule to swap is in the prompt once")

	assert.Equal(t, synthesisSystemPrompt, synthesisPrompt(model.LangEN), "English is the base prompt, unchanged")
	assert.Equal(t, synthesisSystemPrompt, synthesisPrompt(""))

	ru := synthesisPrompt(model.LangRU)
	assert.Contains(t, ru, "in Russian")
	assert.Contains(t, ru, "Enum values, JSON keys and DOIs stay exactly as specified in English")
	assert.Contains(t, ru, "paper titles stay as published")
	assert.NotContains(t, ru, languageRuleEN)
	assert.Contains(t, synthesisSystemPrompt, "in English")
	assert.Len(t, ru, len(synthesisSystemPrompt)-len(languageRuleEN)+len(languageRuleRU))
}

func TestResolveLanguage(t *testing.T) {
	for _, tc := range []struct{ name, opt, cfg, want string }{
		{"the default", "", "", "en"},
		{"the config", "", "ru", "ru"},
		{"the flag beats the config", "en", "ru", "en"},
		{"the flag", "ru", "", "ru"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveLanguage(model.Language(tc.opt), model.Language(tc.cfg))
			require.NoError(t, err)
			assert.Equal(t, model.Language(tc.want), got)
		})
	}

	_, err := resolveLanguage("de", "")
	require.Error(t, err, "Options built in code are validated too")
}

// systemSpy records the system prompt of every call.
type systemSpy struct {
	scriptCompleter
	systems []string
}

func (s *systemSpy) Complete(ctx context.Context, system, user string, o CompleteOptions) (string, Usage, error) {
	s.systems = append(s.systems, system)

	return s.scriptCompleter.Complete(ctx, system, user, o)
}

// A run in Russian: the synthesis is asked for Russian text, the report names
// its language and report.md is labelled in it; the config default applies
// when the run names none, and the run's language beats the config's.
func TestRunReportLanguage(t *testing.T) {
	for _, tc := range []struct {
		name        string
		opt, cfg    model.Language
		want        model.Language
		wantHeading string
	}{
		{"flag", model.LangRU, "", model.LangRU, headingModerateRU},
		{"config", "", model.LangRU, model.LangRU, headingModerateRU},
		{"flag over config", model.LangEN, model.LangRU, model.LangEN, "## Verdict: MODERATE"},
		{"default", "", "", model.LangEN, "## Verdict: MODERATE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, _ := runFakes(t, batchReply, synthesisReply)
			spy := &systemSpy{replies: []string{batchReply, synthesisReply}}
			p.clients.LLM = spy
			p.set.Language = tc.cfg
			runDir := filepath.Join(t.TempDir(), "run")

			st, _, err := p.Run(t.Context(), Options{
				DOIFile: doiList(t, listDOIA, listDOIB), RunDir: runDir, TextMode: TextLean, Language: tc.opt,
			})
			require.NoError(t, err)

			assert.Equal(t, tc.want, st.Report.Language)
			require.Len(t, spy.systems, 2, "one batched critique + one synthesis")
			assert.Equal(t, synthesisPrompt(tc.want), spy.systems[1])
			assert.Equal(t, synthesisSystemPrompt != spy.systems[1], tc.want == model.LangRU)
			assert.Equal(t, evidenceBaseFor(st, st.Pooled, tc.want).Summary, st.Report.EvidenceBase.Summary)

			//nolint:gosec // reads a file the run just wrote into the test's temp directory
			report, err := os.ReadFile(filepath.Join(runDir, "report.md"))
			require.NoError(t, err)
			assert.Contains(t, string(report), tc.wantHeading)

			// the language rides in the checkpoint: a reload renders the same report
			loaded, err := LoadState(runDir)
			require.NoError(t, err)
			assert.Equal(t, tc.want, loaded.Report.Language)
			assert.Equal(t, string(report), RenderMarkdown(loaded))
		})
	}
}

// A run with a language the code does not know fails before any stage.
func TestRunRejectsAnUnknownLanguage(t *testing.T) {
	p, _, llm := runFakes(t, batchReply, synthesisReply)

	_, _, err := p.Run(t.Context(), Options{
		DOIFile: doiList(t, listDOIA), RunDir: filepath.Join(t.TempDir(), "run"), Language: "de",
	})
	require.Error(t, err)
	assert.Zero(t, llm.calls)
}
