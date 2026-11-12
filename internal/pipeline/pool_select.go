package pipeline

// Which studies may enter a pool. A meta-analysis combines only what answers
// one question: the same outcome at the same time point, from studies of one
// design class (Cochrane Handbook 5.2.1, 6.2.4, 10.10.1, 24.6.2.1). The
// critic labels each effect (model.Effect); the rules here are code.

import (
	"fmt"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// The reasons a study with usable numbers is left out of the pool, as they
// appear in pooled_estimate.left_out ("doi (reason)") and in the log.
const (
	leftNonRandomized = "non-randomized"
	leftUnadjusted    = "unadjusted non-randomized"
	leftDesignUnknown = "design unknown"
	leftOtherOutcome  = "other outcome"
	leftOtherTime     = "other time point"
	leftCluster       = "cluster trial not adjusted for clustering"
	leftRepeatReport  = "repeat report of a trial"
	leftSameResults   = "same results"
	leftOtherMeasure  = "other measure"
	// the two reasons below arise only beside a unified SMD pool (pool_unify.go)
	leftScaleUnknown = "direction of scale unknown"
	leftMetricMix    = "change vs final scores"
)

// leftOut is a study kept out of the pool: the kind of decision (one of the
// reasons above) and its text for pooled_estimate.left_out.
type leftOut struct {
	kind string
	text string
}

// leftOutBecause is the left-out entry of a study kept out for a reason that
// needs no more words than the reason itself.
func leftOutBecause(doi, kind string) *leftOut {
	return &leftOut{kind: kind, text: doi + " (" + kind + ")"}
}

// leftOutMeasure is the entry of a study that reports the outcome on another
// measure than the pool's: "doi (MEASURE)".
func leftOutMeasure(ps *pooledStudy) leftOut {
	return leftOut{kind: leftOtherMeasure, text: fmt.Sprintf("%s (%s)", ps.doi, ps.measure)}
}

// leftOutTexts lists the entries for pooled_estimate.left_out; nil when there
// are none.
func leftOutTexts(items []leftOut) []string {
	if len(items) == 0 {
		return nil
	}

	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].text
	}

	return out
}

// countKinds counts the left-out studies per kind of decision; the kinds come
// in the order they first occur.
func countKinds(items []leftOut) (kinds []string, count map[string]int) {
	count = map[string]int{}

	for i := range items {
		if count[items[i].kind] == 0 {
			kinds = append(kinds, items[i].kind)
		}

		count[items[i].kind]++
	}

	return kinds, count
}

// logLeftOut logs the decisions that kept studies out of the pool: one line per
// kind with how many studies it concerns.
func (p *Pipeline) logLeftOut(items []leftOut) {
	kinds, count := countKinds(items)

	for _, kind := range kinds {
		p.log.Info("effects not pooled", "reason", kind, "count", count[kind])
	}
}

// noPoolWarning is the report warning for a corpus that ended without a pooled
// estimate although some studies had numbers: the left-out entries have no
// estimate to hang on, so the reader gets them here. Empty when nothing was
// kept out.
func noPoolWarning(res poolResult) string {
	if res.est != nil || len(res.leftOut) == 0 {
		return ""
	}

	kinds, count := countKinds(res.leftOut)
	parts := make([]string, len(kinds))

	for i, kind := range kinds {
		parts[i] = fmt.Sprintf("%d %s", count[kind], kind)
	}

	return "no pooled estimate; studies with numbers that were not pooled: " + strings.Join(parts, ", ")
}

// designClass tells the three kinds of study the pool rules know apart.
type designClass int

const (
	classUnknown       designClass = iota // the design could not be told
	classRandomized                       // parallel, cluster or crossover trial
	classNonRandomized                    // every other primary design
)

func classOf(d model.StudyDesign) designClass {
	switch {
	case !d.IsKnown():
		return classUnknown
	case d.IsRandomized():
		return classRandomized
	}

	return classNonRandomized
}

// poolStratum decides which design class the pool is made of. Randomized trials
// and non-randomized studies are never pooled together, and the randomized ones
// come first: when the corpus holds ANY direct randomized study — whether or not
// it has usable numbers — only the randomized stratum may be pooled, and without
// numbers there is no pooled estimate (the synthesis judges the trials one by
// one). Only without a direct randomized study is the non-randomized stratum
// pooled. A corpus in which no study's design is known pools as it always did
// and has no stratum.
func poolStratum(st *State, crits []model.Critique) model.Stratum {
	nonRandomized := false

	for i := range st.Enriched {
		e, c := &st.Enriched[i], crits[i]
		if c.Relevance != relevanceDirect || e.Flags.Retracted || !isStudyReport(e, c) {
			continue
		}

		switch classOf(resolveDesign(e, c)) {
		case classRandomized:
			return model.StratumRandomized
		case classNonRandomized:
			nonRandomized = true
		case classUnknown:
		}
	}

	if nonRandomized {
		return model.StratumNonRandomized
	}

	return ""
}

// stratumKind says why a study of the design cannot join the pool of the
// stratum; empty when it can. A non-randomized study joins only with an
// adjusted estimate: raw counts do not control confounding (24.6.1).
func stratumKind(d model.StudyDesign, ef *model.Effect, stratum model.Stratum) string {
	class := classOf(d)

	switch stratum {
	case model.StratumRandomized:
		switch class {
		case classRandomized:
			return ""
		case classNonRandomized:
			return leftNonRandomized
		case classUnknown:
			return leftDesignUnknown
		}
	case model.StratumNonRandomized:
		switch {
		case class == classUnknown:
			return leftDesignUnknown
		case !ef.Adjusted:
			return leftUnadjusted
		}
	}

	return ""
}

// eligibilityKind says why the effect cannot be pooled for the question: the
// study's design class, the outcome and the time point. Empty when it can. A
// missing label is "unknown", which is not "different": a missing outcome match
// reads as the same outcome, a missing time point match as unclear.
func eligibilityKind(d model.StudyDesign, ef *model.Effect, stratum model.Stratum) string {
	if kind := stratumKind(d, ef, stratum); kind != "" {
		return kind
	}

	switch {
	case !ef.OutcomeMatch.IsSame():
		return leftOtherOutcome
	case ef.TimepointMatch.IsOff():
		return leftOtherTime
	}

	return ""
}
