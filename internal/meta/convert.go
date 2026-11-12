package meta

import "math"

// logORToSMD is sqrt(3) / pi, the factor that re-expresses a log odds ratio as
// a standardized mean difference (Cochrane Handbook 10.6: ln(OR) = SMD x
// pi / sqrt(3), the logistic distribution assumption).
var logORToSMD = math.Sqrt(3) / math.Pi

// SMDFromLogOR re-expresses a log odds ratio as a standardized mean difference:
// SMD = ln(OR) x sqrt(3) / pi, the standard error scaled by the same factor.
func SMDFromLogOR(s Study) Study {
	return Study{Y: s.Y * logORToSMD, SE: s.SE * logORToSMD}
}

// Reversed is the effect pointing the other way (the same standard error): the
// mean difference of the comparator against the first arm.
func (s Study) Reversed() Study {
	return Study{Y: -s.Y, SE: s.SE}
}
