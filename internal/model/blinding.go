package model

import (
	"fmt"
	"strings"
)

// Blinding is how a randomized trial masked participants and outcome
// assessors, as the critic reads it from the text. An empty value is a study
// the critic gave no blinding for: a non-randomized design, an artifact
// written before the field existed, or an omitted field; it reads as unknown,
// like BlindingUnclear.
type Blinding string

const (
	// BlindingDouble is a trial masked for participants and for the people who
	// treat them or assess the outcome.
	BlindingDouble Blinding = "double_blind"
	// BlindingSingle is a trial masked on one side only, usually the outcome
	// assessor.
	BlindingSingle Blinding = "single_blind"
	// BlindingOpen is an open-label trial: nobody was masked.
	BlindingOpen Blinding = "open"
	// BlindingUnclear means the text does not say.
	BlindingUnclear Blinding = "unclear"
)

// blindingAliases are the words a critic uses for a value in the prompt's own
// terms (after the spaces and hyphens are read as underscores). Triple and
// quadruple masking add people to the double-blind case; an assessor-blinded
// trial is masked on one side.
var blindingAliases = map[string]Blinding{
	"open_label":       BlindingOpen,
	"unblinded":        BlindingOpen,
	"non_blinded":      BlindingOpen,
	"triple_blind":     BlindingDouble,
	"quadruple_blind":  BlindingDouble,
	"assessor_blinded": BlindingSingle,
	"assessor_blind":   BlindingSingle,
}

// ParseBlinding parses a blinding as the critic writes it, ignoring case and
// reading spaces and hyphens as underscores ("double-blind" is double_blind),
// and accepting the aliases of blindingAliases ("open-label" is open). The
// empty string is an error too: the caller decides what a missing value means.
func ParseBlinding(s string) (Blinding, error) {
	norm := strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(s)))
	b := Blinding(norm)

	switch b {
	case BlindingDouble, BlindingSingle, BlindingOpen, BlindingUnclear:
		return b, nil
	}

	if alias, ok := blindingAliases[norm]; ok {
		return alias, nil
	}

	return "", fmt.Errorf("unknown blinding %q (want double_blind | single_blind | open | unclear)", s)
}

// BlindingBias is the risk-of-bias rating for lack of blinding, computed by
// the pipeline from the pooled studies (not by the synthesis): most of the
// pool's weight is open-label studies, and most of it is on a subjective or
// self-reported outcome.
type BlindingBias struct {
	// Levels is how many levels to rate down for risk of bias: 1 (serious).
	Levels int `json:"levels"`
	// Reason is one sentence with the shares behind the rating.
	Reason string `json:"reason"`
}
