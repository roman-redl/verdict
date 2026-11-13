package pipeline

import (
	"fmt"

	"github.com/roman-redl/verdict/internal/model"
)

// russian is the Russian wording of the report. The numbers keep the decimal
// point and the measure names (RR, SMD, I²) stay as they are written in the
// literature.
func russian() lexicon {
	studies := forms{"исследование", "исследования", "исследований"}

	return lexicon{
		lang:      model.LangRU,
		formIndex: russianForm,

		reportTitle: "Отчёт о доказательствах", question: "Вопрос", verdict: "Вердикт",
		effectDirection: "Направление эффекта", imprecision: "Неточность (расчёт)",
		blindingBias: "Риск систематической ошибки из-за отсутствия ослепления (расчёт)",
		comparable:   "Исследований с числами по исходу", evidenceBase: "База доказательств",
		consensus: "Научный консенсус", directTally: "Направление результатов прямых исследований",
		generated: "Сформировано", rationale: "Обоснование", matrix: "Таблица доказательств",
		contradictions: "Противоречия", gaps: "Пробелы и ограничения", warnings: "Предупреждения по данным",
		matrixHeader: "| Исследование | Год | Дизайн | N | Цитаты за/против | Направление | Качество | " +
			"Релевантность | Примечание |",
		fullTexts: "Полные тексты не получены",
		fullTextsIntro: "Эти прямые исследования оценены по аннотации: полный текст в открытом доступе получить не " +
			"удалось (в гибридном режиме текст ищется только для ключевых исследований). Чтобы прочитать исследование " +
			"целиком, скачайте его PDF и сохраните в каталоге запуска как `fulltext/<файл>` (или выполните " +
			"`verdict addpdf --run-dir <run> <file.pdf>` — DOI команда прочитает из самого PDF), затем повторите запуск " +
			"с `--resume`.",

		verdictNames: map[string]string{
			verdictStrong:       "СИЛЬНЫЕ ДОКАЗАТЕЛЬСТВА — устоявшиеся данные",
			verdictModerate:     "УМЕРЕННЫЕ ДОКАЗАТЕЛЬСТВА — ограниченные, но согласованные данные",
			verdictWeak:         "СЛАБЫЕ ДОКАЗАТЕЛЬСТВА — скудные или низкокачественные данные",
			verdictInsufficient: "НЕДОСТАТОЧНЫЕ ДОКАЗАТЕЛЬСТВА — данных недостаточно для вывода",
		},
		consensusNames: map[string]string{
			consensusBroad:        "широкое согласие",
			consensusContested:    "спорный вопрос",
			consensusEmerging:     "формируется",
			consensusNone:         "консенсуса нет",
			consensusInsufficient: "данных для консенсуса недостаточно",
		},
		effectNames: map[model.EffectDirection]string{
			model.EffectSupports: "подтверждает утверждение",
			model.EffectNoEffect: "значимого эффекта нет",
			model.EffectOpposite: "обратный эффект",
			model.EffectUnclear:  "определить нельзя",
		},
		tallyNames: map[string]string{
			resultPositive: "положительных", resultNull: "нулевых", resultNegative: "отрицательных",
			resultMixed: "смешанных", valueUnclear: "неясных",
		},
		item: "%[2]s: %[1]d",
		blindingNames: map[model.Blinding]string{
			model.BlindingDouble: "двойное слепое", model.BlindingSingle: "одинарное слепое",
			model.BlindingOpen: "открытое", model.BlindingUnclear: "ослепление не указано", "": "ослепление не оценено",
		},

		pooledSingle: "Эффект единственного прямого исследования с числами (не объединён)",
		pooledPlain:  "Объединённый эффект прямых исследований (случайные эффекты, DerSimonian-Laird)",
		pooledConverted: "Объединённый эффект прямых исследований (случайные эффекты, DerSimonian-Laird; " +
			"меры приведены к одной стандартизованной шкале)",
		ci: "95% ДИ от", ciTo: " до ",
		studies:      studies,
		levels:       forms{"уровень", "уровня", "уровней"},
		participants: forms{"участник", "участника", "участников"},
		events:       forms{"событие", "события", "событий"},
		design:       "дизайн", outcome: "исход", notPooled: "не объединены", andMore: " и ещё %d",
		strata: map[model.Stratum]string{
			model.StratumRandomized: "рандомизированные", model.StratumNonRandomized: "нерандомизированные",
		},
		notRatedDown: "оценка не понижена", ratedDown: "оценка понижена на", serious: "серьёзный риск",

		sentence: russianSentence(studies),
		list:     russianList(),
	}
}

