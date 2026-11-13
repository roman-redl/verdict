package pipeline

// The wording of the computed lines of the report: the pooled estimate, the
// imprecision rating and the lack-of-blinding rating (render.go places them).

import (
	"fmt"
	"math"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// pooledLabel names what pooledText shows.
func pooledLabel(pe *model.PooledEstimate) string {
	if pe.Method == model.PoolSingleStudy {
		return "Effect of the one direct study with numbers (not pooled)"
	}

	if pe.Converted {
		return "Pooled effect of the direct studies (random effects, DerSimonian-Laird; " +
			"measures converted to one standardized scale)"
	}

	return "Pooled effect of the direct studies (random effects, DerSimonian-Laird)"
}

// pooledText renders the pooled numbers; Go formats them, the LLM never
// retypes them.
func pooledText(pe *model.PooledEstimate) string {
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

	s := fmt.Sprintf("%s (95%% CI %.*f to %.*f), %d %s", est, prec, pe.CILow, prec, pe.CIHigh,
		pe.K, plural(pe.K, "study", "studies"))

	if pe.Participants > 0 {
		s += fmt.Sprintf(", %d participants", pe.Participants)
	}

	if pe.Events > 0 {
		s += fmt.Sprintf(", %d events", pe.Events)
	}

	if pe.I2 != nil {
		s += fmt.Sprintf(", I² = %.0f%%", *pe.I2)
	}

	if pe.Stratum != "" {
		s += "; design: " + string(pe.Stratum)
	}

	if pe.Outcome != "" {
		s += "; outcome: " + pe.Outcome
	}

	if n := len(pe.LeftOut); n > 0 {
		s += fmt.Sprintf("; not pooled (%d %s): %s", n, plural(n, "study", "studies"), leftOutPreview(pe.LeftOut))
	}

	return s
}

// maxLeftOutShown caps the left-out entries a report line names.
const maxLeftOutShown = 5

// leftOutPreview names the first left-out studies with their reasons; the full
// list is pooled_estimate.left_out in the artifact.
func leftOutPreview(left []string) string {
	if len(left) <= maxLeftOutShown {
		return strings.Join(left, ", ")
	}

	return strings.Join(left[:maxLeftOutShown], ", ") + fmt.Sprintf(", and %d more", len(left)-maxLeftOutShown)
}

// imprecisionText renders the computed rating: the levels, then the reason
// (a sentence with the numbers).
func imprecisionText(imp *model.Imprecision) string {
	s := "not rated down"
	if imp.Levels > 0 {
		s = fmt.Sprintf("rated down %d %s", imp.Levels, plural(imp.Levels, "level", "levels"))
	}

	return s + " — " + imp.Reason
}

// blindingBiasText renders the computed rating like imprecisionText: serious
// (the levels), then the reason with the shares.
func blindingBiasText(bb *model.BlindingBias) string {
	return fmt.Sprintf("serious, rated down %d %s — %s", bb.Levels, plural(bb.Levels, "level", "levels"), bb.Reason)
}

// pooledLine is the one-line digest entry; empty without a pooled estimate.
func pooledLine(pe *model.PooledEstimate) string {
	if pe == nil {
		return ""
	}

	return pooledLabel(pe) + ": " + pooledText(pe) + "."
}
