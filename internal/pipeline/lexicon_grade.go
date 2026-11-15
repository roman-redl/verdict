package pipeline

// The wording of the computed certainty line (model.GradeAssessment): one
// compact line under the computed ratings, e.g. "start strong; risk of bias −1
// (attrition), imprecision −1; → moderate".

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// minus and plus are the signs of the levels in the line (U+2212 is the
// typographic minus); percent turns a share into percent.
const (
	minus   = "−"
	plus    = "+"
	percent = 100
)

// gradeWords are the words of the certainty line in one language.
type gradeWords struct {
	label, start, noDowngrades, noDirect, result string
	// differs is the warning for a computed level that is not the model's own
	// verdict: the computed level is argument 1, the model's level argument 2.
	differs string
	// riskOfBias ... publicationBias name the domains; upgrade names the raise
	// for a large effect.
	riskOfBias, inconsistency, indirectness, imprecision, publicationBias, upgrade string
	// causes name the items that lowered a domain, by the cause the artifact
	// stores; levels name the certainty levels.
	causes map[string]string
	levels map[string]string
}

// gradeText renders the certainty line: the start, each domain that lowered
// the level with its levels and causes, the raise, then the result. With no
// direct primary study the start and the domains do not matter: the line says so.
func (l lexicon) gradeText(g *model.GradeAssessment) string {
	w := &l.grade
	result := "; " + w.result + " " + l.gradeLevel(g.Level)

	if g.NoDirectStudy {
		return w.noDirect + result
	}

	parts := []string{}

	for _, d := range []struct {
		name   string
		domain model.GradeDomain
	}{
		{w.riskOfBias, g.RiskOfBias},
		{w.inconsistency, g.Inconsistency},
		{w.indirectness, g.Indirectness},
		{w.imprecision, g.Imprecision},
		{w.publicationBias, g.PublicationBias},
	} {
		if d.domain.Levels == 0 {
			continue
		}

		parts = append(parts, l.domainText(d.name, d.domain))
	}

	if g.Upgrade > 0 {
		parts = append(parts, w.upgrade+" "+plus+strconv.Itoa(g.Upgrade))
	}

	if len(parts) == 0 {
		parts = append(parts, w.noDowngrades)
	}

	return w.start + " " + l.gradeLevel(g.Start) + "; " + strings.Join(parts, ", ") + result
}

// verdictDiffers is the warning for a computed level that differs from the
// model's own verdict (both are lower-case levels).
func (l lexicon) verdictDiffers(computed, said string) string {
	return fmt.Sprintf(l.grade.differs, l.gradeLevel(computed), l.gradeLevel(said))
}

// domainText is "risk of bias −1 (lack of blinding, attrition 64%)": the
// levels, then the causes the domain has (a cause the table does not know is
// shown as stored), a cause with a share of the weight above one half followed
// by it.
func (l lexicon) domainText(name string, d model.GradeDomain) string {
	s := name + " " + minus + strconv.Itoa(d.Levels)
	if len(d.Causes) == 0 {
		return s
	}

	words := make([]string, len(d.Causes))

	for i, c := range d.Causes {
		words[i] = c
		if w, ok := l.grade.causes[c]; ok {
			words[i] = w
		}

		// the share of a cause the model's answer alone counts (an outcome
		// with a related share of the pool at or below one half) is not shown
		if share, ok := d.Shares[c]; ok && share > majority+shareEpsilon {
			words[i] += " " + strconv.Itoa(int(math.Round(share*percent))) + "%"
		}
	}

	return s + " (" + strings.Join(words, ", ") + ")"
}

// gradeLevel is the word of a certainty level; a value outside the vocabulary
// is shown as it is.
func (l lexicon) gradeLevel(level string) string {
	if w, ok := l.grade.levels[level]; ok {
		return w
	}

	return level
}

func englishGrade() gradeWords {
	return gradeWords{
		label: "Certainty (computed)", start: "start", noDowngrades: "no downgrades", result: "→",
		noDirect: "no direct primary study reporting the outcome",
		differs: "The verdict is the certainty level computed from the grade checklist (%[1]s); " +
			"the model's own level was %[2]s, so the rationale below may argue for that one.",
		riskOfBias: "risk of bias", inconsistency: "inconsistency", indirectness: "indirectness",
		imprecision: "imprecision", publicationBias: "publication bias", upgrade: "large effect",
		causes: map[string]string{
			model.CauseBlinding: "lack of blinding", model.CauseAttrition: "attrition",
			model.CauseAllocationFlaw: "allocation flaw", model.CauseSelectiveOutcome: "selective outcome",
			model.CauseStoppedEarlyForBenefit: "stopped early for benefit",
			model.CauseDirectionsConflict:     "directions conflict", model.CauseSizesDiffer: "sizes differ beyond chance",
			model.CausePopulation: "population", model.CauseIntervention: "intervention",
			model.CauseComparator: "comparator", model.CauseOutcome: "outcome",
		},
		levels: map[string]string{
			verdictStrong: "strong", verdictModerate: "moderate", verdictWeak: "weak", verdictInsufficient: "insufficient",
		},
	}
}

func russianGrade() gradeWords {
	return gradeWords{
		label: "Достоверность доказательств (расчёт)", start: "исходный уровень", noDowngrades: "без понижений",
		result: "→", noDirect: "нет прямых первичных исследований по исходу вопроса",
		differs: "Вывод показан по уровню достоверности, рассчитанному по фактическим ответам модели (%[1]s); " +
			"собственный уровень модели был другим (%[2]s), поэтому обоснование ниже может отстаивать именно его.",
		riskOfBias: "риск систематической ошибки", inconsistency: "несогласованность",
		indirectness: "непрямой характер доказательств", imprecision: "неточность",
		publicationBias: "публикационное смещение", upgrade: "большой эффект",
		causes: map[string]string{
			model.CauseBlinding: "отсутствие ослепления", model.CauseAttrition: "выбывание участников",
			model.CauseAllocationFlaw:         "изъян распределения",
			model.CauseSelectiveOutcome:       "избирательная публикация результатов",
			model.CauseStoppedEarlyForBenefit: "остановка досрочно из-за пользы",
			model.CauseDirectionsConflict:     "направления результатов расходятся",
			model.CauseSizesDiffer:            "размеры эффекта различаются сверх случайных",
			model.CausePopulation:             "популяция", model.CauseIntervention: "вмешательство",
			model.CauseComparator: "сравнение", model.CauseOutcome: "исход",
		},
		levels: map[string]string{
			verdictStrong: "сильный", verdictModerate: "умеренный", verdictWeak: "слабый", verdictInsufficient: "недостаточный",
		},
	}
}
