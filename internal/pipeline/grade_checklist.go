package pipeline

// The certainty level of the verdict, computed in Go. The synthesis model used
// to judge how SERIOUS each risk-of-bias and indirectness problem was, and the
// level flipped between repeats on those borderline calls. Now it answers
// factual yes/no items (model.GradeChecklist) and the code does the arithmetic:
//
//	level = start - risk of bias - inconsistency - indirectness - imprecision
//	        - publication bias + upgrade, within insufficient..strong
//
// with the start by the design of the evidence, the lack-of-blinding and the
// imprecision ratings as the pipeline computed them (pool_blinding.go,
// imprecision.go; where it computed none the model rates the imprecision
// itself, as a number of levels), the risk of bias aggregated by weight from the
// per-study answers (grade_risk.go), and the caps below. The model does not say
// whether a direct study exists. The model's own "verdict" field is only
// compared with the result.

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// The certainty scale: a level is its index.
const (
	levelInsufficient = iota
	levelWeak
	levelModerate
	levelStrong
)

// certaintyNames are the verdict words by level.
var certaintyNames = [...]string{
	levelInsufficient: verdictInsufficient,
	levelWeak:         verdictWeak,
	levelModerate:     verdictModerate,
	levelStrong:       verdictStrong,
}

const (
	// maxRiskOfBiasLevels caps the risk-of-bias domain (very serious).
	maxRiskOfBiasLevels = 2
	// maxImprecisionLevels caps the imprecision rating (very serious).
	maxImprecisionLevels = 2
	// minComparableStudies is how many comparable studies inconsistency needs
	// to be judged across.
	minComparableStudies = 2
)

// errChecklist marks an answer whose grade_checklist is missing or invalid.
var errChecklist = errors.New("unusable grade_checklist")

// certaintyInput is everything the arithmetic reads. Checklist must be normalized.
type certaintyInput struct {
	// Randomized is true when the evidence starts at strong (see randomizedStart).
	Randomized bool
	// NoDirectStudy is true when no direct primary study is in the evidence
	// (noDirectStudy).
	NoDirectStudy bool
	// BlindingFired is true when the pipeline's lack-of-blinding rule fired.
	BlindingFired bool
	// Comparable is the number of comparable studies (inconsistency_basis).
	Comparable int
	// Imprecision is the rating computed from the pooled estimate; nil when the
	// pipeline computed none (no estimate, or one it cannot rate), when the
	// imprecision is the checklist's imprecision_without_pool.
	Imprecision *model.Imprecision
	// Studies are the direct primary studies with their weights, the basis of
	// the per-study risk-of-bias answers.
	Studies   []riskStudy
	Checklist model.GradeChecklist
}

// computeGrade runs the arithmetic and returns every step of it. It is pure.
func computeGrade(in certaintyInput) model.GradeAssessment {
	start := levelWeak
	if in.Randomized {
		start = levelStrong
	}

	a := model.GradeAssessment{
		Start:           certaintyNames[start],
		RiskOfBias:      riskOfBiasDomain(in.BlindingFired, in.Studies, &in.Checklist.RiskOfBias),
		Inconsistency:   inconsistencyDomain(in.Comparable, &in.Checklist.Inconsistency),
		Indirectness:    indirectnessDomain(&in.Checklist.Indirectness),
		PublicationBias: publicationBiasDomain(&in.Checklist.PublicationBias),
		NoDirectStudy:   in.NoDirectStudy,
		Checklist:       in.Checklist,
	}

	if in.Imprecision != nil {
		a.Imprecision = imprecisionDomain(in.Imprecision)
	} else {
		a.Imprecision = imprecisionAnswerDomain(in.Checklist.ImprecisionWithoutPool)
	}

	// only non-randomized evidence is raised: randomized starts at the top
	if !in.Randomized && in.Checklist.LargeEffect.IsYes() {
		a.Upgrade = 1
	}

	level := start + a.Upgrade -
		a.RiskOfBias.Levels - a.Inconsistency.Levels - a.Indirectness.Levels -
		a.Imprecision.Levels - a.PublicationBias.Levels
	level = min(max(level, levelInsufficient), levelStrong)

	if in.NoDirectStudy {
		level = levelInsufficient
	}

	a.Level = certaintyNames[level]

	return a
}