// russianVerb picks the verb form that agrees with a count in the object:
// "отвечает 1 работа", "отвечают 12 работ".
func russianVerb(n int, one, many string) string {
	if russianForm(n) == 0 {
		return one
	}

	return many
}

func russianSentence(studies forms) evidenceWords {
	works := forms{"работа", "работы", "работ"}

	return evidenceWords{
		reviewed: func(n int) string { return fmt.Sprintf("Просмотрено работ: %d", n) },
		directHead: func(n int) string {
			return fmt.Sprintf("на вопрос напрямую %s %d %s", russianVerb(n, "отвечает", "отвечают"), n, works[russianForm(n)])
		},
		repeat: func(n int) string {
			return fmt.Sprintf(" (повторных публикаций тех же исследований, учтённых один раз: %d)", n)
		},
		noneDirect: "ни одна работа не отвечает на вопрос напрямую.",
		randomized: forms{
			"рандомизированное исследование", "рандомизированных исследования", "рандомизированных исследований",
		},
		observational: forms{
			"обсервационное исследование", "обсервационных исследования", "обсервационных исследований",
		},
		reviews: forms{
			"систематический обзор или метаанализ", "систематических обзора или метаанализа",
			"систематических обзоров или метаанализов",
		},
		other:     forms{"другая публикация", "другие публикации", "других публикаций"},
		otherNote: " (нарративные обзоры, руководства, другой или неясный дизайн)",
		sizeNone:  "размер не указан", sizeNoneAll: "размер не указан у всех %d", sizeNoneFor: "размер не указан у %d",
		median: "медиана", largest: "максимум",
		noPool:       "Не объединено: нет прямых исследований с пригодными числами по исходу.",
		notPooledOne: "Не объединено: одно исследование с числами", pooledPrefix: "Объединено: ",
		studies: studies, trials: fixed("РКИ"),
		retracted: "Отозвано и исключено", indirect: "Косвенные или не по теме", notAssessed: "Не оценены (сбой оценки)",
	}
}

func russianList() listWords {
	return listWords{
		header:      "Прямые исследования по числу участников (полоса — логарифмическая шкала)",
		moreFmt:     "… и ещё %d",
		review:      "обзор",
		notAssessed: "не оценены",
		pooled:      "в объединённой оценке",
		designs: map[model.StudyDesign]string{
			model.DesignRCT: "РКИ", model.DesignClusterRCT: "кластерное РКИ", model.DesignCrossoverRCT: "перекрёстное РКИ",
			model.DesignNonrandomizedTrial: "нерандомизированное исследование", model.DesignCohort: "когортное",
			model.DesignCaseControl: "случай-контроль", model.DesignCrossSectional: "поперечное",
			model.DesignCaseSeries: "серия случаев", model.DesignSystematicReview: "систематический обзор",
			model.DesignMetaAnalysis: "метаанализ", model.DesignNarrativeReview: "нарративный обзор",
			model.DesignGuideline: "руководство", model.DesignOther: "другой дизайн", model.DesignUnclear: "дизайн неясен",
		},
		directions: map[model.StudyDirection]string{
			model.DirectionSupports: "за", model.DirectionNull: "нет эффекта", model.DirectionOpposite: "против",
			model.DirectionMixed: "смешанный", model.DirectionUnclear: "неясно",
		},
	}
}
