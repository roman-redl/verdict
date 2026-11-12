package model

import (
	"fmt"
	"strings"
)

// EffectDirection is where the evidence points relative to the claim of the
// question: the synthesis states it explicitly (Report.EffectDirection), next
// to the verdict (how certain) and the consensus status (how much the studies
// agree).
type EffectDirection string

const (
	// EffectSupports means the claim holds.
	EffectSupports EffectDirection = "supports"
	// EffectNoEffect means the evidence shows no meaningful difference.
	EffectNoEffect EffectDirection = "no_effect"
	// EffectOpposite means the effect goes the opposite way.
	EffectOpposite EffectDirection = "opposite"
	// EffectUnclear means the direction cannot be stated.
	EffectUnclear EffectDirection = "unclear"
)

// ParseEffectDirection parses a direction as the synthesis writes it. The
// empty string is an error too: the caller decides what a missing value means
// (an old artifact, or a model that left the field out).
func ParseEffectDirection(s string) (EffectDirection, error) {
	d := EffectDirection(strings.ToLower(strings.TrimSpace(s)))

	switch d {
	case EffectSupports, EffectNoEffect, EffectOpposite, EffectUnclear:
		return d, nil
	}

	return "", fmt.Errorf("unknown effect direction %q (want supports | no_effect | opposite | unclear)", s)
}

// Measure is an effect measure that can be pooled.
type Measure string

const (
	MeasureRR  Measure = "RR"  // risk ratio
	MeasureOR  Measure = "OR"  // odds ratio
	MeasureHR  Measure = "HR"  // hazard ratio
	MeasureMD  Measure = "MD"  // mean difference, in the unit of the outcome
	MeasureSMD Measure = "SMD" // standardized mean difference (Hedges' g)
)

// ParseMeasure parses a measure name, ignoring case and spaces.
func ParseMeasure(s string) (Measure, error) {
	m := Measure(strings.ToUpper(strings.TrimSpace(s)))

	switch m {
	case MeasureRR, MeasureOR, MeasureHR, MeasureMD, MeasureSMD:
		return m, nil
	}

	return "", fmt.Errorf("unknown effect measure %q (want RR | OR | HR | MD | SMD)", s)
}

// IsRatio reports whether the measure is a ratio, pooled on the log scale.
func (m Measure) IsRatio() bool {
	return m == MeasureRR || m == MeasureOR || m == MeasureHR
}

// Arm is the numbers one study arm reports for the question's outcome: events
// and total for a binary outcome, mean, SD and n for a continuous one. A
// field is a pointer so that "not stated" differs from a real zero.
// Percent is the alternative to Events for a binary outcome the study gives
// only as a percentage of the arm (it needs Total); the pipeline turns it into
// a count. SE and CILow/CIHigh are the standard error and the 95% interval of
// THIS arm's mean, copied when the SD is not stated: the pipeline restores the
// SD from them. They are never the statistics of the difference between arms.
type Arm struct {
	Events  *float64 `json:"events,omitempty"`
	Percent *float64 `json:"percent,omitempty"` // 0-100, instead of events
	Total   *float64 `json:"total,omitempty"`
	Mean    *float64 `json:"mean,omitempty"`
	SD      *float64 `json:"sd,omitempty"`
	N       *float64 `json:"n,omitempty"`
	SE      *float64 `json:"se,omitempty"`
	CILow   *float64 `json:"ci_low,omitempty"`
	CIHigh  *float64 `json:"ci_high,omitempty"`
}

