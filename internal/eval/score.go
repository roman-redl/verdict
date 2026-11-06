package eval

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

var errNoReport = errors.New("the run has no report")

// relevanceDirect is the matrix relevance of a study that tests the claim
// itself; only such studies vote on the outcome.
const relevanceDirect = "direct"

// resultDirection is the critique's result_direction of a matrix row.
type resultDirection string

const (
	dirPositive resultDirection = "positive" // supports the claim
	dirNull     resultDirection = "null"     // no effect found
	dirNegative resultDirection = "negative" // the opposite effect
	dirMixed    resultDirection = "mixed"
	dirUnclear  resultDirection = "unclear"
)

// directionOrder is the order directions appear in a tally.
var directionOrder = []resultDirection{dirPositive, dirNull, dirNegative, dirMixed, dirUnclear}

// parseResultDirection normalizes a matrix direction. An empty or unknown
// value counts as unclear, the same way the pipeline's own tally treats it.
func parseResultDirection(s string) resultDirection {
	d := resultDirection(strings.ToLower(strings.TrimSpace(s)))

	switch d {
	case dirPositive, dirNull, dirNegative, dirMixed, dirUnclear:
		return d
	}

	return dirUnclear
}

// ExpectedOutcome maps the expert answer onto the claim: an answer equal to
// works_if confirms it, the other direction is the opposite effect, "no
// difference" is no effect, and the two "cannot say" answers are not proven.
func ExpectedOutcome(worksIf Change, a Answer) Outcome {
	switch a {
	case AnswerHigher:
		return confirmed(worksIf == ChangeHigher)
	case AnswerLower:
		return confirmed(worksIf == ChangeLower)
	case AnswerNoDifference:
		return OutcomeNoEffect
	case AnswerUncertain, AnswerInsufficient:
		return OutcomeNotProven
	}

	return OutcomeNotProven
}

func confirmed(ok bool) Outcome {
	if ok {
		return OutcomeWorks
	}

	return OutcomeOpposite
}

// tally counts the direct studies of an evidence matrix by direction.
type tally struct {
	direct int
	by     map[resultDirection]int
}

func tallyDirect(rows []model.MatrixRow) tally {
	t := tally{by: map[resultDirection]int{}}

	for i := range rows {
		if strings.ToLower(strings.TrimSpace(rows[i].Relevance)) != relevanceDirect {
			continue
		}

		t.direct++
		t.by[parseResultDirection(rows[i].Direction)]++
	}

	return t
}

// String is the tally as recorded in the results, e.g. "direct 2: positive 1,
// null 1".
func (t tally) String() string {
	if t.direct == 0 {
		return "direct 0"
	}

	var parts []string

	for _, d := range directionOrder {
		if n := t.by[d]; n > 0 {
			parts = append(parts, string(d)+" "+strconv.Itoa(n))
		}
	}

	return fmt.Sprintf("direct %d: %s", t.direct, strings.Join(parts, ", "))
}

// majority is the outcome of the direction with strictly more direct studies
// than every other direction. A tie, a majority of mixed or unclear studies
// and a matrix without direct studies are not proven.
func (t tally) majority() Outcome {
	best, bestN, tie := dirUnclear, 0, false

	for _, d := range directionOrder {
		switch n := t.by[d]; {
		case n > bestN:
			best, bestN, tie = d, n, false
		case n == bestN && n > 0:
			tie = true
		}
	}

	if bestN == 0 || tie {
		return OutcomeNotProven
	}

	switch best {
	case dirPositive:
		return OutcomeWorks
	case dirNull:
		return OutcomeNoEffect
	case dirNegative:
		return OutcomeOpposite
	case dirMixed, dirUnclear:
		return OutcomeNotProven
	}

	return OutcomeNotProven
}

// Judgement is verdict's answer reduced to the terms of the benchmark.
type Judgement struct {
	Outcome Outcome
	Level   Level
	// Tally is the direct-study count by direction, e.g. "direct 2: positive 1, null 1".
	Tally string
	// Consensus is the report's consensus status as parsed.
	Consensus ConsensusStatus
	// Effect is the report's effect direction; empty for a report written
	// before the field existed.
	Effect model.EffectDirection
}

// effectOutcome maps the report's own statement of the effect direction onto
// the benchmark's outcomes.
func effectOutcome(d model.EffectDirection) Outcome {
	switch d {
	case model.EffectSupports:
		return OutcomeWorks
	case model.EffectNoEffect:
		return OutcomeNoEffect
	case model.EffectOpposite:
		return OutcomeOpposite
	case model.EffectUnclear:
		return OutcomeNotProven
	}

	return OutcomeNotProven
}

// Judge derives verdict's outcome from a finished report. An "insufficient"
// verdict is not proven. Otherwise the report's effect direction decides
// (supports = works, no_effect, opposite, unclear = not proven). A report
// without one (written before the field existed) falls back to the old rule:
// a consensus status that says the direction cannot be stated
// (insufficient_data) or the studies disagree (contested, no_consensus) is
// not proven, and otherwise (broad_agreement, emerging, or a status that is
// empty or unknown) the majority direction of the direct studies decides.
func Judge(rep *model.Report) (Judgement, error) {
	if rep == nil {
		return Judgement{}, errNoReport
	}

	level, err := ParseLevel(rep.Verdict)
	if err != nil {
		return Judgement{}, err
	}

	t := tallyDirect(rep.Matrix)
	cs := ParseConsensusStatus(rep.ConsensusStatus)
	j := Judgement{Outcome: OutcomeNotProven, Level: level, Tally: t.String(), Consensus: cs}

	if rep.EffectDirection != "" {
		if j.Effect, err = model.ParseEffectDirection(string(rep.EffectDirection)); err != nil {
			return Judgement{}, fmt.Errorf("report: %w", err)
		}
	}

	if level == LevelInsufficient {
		return j, nil
	}

	if j.Effect != "" {
		j.Outcome = effectOutcome(j.Effect)

		return j, nil
	}

	switch cs {
	case ConsensusInsufficientData, ConsensusContested, ConsensusNoConsensus:
		// verdict itself says the direction cannot be stated.
	case ConsensusBroadAgreement, ConsensusEmerging, ConsensusUnknown:
		j.Outcome = t.majority()
	}

	return j, nil
}

// ExpectedLevel is the verdict level the expert certainty corresponds to;
// false when the review gives no certainty.
func ExpectedLevel(c Certainty) (Level, bool) {
	switch c {
	case CertaintyHigh:
		return LevelStrong, true
	case CertaintyModerate:
		return LevelModerate, true
	case CertaintyLow:
		return LevelWeak, true
	case CertaintyVeryLow:
		return LevelInsufficient, true
	case CertaintyNone:
		return "", false
	}

	return "", false
}

// Strength compares verdict's level with the expert certainty.
type Strength struct {
	// Scored is false when the review gives no certainty.
	Scored bool
	// Exact is a level equal to the expected one; Within1 allows one step
	// of the ordinal scale strong > moderate > weak > insufficient.
	Exact, Within1 bool
}

// ScoreStrength compares the verdict level with the expert certainty.
func ScoreStrength(c Certainty, got Level) Strength {
	want, ok := ExpectedLevel(c)
	if !ok {
		return Strength{}
	}

	diff := want.rank() - got.rank()

	return Strength{Scored: true, Exact: diff == 0, Within1: diff >= -1 && diff <= 1}
}
