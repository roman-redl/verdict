package model

import (
	"fmt"
	"strings"
)

// ImprecisionTarget is the conclusion an imprecision rating applies to:
// the change of the outcome under the intervention that the pooled estimate
// supports, or the absence of a meaningful change.
type ImprecisionTarget string

const (
	// TargetLower means the outcome's value is lower under the intervention.
	TargetLower ImprecisionTarget = "lower"
	// TargetHigher means the outcome's value is higher under the intervention.
	TargetHigher ImprecisionTarget = "higher"
	// TargetLittleOrNoDifference means little or no difference: the interval
	// includes no effect and is not too wide to say so (no difference seen).
	TargetLittleOrNoDifference ImprecisionTarget = "little_or_no_difference"
	// TargetUncertain means the evidence cannot tell: the interval includes no
	// effect and is so wide that no direction and no "no difference" can be
	// stated.
	TargetUncertain ImprecisionTarget = "uncertain"
)

// ParseImprecisionTarget parses a target as it is stored in an artifact.
func ParseImprecisionTarget(s string) (ImprecisionTarget, error) {
	t := ImprecisionTarget(strings.ToLower(strings.TrimSpace(s)))

	switch t {
	case TargetLower, TargetHigher, TargetLittleOrNoDifference, TargetUncertain:
		return t, nil
	}

	return "", fmt.Errorf("unknown imprecision target %q (want lower | higher | little_or_no_difference | uncertain)",
		s)
}

// ClaimChange is the change of the outcome's measured value that the claim of
// the question states for the intervention versus the comparator.
type ClaimChange string

const (
	// ClaimLower is a claim that the outcome's value falls ("lowers the risk").
	ClaimLower ClaimChange = "lower"
	// ClaimHigher is a claim that the outcome's value rises ("increases survival").
	ClaimHigher ClaimChange = "higher"
)

// ParseClaimChange parses the synthesis's claim_change. The empty string is an
// error too: the caller decides what a missing value means.
func ParseClaimChange(s string) (ClaimChange, error) {
	c := ClaimChange(strings.ToLower(strings.TrimSpace(s)))

	switch c {
	case ClaimLower, ClaimHigher:
		return c, nil
	}

	return "", fmt.Errorf("unknown claim change %q (want lower | higher)", s)
}

// DependsOnClaim tells whether the direction implied by the target needs the
// claim's change: only a lower or higher value is read against the claim.
func (t ImprecisionTarget) DependsOnClaim() bool {
	return t == TargetLower || t == TargetHigher
}

// DirectionFor is the effect direction relative to a claim: no effect for a
// target of little or no difference, unclear for an uncertain target, supports
// when the target is the change the claim states, opposite otherwise. A value
// that is not a target at all implies no direction: ok is false then.
func (t ImprecisionTarget) DirectionFor(c ClaimChange) (d EffectDirection, ok bool) {
	switch t {
	case TargetLittleOrNoDifference:
		return EffectNoEffect, true
	case TargetUncertain:
		return EffectUnclear, true
	case TargetLower:
		return directionOf(c == ClaimLower), true
	case TargetHigher:
		return directionOf(c == ClaimHigher), true
	}

	return "", false
}

func directionOf(supports bool) EffectDirection {
	if supports {
		return EffectSupports
	}

	return EffectOpposite
}

// Imprecision is the GRADE imprecision rating of the pooled estimate,
// computed by the pipeline from the confidence interval (and the amount of
// data) so that it does not vary between runs of the synthesis.
type Imprecision struct {
	// Levels is how many levels to rate down for imprecision: 0, 1 or 2.
	Levels int `json:"levels"`
	// Target is the conclusion the levels apply to.
	Target ImprecisionTarget `json:"target"`
	// BandLow and BandHigh bound the range of trivial effects that was used;
	// absent for a mean difference without an SD and when the interval
	// excludes no effect.
	BandLow  *float64 `json:"band_low,omitempty"`
	BandHigh *float64 `json:"band_high,omitempty"`
	// Reason is one sentence with the numbers behind the levels.
	Reason string `json:"reason"`
	// SizeChecked is false when the information-size rule was needed but the
	// number of participants is unknown.
	SizeChecked bool `json:"size_checked"`
}
