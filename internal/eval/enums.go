// Package eval measures verdict's judgment against the expert conclusions of
// Cochrane reviews (the MedEvidence benchmark): every benchmark question runs
// through the normal pipeline on exactly the studies the review used, and the
// outcome is compared with the expert answer. The package holds the input set,
// the scoring rules, the run loop and the summary; it reaches the pipeline
// through the Runner port.
package eval

import (
	"fmt"
	"strings"
)

// Change is the direction of the effect the claim of a question asserts: the
// claim holds when the expert answer is the same word.
type Change string

const (
	// ChangeHigher means the claim is true if the measured value is higher.
	ChangeHigher Change = "higher"
	// ChangeLower means the claim is true if the measured value is lower.
	ChangeLower Change = "lower"
)

// ParseChange parses the works_if field of the input set.
func ParseChange(s string) (Change, error) {
	c := Change(strings.ToLower(strings.TrimSpace(s)))

	switch c {
	case ChangeHigher, ChangeLower:
		return c, nil
	}

	return "", fmt.Errorf("unknown works_if %q (want higher | lower)", s)
}

// Answer is the expert conclusion of the review.
type Answer string

const (
	AnswerHigher       Answer = "higher"
	AnswerLower        Answer = "lower"
	AnswerNoDifference Answer = "no difference"
	AnswerUncertain    Answer = "uncertain effect"
	AnswerInsufficient Answer = "insufficient data"
)

// ParseAnswer parses the answer field of the input set.
func ParseAnswer(s string) (Answer, error) {
	a := Answer(strings.ToLower(strings.TrimSpace(s)))

	switch a {
	case AnswerHigher, AnswerLower, AnswerNoDifference, AnswerUncertain, AnswerInsufficient:
		return a, nil
	}

	return "", fmt.Errorf("unknown answer %q (want higher | lower | no difference | uncertain effect | "+
		"insufficient data)", s)
}

// Certainty is the certainty of the evidence the review states for its answer.
type Certainty string

const (
	// CertaintyNone means the review gives no certainty; strength is not scored.
	CertaintyNone     Certainty = ""
	CertaintyHigh     Certainty = "high"
	CertaintyModerate Certainty = "moderate"
	CertaintyLow      Certainty = "low"
	CertaintyVeryLow  Certainty = "very low"
)

// ParseCertainty parses the certainty field of the input set; the empty
// string is valid and means "none given".
func ParseCertainty(s string) (Certainty, error) {
	c := Certainty(strings.ToLower(strings.TrimSpace(s)))

	switch c {
	case CertaintyNone, CertaintyHigh, CertaintyModerate, CertaintyLow, CertaintyVeryLow:
		return c, nil
	}

	return "", fmt.Errorf("unknown certainty %q (want high | moderate | low | very low | empty)", s)
}

// Level is the strength of evidence in verdict's report (Report.Verdict).
type Level string

const (
	LevelStrong       Level = "strong"
	LevelModerate     Level = "moderate"
	LevelWeak         Level = "weak"
	LevelInsufficient Level = "insufficient"
)

// ParseLevel parses a report verdict.
func ParseLevel(s string) (Level, error) {
	l := Level(strings.ToLower(strings.TrimSpace(s)))

	switch l {
	case LevelStrong, LevelModerate, LevelWeak, LevelInsufficient:
		return l, nil
	}

	return "", fmt.Errorf("unknown verdict level %q (want strong | moderate | weak | insufficient)", s)
}

// rank orders the levels: strong > moderate > weak > insufficient.
func (l Level) rank() int {
	switch l {
	case LevelStrong:
		return 3
	case LevelModerate:
		return 2
	case LevelWeak:
		return 1
	case LevelInsufficient:
		return 0
	}

	return -1
}

// ConsensusStatus is verdict's own statement about the direction of the
// evidence (Report.ConsensusStatus).
type ConsensusStatus string

const (
	ConsensusBroadAgreement   ConsensusStatus = "broad_agreement"
	ConsensusEmerging         ConsensusStatus = "emerging"
	ConsensusContested        ConsensusStatus = "contested"
	ConsensusNoConsensus      ConsensusStatus = "no_consensus"
	ConsensusInsufficientData ConsensusStatus = "insufficient_data"
	// ConsensusUnknown collects an empty or unrecognized status, e.g. from a
	// report written before the field existed or a model that drifted from
	// the prompt's values.
	ConsensusUnknown ConsensusStatus = "unknown"
)

// ParseConsensusStatus normalizes a report's consensus status; anything
// outside the prompt's values becomes ConsensusUnknown.
func ParseConsensusStatus(s string) ConsensusStatus {
	c := ConsensusStatus(strings.ToLower(strings.TrimSpace(s)))

	switch c {
	case ConsensusBroadAgreement, ConsensusEmerging, ConsensusContested, ConsensusNoConsensus,
		ConsensusInsufficientData:
		return c
	case ConsensusUnknown:
		return ConsensusUnknown
	}

	return ConsensusUnknown
}

// Outcome is what the evidence says about the claim, in the terms both the
// expert answer and verdict's report are reduced to.
type Outcome string

const (
	// OutcomeWorks means the evidence supports the claim.
	OutcomeWorks Outcome = "works"
	// OutcomeNoEffect means the studies found no effect.
	OutcomeNoEffect Outcome = "no_effect"
	// OutcomeOpposite means the effect goes against the claim.
	OutcomeOpposite Outcome = "opposite"
	// OutcomeNotProven means the evidence is too thin or too mixed to say.
	OutcomeNotProven Outcome = "not_proven"
)

// outcomes lists every outcome in the order the summary shows them.
var outcomes = []Outcome{OutcomeWorks, OutcomeNoEffect, OutcomeOpposite, OutcomeNotProven}

// ParseOutcome parses an outcome name.
func ParseOutcome(s string) (Outcome, error) {
	o := Outcome(strings.ToLower(strings.TrimSpace(s)))

	switch o {
	case OutcomeWorks, OutcomeNoEffect, OutcomeOpposite, OutcomeNotProven:
		return o, nil
	}

	return "", fmt.Errorf("unknown outcome %q (want works | no_effect | opposite | not_proven)", s)
}
