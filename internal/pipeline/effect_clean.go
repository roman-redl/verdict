package pipeline

import "github.com/roman-redl/verdict/internal/model"

// usableEffect returns a copy of the critic's extracted effect without the
// numbers that are placeholders rather than data, or nil when nothing usable
// is left. The critic sometimes fills a number the paper does not give with a
// zero (an interval 0 to 0, an arm of 0 events out of 0): the synthesis must
// not reason from those, so only the cleaned copy goes into its payload. The
// input is never modified; the float pointers are shared but only read.
// Pooling keeps its own validation (deriveEffect) on the original.
func usableEffect(ef *model.Effect) *model.Effect {
	if ef == nil {
		return nil
	}

	out := *ef
	out.Intervention = usableArm(ef.Intervention)
	out.Control = usableArm(ef.Control)

	if degenerateInterval(ef) {
		out.CILow, out.CIHigh, out.CILevel = nil, nil, nil
	}

	if out.Intervention == nil && out.Control == nil && out.Value == nil {
		return nil
	}

	return &out
}

// degenerateInterval reports an interval that cannot be a real one: both
// bounds given with zero width, or a value outside them. Only a complete
// interval is judged; a single bound is left as it is.
func degenerateInterval(ef *model.Effect) bool {
	if ef.CILow == nil || ef.CIHigh == nil {
		return false
	}

	low, high := min(*ef.CILow, *ef.CIHigh), max(*ef.CILow, *ef.CIHigh)

	if low == high {
		return true
	}

	return ef.Value != nil && (*ef.Value < low || *ef.Value > high)
}

// usableArm returns a copy of the arm without its unusable groups of fields,
// or nil when no field is left. A total of zero or less voids the counts, an
// n of zero or less or a negative SD voids the continuous summary.
func usableArm(a *model.Arm) *model.Arm {
	if a == nil {
		return nil
	}

	out := *a

	if a.Total != nil && *a.Total <= 0 {
		out.Events, out.Percent, out.Total = nil, nil, nil
	}

	if (a.N != nil && *a.N <= 0) || (a.SD != nil && *a.SD < 0) {
		out.Mean, out.SD, out.N, out.SE, out.CILow, out.CIHigh = nil, nil, nil, nil, nil, nil
	}

	if out == (model.Arm{}) {
		return nil
	}

	return &out
}