// Effect is what the critic extracts from a study's text for the question's
// outcome: the raw numbers per arm, or, when only an estimate is reported (or
// the estimate is the better source), the estimate with its confidence
// interval and the interval's level. The numbers are copied from
// the text, never computed by the model; the pipeline pools them.
// Intervention is the claim's intervention, Control its comparator.
type Effect struct {
	Outcome   string `json:"outcome,omitempty"`   // which outcome the numbers are for
	Timepoint string `json:"timepoint,omitempty"` // when it was measured, if stated
	Unit      string `json:"unit,omitempty"`      // of a continuous outcome (mean, SD, MD)

	// OutcomeMatch is how the outcome relates to the question's primary one;
	// empty (an artifact written before the field existed) reads as "same".
	OutcomeMatch OutcomeMatch `json:"outcome_match,omitempty"`
	// TimepointWeeks is the follow-up the effect is measured at, in weeks;
	// nil when the text does not state it. TimepointMatch says whether it is
	// the question's time frame; empty reads as "unclear".
	TimepointWeeks *float64       `json:"timepoint_weeks,omitempty"`
	TimepointMatch TimepointMatch `json:"timepoint_match,omitempty"`
	// Adjusted is true when the numbers are an adjusted estimate (aOR, aRR,
	// aHR) of a non-randomized study; false or absent means crude numbers.
	Adjusted bool `json:"adjusted,omitempty"`
	// Cluster is set for a cluster-randomized trial.
	Cluster *Cluster `json:"cluster,omitempty"`

	Intervention *Arm `json:"intervention,omitempty"`
	Control      *Arm `json:"control,omitempty"`

	// The direction of the outcome, needed to put studies on one standardized
	// scale; nil or empty means the critic could not tell (an artifact written
	// before the fields existed has none). HigherIsBetter is for a continuous
	// outcome: true when a higher value is the better state of the patient.
	// Metric is for a continuous outcome: final values or change scores.
	// EventIsHarm is for a binary outcome: true when the counted event is a bad
	// outcome (death, relapse), false when it is a good one (remission).
	HigherIsBetter *bool       `json:"higher_is_better,omitempty"`
	Metric         ScoreMetric `json:"metric,omitempty"`
	EventIsHarm    *bool       `json:"event_is_harm,omitempty"`

	// The reported estimate.
	Measure string   `json:"measure,omitempty"` // RR | OR | HR | MD | SMD
	Value   *float64 `json:"value,omitempty"`
	CILow   *float64 `json:"ci_low,omitempty"`
	CIHigh  *float64 `json:"ci_high,omitempty"`
	// CILevel is the level of the interval in percent (90, 95, 97.2); absent
	// means 95.
	CILevel *float64 `json:"ci_level,omitempty"`
	// Reversed is true when the reported estimate compares the claim's
	// comparator against its intervention (control vs intervention), as printed;
	// the pipeline flips it. It applies to the reported estimate only: the arms'
	// numbers are already assigned to intervention and control.
	Reversed bool `json:"reversed,omitempty"`
}

// PoolMethod names how a PooledEstimate was obtained.
type PoolMethod string

const (
	// PoolRandomEffects is the DerSimonian-Laird random-effects model over
	// two or more studies.
	PoolRandomEffects PoolMethod = "random_effects_dl"
	// PoolSingleStudy is the effect of the only study that reported one: no
	// pooling, no heterogeneity.
	PoolSingleStudy PoolMethod = "single_study"
)

// Stratum is the design class a pooled estimate is made of: randomized trials
// and non-randomized studies are never pooled together.
type Stratum string

const (
	// StratumRandomized is randomized trials (parallel, cluster, crossover).
	StratumRandomized Stratum = "randomized"
	// StratumNonRandomized is every other primary design; the GRADE starting
	// level is low.
	StratumNonRandomized Stratum = "non-randomized"
)

// PooledEstimate is the pooled effect of the direct studies (or the effect of
// the single study that reported one), computed by the pipeline from the
// critics' extracted numbers.
type PooledEstimate struct {
	Method PoolMethod `json:"method"`
	// Stratum is the design class of the pooled studies; empty when their
	// design is unknown, and in artifacts written before the field existed.
	Stratum  Stratum `json:"stratum,omitempty"`
	Measure  Measure `json:"measure"`
	Estimate float64 `json:"estimate"` // ratios on the natural scale, differences in the outcome's unit
	CILow    float64 `json:"ci_low"`
	CIHigh   float64 `json:"ci_high"`
	K        int     `json:"k"`
	// Participants is the total across the pooled studies; zero when any
	// study's size is unknown.
	Participants int `json:"participants,omitempty"`
	// I2 is the heterogeneity in percent (0-100); nil for a single study.
	I2 *float64 `json:"i2,omitempty"`
	// Unit is the outcome's unit of a pooled MD.
	Unit string `json:"unit,omitempty"`
	// Converted is true for an SMD that Go built from studies on different
	// measures of one outcome (means and counts): every study is oriented so
	// that a POSITIVE value favours the intervention, whatever the direction of
	// the original scales, and the claim is read against that orientation.
	Converted bool `json:"converted,omitempty"`
	// SD is the outcome's standard deviation of a pooled MD (the
	// participant-weighted mean of the studies' pooled SDs); nil when any
	// study gave only an estimate, and for the other measures.
	SD *float64 `json:"sd,omitempty"`
	// Outcome is what the pooled numbers measure, as the critics named it
	// (with the time point when stated); it can be the complement of the
	// claim's outcome, which the direction is read against.
	Outcome string   `json:"outcome,omitempty"`
	Studies []string `json:"studies"` // DOIs
	// LeftOut lists the direct studies with usable numbers that are not in the
	// pool, each as "doi (reason)": another measure ("doi (MEASURE)"; a study
	// from counts is labelled RR, its default measure, even when the pool is an
	// OR that it could have joined), another outcome or time point, another
	// design class, an unadjusted non-randomized or cluster estimate, a repeat
	// report of a trial, the same results as another paper, and, beside a
	// converted SMD, a study whose scale direction is unknown or whose means
	// are change scores against final values.
	LeftOut []string `json:"left_out,omitempty"`
}
