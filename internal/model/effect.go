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
// a count.
type Arm struct {
	Events  *float64 `json:"events,omitempty"`
	Percent *float64 `json:"percent,omitempty"` // 0-100, instead of events
	Total   *float64 `json:"total,omitempty"`
	Mean    *float64 `json:"mean,omitempty"`
	SD      *float64 `json:"sd,omitempty"`
	N       *float64 `json:"n,omitempty"`
}

// Effect is what the critic extracts from a study's text for the question's
// outcome: the raw numbers per arm, or, when only an estimate is reported (or
// the estimate is the better source), the estimate with its confidence
// interval and the interval's level. The numbers are copied from
// the text, never computed by the model; the pipeline pools them.
// Intervention is the claim's intervention, Control its comparator.
type Effect struct {
	Outcome      string `json:"outcome,omitempty"`   // which outcome the numbers are for
	Timepoint    string `json:"timepoint,omitempty"` // when it was measured, if stated
	Unit         string `json:"unit,omitempty"`      // of a continuous outcome (mean, SD, MD)
	Intervention *Arm   `json:"intervention,omitempty"`
	Control      *Arm   `json:"control,omitempty"`

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

// PooledEstimate is the pooled effect of the direct studies (or the effect of
// the single study that reported one), computed by the pipeline from the
// critics' extracted numbers.
type PooledEstimate struct {
	Method   PoolMethod `json:"method"`
	Measure  Measure    `json:"measure"`
	Estimate float64    `json:"estimate"` // ratios on the natural scale, differences in the outcome's unit
	CILow    float64    `json:"ci_low"`
	CIHigh   float64    `json:"ci_high"`
	K        int        `json:"k"`
	// Participants is the total across the pooled studies; zero when any
	// study's size is unknown.
	Participants int `json:"participants,omitempty"`
	// I2 is the heterogeneity in percent (0-100); nil for a single study.
	I2 *float64 `json:"i2,omitempty"`
	// Unit is the outcome's unit of a pooled MD.
	Unit string `json:"unit,omitempty"`
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
	// pool because they report the outcome on another measure, each as
	// "doi (MEASURE)". A study from counts is labelled RR, its default measure,
	// even when the pool is an OR that it could have joined.
	LeftOut []string `json:"left_out,omitempty"`
}