// inconsistencyDomain is one level when two or more studies are comparable and
// the model found conflicting directions or sizes that differ beyond chance;
// with fewer studies the answers are ignored (a single study cannot be
// inconsistent).
func inconsistencyDomain(studies int, it *model.InconsistencyItems) model.GradeDomain {
	if studies < minComparableStudies {
		return model.GradeDomain{}
	}

	var causes []string

	if it.DirectionsConflict.IsYes() {
		causes = append(causes, model.CauseDirectionsConflict)
	}

	if it.SizesDifferBeyondChance.IsYes() {
		causes = append(causes, model.CauseSizesDiffer)
	}

	return model.GradeDomain{Levels: min(len(causes), 1), Causes: causes}
}

// indirectnessDomain is one level when any of the four elements differs from
// the question.
func indirectnessDomain(it *model.IndirectnessItems) model.GradeDomain {
	var causes []string

	for _, c := range []struct {
		answer model.YesNo
		cause  string
	}{
		{it.Population, model.CausePopulation},
		{it.Intervention, model.CauseIntervention},
		{it.Comparator, model.CauseComparator},
		{it.Outcome, model.CauseOutcome},
	} {
		if c.answer.IsYes() {
			causes = append(causes, c.cause)
		}
	}

	return model.GradeDomain{Levels: min(len(causes), 1), Causes: causes}
}

// imprecisionDomain takes the levels computed from the pooled estimate,
// clamped to 0..2.
func imprecisionDomain(computed *model.Imprecision) model.GradeDomain {
	return model.GradeDomain{Levels: min(max(computed.Levels, 0), maxImprecisionLevels)}
}

// imprecisionAnswerDomain takes the model's imprecision_without_pool (used only
// when the pipeline computed no imprecision), clamped to 0..2; a missing answer
// (normalization requires it, so only a checklist built by hand lacks it) is 0.
func imprecisionAnswerDomain(answer *int) model.GradeDomain {
	if answer == nil {
		return model.GradeDomain{}
	}

	return model.GradeDomain{Levels: min(max(*answer, 0), maxImprecisionLevels)}
}

// publicationBiasDomain is one level when the model suspects it.
func publicationBiasDomain(it *model.PublicationBiasItem) model.GradeDomain {
	if !it.Suspected.IsYes() {
		return model.GradeDomain{}
	}

	return model.GradeDomain{Levels: 1}
}

// randomizedStart tells whether the evidence starts at strong. The stratum of
// the pooled estimate decides; without an estimate, or with no stratum in it
// (the design of the pooled studies is unknown), the best design among the
// direct studies does: any randomized one starts at strong, else weak.
func randomizedStart(pe *model.PooledEstimate, eb *model.EvidenceBase) bool {
	if pe != nil && pe.Stratum != "" {
		return pe.Stratum == model.StratumRandomized
	}

	return eb != nil && eb.Randomized != nil
}

// noDirectStudy tells whether no direct primary study is in the evidence: the
// evidence base (which already leaves out retracted papers, non-study papers
// and the papers the critic found only indirect) has no direct study that is
// not a review. It depends on the data only, never on the model's answer (its
// consensus_status included).
func noDirectStudy(eb *model.EvidenceBase) bool {
	return len(directPrimaryStudies(eb)) == 0
}

// directPrimaryStudies are the studies of the evidence base that are primary
// reports: the evidence base lists the direct ones, reviews and guidelines
// among them.
func directPrimaryStudies(eb *model.EvidenceBase) []model.EvidenceStudy {
	if eb == nil {
		return nil
	}

	var out []model.EvidenceStudy

	for _, s := range eb.Studies {
		if !s.Design.IsSecondary() {
			out = append(out, s)
		}
	}

	return out
}

