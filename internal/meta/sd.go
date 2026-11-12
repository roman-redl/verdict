package meta

import "math"

// Restoring the SD of one arm from the other statistics a paper prints
// (Cochrane Handbook 6.5.2.2 and 6.5.2.3). Both functions take the statistics
// of a SINGLE arm; the standard error of the difference between two arms is not
// one of them and cannot give an arm's SD.

const (
	// smallSample is the arm size below which the Handbook asks for a value of
	// the t distribution instead of 1.96 when a 95% interval was calculated.
	smallSample = 60
	// handbookZ is the multiplier of a 95% interval for a large sample (the
	// Handbook divides the width by 3.92 = 2 x 1.96).
	handbookZ = 1.96
	// minArmSize is the smallest arm an SD can be restored for.
	minArmSize = 2
)

// finitePositive reports a finite number above zero.
func finitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

// SDFromSE is the SD of one arm from the standard error of its mean:
// SD = SE x sqrt(n).
func SDFromSE(se, n float64) (float64, error) {
	if !finitePositive(se) || !finitePositive(n) || n < minArmSize {
		return 0, invalid("standard error %v with n = %v", se, n)
	}

	return se * math.Sqrt(n), nil
}

// SDFromCI is the SD of one arm from the 95% confidence interval of its mean:
// SD = sqrt(n) x (upper - lower) / (2 t), where t is the 97.5% quantile of
// Student's t distribution with n - 1 degrees of freedom when the arm has fewer
// than 60 participants, and 1.96 otherwise.
func SDFromCI(low, high, n float64) (float64, error) {
	if !finitePositive(high-low) || !finitePositive(n) || n < minArmSize {
		return 0, invalid("interval %v to %v with n = %v", low, high, n)
	}

	mult := handbookZ

	if n < smallSample {
		t, err := TQuantile975(n - 1)
		if err != nil {
			return 0, err
		}

		mult = t
	}

	return math.Sqrt(n) * (high - low) / (2 * mult), nil
}
