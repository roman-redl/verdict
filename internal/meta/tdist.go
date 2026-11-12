package meta

import "math"

// Student's t quantile without a library: the cumulative distribution comes
// from the regularized incomplete beta function (continued fraction, modified
// Lentz), the quantile from bisection on it. Accurate to well below 1e-9, which
// is far finer than the rounding of the numbers a paper prints.

const (
	betaMaxIter = 300
	betaEps     = 1e-14
	betaTiny    = 1e-300
	// tTail is the one-sided tail probability of a 95% interval.
	tTail = 0.025
	// tBracket is the upper end of the quantile search; the 97.5% quantile at
	// one degree of freedom is 12.7.
	tBracket       = 1e4
	bisectionSteps = 200
)

// TQuantile975 is the 97.5% quantile of Student's t distribution with df
// degrees of freedom (df of at least 1): the multiplier of a 95% interval of a
// mean estimated from df + 1 observations.
func TQuantile975(df float64) (float64, error) {
	if math.IsNaN(df) || df < 1 || math.IsInf(df, 0) {
		return 0, invalid("degrees of freedom %v (want at least 1)", df)
	}

	lo, hi := 0.0, tBracket

	for range bisectionSteps {
		mid := (lo + hi) / 2
		if tUpperTail(mid, df) > tTail {
			lo = mid
		} else {
			hi = mid
		}
	}

	return (lo + hi) / 2, nil
}

// tUpperTail is P(T > t) for t >= 0: half of the regularized incomplete beta
// I_x(df/2, 1/2) with x = df / (df + t^2).
func tUpperTail(t, df float64) float64 {
	return 0.5 * betaInc(df/2, 0.5, df/(df+t*t))
}

// betaInc is the regularized incomplete beta function I_x(a, b).
func betaInc(a, b, x float64) float64 {
	if x <= 0 {
		return 0
	}

	if x >= 1 {
		return 1
	}

	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	front := math.Exp(lab - la - lb + a*math.Log(x) + b*math.Log1p(-x))

	// the continued fraction converges fast on this side of the mean
	if x < (a+1)/(a+b+2) {
		return front * betaFraction(a, b, x) / a
	}

	return 1 - front*betaFraction(b, a, 1-x)/b
}

// nonZero keeps a Lentz term away from zero.
func nonZero(v float64) float64 {
	if math.Abs(v) < betaTiny {
		return betaTiny
	}

	return v
}

// betaFraction is the continued fraction of the incomplete beta function.
func betaFraction(a, b, x float64) float64 {
	c := 1.0
	d := 1 / nonZero(1-(a+b)*x/(a+1))
	h := d

	for m := 1; m <= betaMaxIter; m++ {
		fm := float64(m)
		m2 := 2 * fm

		even := fm * (b - fm) * x / ((a - 1 + m2) * (a + m2))
		d = 1 / nonZero(1+even*d)
		c = nonZero(1 + even/c)
		h *= d * c

		odd := -(a + fm) * (a + b + fm) * x / ((a + m2) * (a + 1 + m2))
		d = 1 / nonZero(1+odd*d)
		c = nonZero(1 + odd/c)

		step := d * c
		h *= step

		if math.Abs(step-1) < betaEps {
			break
		}
	}

	return h
}
