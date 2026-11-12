package pipeline

// The GRADE imprecision rating and the effect direction, computed in Go from
// the pooled estimate: the synthesis judged both itself before, and its
// answers varied between runs and mixed up "no effect" with "not enough
// data". Now the model reads the rating and the conclusion it applies to; it
// only tells which way the claim says the outcome moves. A direction is stated
// only when the pooled interval excludes no effect; an interval that includes
// it gives "no difference seen" or, when too wide, "cannot tell" (grade).

import (
	"fmt"

	"github.com/roman-redl/verdict/internal/grade"
	"github.com/roman-redl/verdict/internal/model"
)

// gradeInput maps the pooled estimate onto the arithmetic package's input.
func gradeInput(pe *model.PooledEstimate) (grade.Input, error) {
	in := grade.Input{
		Estimate: pe.Estimate, CILow: pe.CILow, CIHigh: pe.CIHigh, Participants: pe.Participants,
	}

	switch pe.Measure {
	case model.MeasureRR, model.MeasureHR:
		in.Measure = grade.MeasureRatio
	case model.MeasureOR:
		in.Measure = grade.MeasureOddsRatio
	case model.MeasureSMD:
		in.Measure = grade.MeasureSMD
	case model.MeasureMD:
		in.Measure = grade.MeasureMD

		if pe.SD != nil {
			in.SD = *pe.SD
		}
	default:
		return in, fmt.Errorf("measure %q has no imprecision rule", pe.Measure)
	}

	return in, nil
}

// assessImprecision rates the imprecision of the pooled estimate. It returns
// nil without an estimate and, with a warning, when the numbers cannot be
// rated: the synthesis then judges imprecision itself, as before.
func (p *Pipeline) assessImprecision(pe *model.PooledEstimate) *model.Imprecision {
	if pe == nil {
		return nil
	}

	in, err := gradeInput(pe)
	if err != nil {
		p.log.Warn("imprecision not computed: unusable pooled estimate", "err", err)

		return nil
	}

	a, err := grade.AssessImprecision(in)
	if err != nil {
		p.log.Warn("imprecision not computed: unusable pooled estimate", "err", err)

		return nil
	}

	// grade and model name the targets alike on purpose: the stored value
	// goes through the model's parser, like any value read from outside
	target, err := model.ParseImprecisionTarget(string(a.Target))
	if err != nil {
		p.log.Error("imprecision not computed: unknown target from the rule", "err", err)

		return nil
	}

	return &model.Imprecision{
		Levels: a.Levels, Target: target, BandLow: a.BandLow, BandHigh: a.BandHigh,
		Reason: a.Reason, SizeChecked: a.SizeChecked,
	}
}

// directionFromPooled is the effect direction implied by the computed
// conclusion and the change the claim states: supports or opposite when the
// interval excludes no effect, no_effect when it includes no effect and is not
// too wide, unclear when it is too wide to say anything. Without the rating,
// or without a usable claim_change where the target needs one, the synthesis's
// own direction stays (a missing or invalid claim_change is logged). When the
// code's direction differs from the synthesis's, the code wins and the second
// result is the report warning that records both.
func (p *Pipeline) directionFromPooled(imp *model.Imprecision, claimChange string,
	said model.EffectDirection,
) (direction model.EffectDirection, warning string) {
	if imp == nil {
		return said, ""
	}

	// no_effect and unclear do not depend on the claim, so they are imposed
	// even when the synthesis returned no usable claim_change
	var claim model.ClaimChange

	if imp.Target.DependsOnClaim() {
		var err error

		claim, err = model.ParseClaimChange(claimChange)
		if err != nil {
			p.log.Warn("effect_direction kept from the synthesis: no usable claim_change", "err", err)

			return said, ""
		}
	}

	computed, ok := imp.Target.DirectionFor(claim)
	if !ok {
		p.log.Error("effect_direction kept from the synthesis: the imprecision target implies no direction",
			"target", imp.Target)

		return said, ""
	}

	if computed == said {
		return said, ""
	}

	return computed, fmt.Sprintf("effect_direction set from the pooled estimate: %s (the synthesis said %s)",
		computed, said)
}
