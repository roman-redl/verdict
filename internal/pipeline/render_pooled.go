package pipeline

// The wording of the computed lines of the report: the pooled estimate, the
// imprecision rating and the lack-of-blinding rating (render.go places them).
// The labels and the units come from the lexicon; the reason texts of the
// imprecision and blinding ratings are generated in English.

import (
	"fmt"
	"math"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// pooledLabel names what pooledText shows.
func (l lexicon) pooledLabel(pe *model.PooledEstimate) string {
	if pe.Method == model.PoolSingleStudy {
		return l.pooledSingle
	}

	if pe.Converted {
		return l.pooledConverted
	}

	return l.pooledPlain
}

// pooledText renders the pooled numbers; Go formats them, the LLM never
// retypes them.
func (l lexicon) pooledText(pe *model.PooledEstimate) string {
	// one precision for the three numbers: two decimals, three when any of
	// them is small, so that a small effect does not read as zero
	prec := 2

	for _, v := range []float64{pe.Estimate, pe.CILow, pe.CIHigh} {
		if a := math.Abs(v); a != 0 && a < 0.1 {
			prec = 3
		}
	}

	est := fmt.Sprintf("%s %.*f", pe.Measure, prec, pe.Estimate)
	if pe.Unit != "" {
		est += " " + pe.Unit
	}

	s := fmt.Sprintf("%s (%s %.*f%s%.*f), %s", est, l.ci, prec, pe.CILow, l.ciTo, prec, pe.CIHigh,
		l.count(pe.K, l.studies))

	if pe.Participants > 0 {
		s += ", " + l.count(pe.Participants, l.participants)
	}

	if pe.Events > 0 {
		s += ", " + l.count(pe.Events, l.events)
	}

	if pe.I2 != nil {
		s += fmt.Sprintf(", I² = %.0f%%", *pe.I2)
	}

	if pe.Stratum != "" {
		s += "; " + l.design + ": " + l.stratum(pe.Stratum)
	}

	if pe.Outcome != "" {
		s += "; " + l.outcome + ": " + pe.Outcome
	}

	if n := len(pe.LeftOut); n > 0 {
		s += fmt.Sprintf("; %s (%s): %s", l.notPooled, l.count(n, l.studies), l.leftOutPreview(pe.LeftOut))
	}

	return s
}

// stratum names the design class of a pooled estimate; a value outside the
// vocabulary is shown as it is.
func (l lexicon) stratum(s model.Stratum) string {
	if name, ok := l.strata[s]; ok {
		return name
	}

	return string(s)
}

// maxLeftOutShown caps the left-out entries a report line names.
const maxLeftOutShown = 5

// leftOutPreview names the first left-out studies with their reasons; the full
// list is pooled_estimate.left_out in the artifact.
func (l lexicon) leftOutPreview(left []string) string {
	if len(left) <= maxLeftOutShown {
		return strings.Join(left, ", ")
	}

	return strings.Join(left[:maxLeftOutShown], ", ") + fmt.Sprintf(l.andMore, len(left)-maxLeftOutShown)
}

// imprecisionText renders the computed rating: the levels, then the reason
// (a sentence with the numbers).
func (l lexicon) imprecisionText(imp *model.Imprecision) string {
	s := l.notRatedDown
	if imp.Levels > 0 {
		s = l.ratedDown + " " + l.count(imp.Levels, l.levels)
	}

	return s + " — " + imp.Reason
}

// blindingBiasText renders the computed rating like imprecisionText: serious
// (the levels), then the reason with the shares.
func (l lexicon) blindingBiasText(bb *model.BlindingBias) string {
	return l.serious + ", " + l.ratedDown + " " + l.count(bb.Levels, l.levels) + " — " + bb.Reason
}

// pooledLine is the one-line digest entry; empty without a pooled estimate.
func (l lexicon) pooledLine(pe *model.PooledEstimate) string {
	if pe == nil {
		return ""
	}

	return l.pooledLabel(pe) + ": " + l.pooledText(pe) + "."
}
