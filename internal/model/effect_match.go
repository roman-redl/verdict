package model

import (
	"fmt"
	"strings"
)

// OutcomeMatch says how the outcome an effect is measured for relates to the
// question's primary outcome. The critic answers it per effect; the pipeline
// pools only effects for the same outcome.
type OutcomeMatch string

const (
	// OutcomeSame is the question's primary outcome, defined the same way.
	OutcomeSame OutcomeMatch = "same"
	// OutcomeRelated is a surrogate, a composite, another definition or a
	// component of the question's outcome.
	OutcomeRelated OutcomeMatch = "related"
	// OutcomeDifferent is another outcome.
	OutcomeDifferent OutcomeMatch = "different"
)

// ParseOutcomeMatch parses an outcome match as the critic writes it, ignoring
// case. The empty string is an error too: the caller decides what a missing
// value means (an artifact written before the field existed).
func ParseOutcomeMatch(s string) (OutcomeMatch, error) {
	m := OutcomeMatch(strings.ToLower(strings.TrimSpace(s)))

	switch m {
	case OutcomeSame, OutcomeRelated, OutcomeDifferent:
		return m, nil
	}

	return "", fmt.Errorf("unknown outcome match %q (want same | related | different)", s)
}

// IsSame reports whether the effect may be pooled as the question's outcome.
// A missing value counts as the same outcome: "unknown" is not "different".
func (m OutcomeMatch) IsSame() bool { return m == "" || m == OutcomeSame }

// TimepointMatch says whether the follow-up an effect is measured at is the
// time frame the question asks about.
type TimepointMatch string

const (
	// TimepointYes is the question's time frame, or the question has none.
	TimepointYes TimepointMatch = "yes"
	// TimepointNo is another follow-up than the question's.
	TimepointNo TimepointMatch = "no"
	// TimepointUnclear means the follow-up or the frame cannot be told.
	TimepointUnclear TimepointMatch = "unclear"
)

// ParseTimepointMatch parses a time point match as the critic writes it,
// ignoring case. The empty string is an error too, as in ParseOutcomeMatch.
func ParseTimepointMatch(s string) (TimepointMatch, error) {
	m := TimepointMatch(strings.ToLower(strings.TrimSpace(s)))

	switch m {
	case TimepointYes, TimepointNo, TimepointUnclear:
		return m, nil
	}

	return "", fmt.Errorf("unknown timepoint match %q (want yes | no | unclear)", s)
}

// IsOff reports whether the effect is measured at another follow-up than the
// question's. A missing value is "unclear", which does not exclude.
func (m TimepointMatch) IsOff() bool { return m == TimepointNo }

// Cluster describes the clustering of a cluster-randomized trial, as far as
// the paper states it. The pipeline adjusts counts and means that do not
// account for clustering by the design effect.
type Cluster struct {
	Unit     string   `json:"unit,omitempty"`      // what was randomized: village, clinic, school
	Clusters *float64 `json:"clusters,omitempty"`  // the number of clusters randomized
	MeanSize *float64 `json:"mean_size,omitempty"` // participants per cluster, when stated
	ICC      *float64 `json:"icc,omitempty"`       // intracluster correlation, when reported
	// AdjustedForClustering is true when the reported estimate and interval
	// already account for clustering.
	AdjustedForClustering bool `json:"adjusted_for_clustering,omitempty"`
}