// checkChecklist is the answer check of the synthesis call: the checklist must
// be there and valid. It normalizes the answers in place. imprecisionRequired
// is true when the pipeline computed no imprecision (the input has no
// imprecision field), so the model has to rate it.
func (r *synthesisResult) checkChecklist(imprecisionRequired bool) error {
	if r.GradeChecklist == nil {
		return fmt.Errorf("%w: the grade_checklist object is missing", errChecklist)
	}

	if err := r.GradeChecklist.Normalize(imprecisionRequired); err != nil {
		return fmt.Errorf("%w: %w", errChecklist, err)
	}

	return nil
}

// verdictWord is a verdict level as the model wrote it, in its canonical
// lower-case form; ok is false for a word outside the vocabulary.
func verdictWord(s string) (word string, ok bool) {
	word = strings.ToLower(strings.TrimSpace(s))

	return word, slices.Contains(certaintyNames[:], word)
}

// fallbackVerdict is the verdict when there is no usable checklist: the
// synthesis's own, but never above what the data allow. With no direct primary
// study the level is insufficient whatever the model said, and a word outside
// the vocabulary (the report knows four levels) is replaced with insufficient,
// the most cautious one, like an unknown effect_direction is with unclear.
func (p *Pipeline) fallbackVerdict(eb *model.EvidenceBase, said string) string {
	word, ok := verdictWord(said)

	switch {
	case noDirectStudy(eb):
		if word != verdictInsufficient {
			p.log.Warn("verdict set to insufficient: no direct primary study", "synthesis", said)
		}

		return verdictInsufficient
	case !ok:
		p.log.Warn("verdict replaced with insufficient: the synthesis gave no known level", "synthesis", said)

		return verdictInsufficient
	}

	return word
}

// certainty is the verdict of the report: the computed level, with the steps
// that led to it, when the answer has a usable checklist; else (checklistErr
// set, after the JSON retry) the synthesis's own verdict (fallbackVerdict), with
// a warning in the log and no Grade in the report. A computed level that
// differs from the synthesis's own verdict wins, with a warning in the log that
// records both and one in the report (in the language of lx), since the
// rationale is the model's and may argue for its own level.
func (p *Pipeline) certainty(st *State, eb *model.EvidenceBase, jr *synthesisResult,
	checklistErr error, lx lexicon,
) (verdict string, grade *model.GradeAssessment) {
	if checklistErr != nil || jr.GradeChecklist == nil {
		p.log.Warn("verdict kept from the synthesis: no usable grade_checklist", "err", checklistErr)

		return p.fallbackVerdict(eb, jr.Verdict), nil
	}

	a := computeGrade(certaintyInput{
		Randomized:    randomizedStart(st.Pooled, eb),
		NoDirectStudy: noDirectStudy(eb),
		BlindingFired: st.BlindingBias != nil,
		Comparable:    comparableStudies(st),
		Imprecision:   st.Imprecision,
		Studies:       p.riskWeights(st),
		Checklist:     *jr.GradeChecklist,
	})

	p.warnOnFallbacks(st, jr.GradeChecklist)

	if !strings.EqualFold(strings.TrimSpace(jr.Verdict), a.Level) {
		p.log.Warn("verdict set from the grade checklist", "computed", a.Level, "synthesis", jr.Verdict,
			"start", a.Start, "risk_of_bias", a.RiskOfBias.Levels, "inconsistency", a.Inconsistency.Levels,
			"indirectness", a.Indirectness.Levels, "imprecision", a.Imprecision.Levels,
			"publication_bias", a.PublicationBias.Levels, "upgrade", a.Upgrade)

		// an empty verdict names no level the reader could compare with
		if said, _ := verdictWord(jr.Verdict); said != "" {
			st.Warnings = append(st.Warnings, lx.verdictDiffers(a.Level, said))
		}
	}

	return a.Level, &a
}
