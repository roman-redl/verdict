package model

import (
	"fmt"
	"strings"
)

// ScoreMetric says what the means of a continuous outcome are: the values at
// the end of follow-up or the change from baseline. The two are never put on
// one standardized scale (Cochrane Handbook 10.5.2).
type ScoreMetric string

const (
	// MetricFinal is the value measured at the end of follow-up.
	MetricFinal ScoreMetric = "final"
	// MetricChange is the change from baseline (a change score).
	MetricChange ScoreMetric = "change"
)

// ParseScoreMetric parses a metric as the critic writes it. The empty string is
// an error too: the caller decides what a missing value means (unknown).
func ParseScoreMetric(s string) (ScoreMetric, error) {
	m := ScoreMetric(strings.ToLower(strings.TrimSpace(s)))

	switch m {
	case MetricFinal, MetricChange:
		return m, nil
	}

	return "", fmt.Errorf("unknown score metric %q (want final | change)", s)
}
