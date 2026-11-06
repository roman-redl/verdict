package eval

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// Matrix directions and tallies shared by the tests.
const (
	pos = "positive"
	nul = "null"
	neg = "negative"
	mix = "mixed"

	tallyNone        = "direct 0"
	tallyOnePositive = "direct 1: positive 1"
)

func TestExpectedOutcome(t *testing.T) {
	for _, tc := range []struct {
		name    string
		worksIf Change
		answer  Answer
		want    Outcome
	}{
		{"higher confirms a higher claim", ChangeHigher, AnswerHigher, OutcomeWorks},
		{"lower confirms a lower claim", ChangeLower, AnswerLower, OutcomeWorks},
		{"lower against a higher claim", ChangeHigher, AnswerLower, OutcomeOpposite},
		{"higher against a lower claim", ChangeLower, AnswerHigher, OutcomeOpposite},
		{"no difference", ChangeHigher, AnswerNoDifference, OutcomeNoEffect},
		{"uncertain effect", ChangeLower, AnswerUncertain, OutcomeNotProven},
		{"insufficient data", ChangeHigher, AnswerInsufficient, OutcomeNotProven},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ExpectedOutcome(tc.worksIf, tc.answer))
		})
	}
}

func row(relevance, direction string) model.MatrixRow {
	return model.MatrixRow{Relevance: relevance, Direction: direction}
}

func directRows(directions ...string) []model.MatrixRow {
	rows := make([]model.MatrixRow, len(directions))
	for i, d := range directions {
		rows[i] = row("direct", d)
	}

	return rows
}

func TestJudge(t *testing.T) {
	for _, tc := range []struct {
		name       string
		level      Level
		directions []string
		wantOut    Outcome
		wantTally  string
	}{
		{"insufficient overrides", LevelInsufficient, []string{pos, pos}, OutcomeNotProven, "direct 2: positive 2"},
		{"positive majority", LevelModerate, []string{pos, pos, nul}, OutcomeWorks, "direct 3: positive 2, null 1"},
		{"null majority", LevelWeak, []string{nul, nul, pos}, OutcomeNoEffect, "direct 3: positive 1, null 2"},
		{"negative majority", LevelStrong, []string{neg, neg, pos}, OutcomeOpposite, "direct 3: positive 1, negative 2"},
		{"tie", LevelModerate, []string{pos, nul}, OutcomeNotProven, "direct 2: positive 1, null 1"},
		{
			"plurality beats smaller groups", LevelModerate,
			[]string{pos, pos, nul, neg},
			OutcomeWorks, "direct 4: positive 2, null 1, negative 1",
		},
		{
			"a tie behind the leader does not matter", LevelWeak,
			[]string{pos, nul, neg, neg},
			OutcomeOpposite, "direct 4: positive 1, null 1, negative 2",
		},
		{"mixed majority", LevelModerate, []string{mix, mix, pos}, OutcomeNotProven, "direct 3: positive 1, mixed 2"},
		{
			"empty direction is unclear", LevelWeak,
			[]string{"", "", pos},
			OutcomeNotProven, "direct 3: positive 1, unclear 2",
		},
		{"unknown direction is unclear", LevelWeak, []string{"sideways"}, OutcomeNotProven, "direct 1: unclear 1"},
		{"no rows", LevelWeak, nil, OutcomeNotProven, tallyNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Judge(&model.Report{Verdict: string(tc.level), Matrix: directRows(tc.directions...)})
			require.NoError(t, err)
			assert.Equal(t, tc.wantOut, got.Outcome)
			assert.Equal(t, tc.level, got.Level)
			assert.Equal(t, tc.wantTally, got.Tally)
		})
	}
}

// The consensus status gates the majority rule: the statuses that say the
// direction cannot be stated (or the studies disagree) give not_proven even
// with a clear majority of direct studies; an empty or unrecognized status
// leaves the rule alone and is reported as unknown.
func TestJudgeConsensusStatus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		level   Level
		status  string
		wantOut Outcome
		wantCS  ConsensusStatus
	}{
		{
			"broad agreement keeps the majority", LevelStrong, string(ConsensusBroadAgreement),
			OutcomeWorks, ConsensusBroadAgreement,
		},
		{"emerging keeps the majority", LevelWeak, "emerging", OutcomeWorks, ConsensusEmerging},
		{"contested is not proven", LevelModerate, "contested", OutcomeNotProven, ConsensusContested},
		{"no consensus is not proven", LevelModerate, "no_consensus", OutcomeNotProven, ConsensusNoConsensus},
		{
			"insufficient data is not proven", LevelWeak, "insufficient_data",
			OutcomeNotProven, ConsensusInsufficientData,
		},
		{
			"insufficient verdict wins over agreement", LevelInsufficient, string(ConsensusBroadAgreement),
			OutcomeNotProven, ConsensusBroadAgreement,
		},
		{"case and spaces are ignored", LevelModerate, " Contested ", OutcomeNotProven, ConsensusContested},
		{"empty status keeps the majority", LevelModerate, "", OutcomeWorks, ConsensusUnknown},
		{"unrecognized status keeps the majority", LevelModerate, "mostly_fine", OutcomeWorks, ConsensusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := &model.Report{Verdict: string(tc.level), ConsensusStatus: tc.status, Matrix: directRows(pos, pos, nul)}

			got, err := Judge(rep)
			require.NoError(t, err)
			assert.Equal(t, tc.wantOut, got.Outcome)
			assert.Equal(t, tc.wantCS, got.Consensus)
			assert.Equal(t, "direct 3: positive 2, null 1", got.Tally)
		})
	}
}

// A report with an effect direction is judged by it, whatever the direct
// studies' majority and the consensus status say; only an insufficient
// verdict overrides it.
func TestJudgeEffectDirection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		level   Level
		effect  model.EffectDirection
		status  ConsensusStatus
		wantOut Outcome
	}{
		{"supports is works", LevelModerate, model.EffectSupports, ConsensusBroadAgreement, OutcomeWorks},
		{"no effect against a positive majority", LevelModerate, model.EffectNoEffect, ConsensusEmerging, OutcomeNoEffect},
		{"opposite", LevelWeak, model.EffectOpposite, ConsensusEmerging, OutcomeOpposite},
		{"unclear is not proven", LevelStrong, model.EffectUnclear, ConsensusBroadAgreement, OutcomeNotProven},
		{"supports despite a contested status", LevelModerate, model.EffectSupports, ConsensusContested, OutcomeWorks},
		{"insufficient verdict wins", LevelInsufficient, model.EffectSupports, ConsensusEmerging, OutcomeNotProven},
		{"case and spaces are ignored", LevelModerate, " Supports ", ConsensusEmerging, OutcomeWorks},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := &model.Report{
				Verdict: string(tc.level), EffectDirection: tc.effect, ConsensusStatus: string(tc.status),
				Matrix: directRows(nul, nul, pos),
			}

			got, err := Judge(rep)
			require.NoError(t, err)
			assert.Equal(t, tc.wantOut, got.Outcome)
			assert.Equal(t, model.EffectDirection(strings.ToLower(strings.TrimSpace(string(tc.effect)))), got.Effect)
		})
	}
}

// Without an effect direction (an old artifact) the former rule applies.
func TestJudgeWithoutEffectDirectionKeepsTheMajorityRule(t *testing.T) {
	got, err := Judge(&model.Report{Verdict: string(LevelModerate), Matrix: directRows(nul, nul, pos)})
	require.NoError(t, err)
	assert.Equal(t, OutcomeNoEffect, got.Outcome)
	assert.Empty(t, got.Effect)
}

func TestJudgeRejectsUnknownEffectDirection(t *testing.T) {
	_, err := Judge(&model.Report{Verdict: string(LevelModerate), EffectDirection: "maybe"})
	require.ErrorContains(t, err, "unknown effect direction")
}

func TestParseConsensusStatus(t *testing.T) {
	for in, want := range map[string]ConsensusStatus{
		string(ConsensusBroadAgreement): ConsensusBroadAgreement,
		"Emerging":                      ConsensusEmerging,
		"Contested":                     ConsensusContested,
		"no_consensus":                  ConsensusNoConsensus,
		"INSUFFICIENT_DATA":             ConsensusInsufficientData,
		"unknown":                       ConsensusUnknown,
		"":                              ConsensusUnknown,
		"agreement":                     ConsensusUnknown,
	} {
		assert.Equal(t, want, ParseConsensusStatus(in), in)
	}
}

// Only rows of relevance "direct" vote; case and spaces do not matter.
func TestJudgeCountsOnlyDirectRows(t *testing.T) {
	ignored := []model.MatrixRow{row("indirect", neg), row("", neg), row("indirect", neg)}

	got, err := Judge(&model.Report{Verdict: string(LevelModerate), Matrix: ignored})
	require.NoError(t, err)
	assert.Equal(t, OutcomeNotProven, got.Outcome)
	assert.Equal(t, tallyNone, got.Tally)

	withDirect := []model.MatrixRow{row("indirect", neg), row(" Direct", "Positive"), row("", neg)}

	got, err = Judge(&model.Report{Verdict: " Strong ", Matrix: withDirect})
	require.NoError(t, err)
	assert.Equal(t, OutcomeWorks, got.Outcome)
	assert.Equal(t, LevelStrong, got.Level)
	assert.Equal(t, tallyOnePositive, got.Tally)
}

func TestJudgeRejectsBadReports(t *testing.T) {
	_, err := Judge(nil)
	require.ErrorIs(t, err, errNoReport)

	_, err = Judge(&model.Report{Verdict: "excellent"})
	require.ErrorContains(t, err, "unknown verdict level")

	_, err = Judge(&model.Report{})
	require.Error(t, err)
}

func TestScoreStrength(t *testing.T) {
	exact := Strength{Scored: true, Exact: true, Within1: true}
	oneOff := Strength{Scored: true, Within1: true}
	far := Strength{Scored: true}

	for _, tc := range []struct {
		name      string
		certainty Certainty
		got       Level
		want      Strength
	}{
		{"high and strong", CertaintyHigh, LevelStrong, exact},
		{"high and moderate", CertaintyHigh, LevelModerate, oneOff},
		{"high and weak", CertaintyHigh, LevelWeak, far},
		{"moderate and moderate", CertaintyModerate, LevelModerate, exact},
		{"moderate and strong", CertaintyModerate, LevelStrong, oneOff},
		{"low and weak", CertaintyLow, LevelWeak, exact},
		{"low and insufficient", CertaintyLow, LevelInsufficient, oneOff},
		{"low and strong", CertaintyLow, LevelStrong, far},
		{"very low and insufficient", CertaintyVeryLow, LevelInsufficient, exact},
		{"very low and weak", CertaintyVeryLow, LevelWeak, oneOff},
		{"very low and strong", CertaintyVeryLow, LevelStrong, far},
		{"no certainty is not scored", CertaintyNone, LevelStrong, Strength{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ScoreStrength(tc.certainty, tc.got))
		})
	}
}

func TestExpectedLevel(t *testing.T) {
	for c, want := range map[Certainty]Level{
		CertaintyHigh: LevelStrong, CertaintyModerate: LevelModerate,
		CertaintyLow: LevelWeak, CertaintyVeryLow: LevelInsufficient,
	} {
		got, ok := ExpectedLevel(c)
		assert.True(t, ok, c)
		assert.Equal(t, want, got, c)
	}

	_, ok := ExpectedLevel(CertaintyNone)
	assert.False(t, ok)
}

func TestParsers(t *testing.T) {
	c, err := ParseChange(" Higher ")
	require.NoError(t, err)
	assert.Equal(t, ChangeHigher, c)

	_, err = ParseChange("same")
	require.ErrorContains(t, err, "works_if")

	a, err := ParseAnswer("Uncertain Effect")
	require.NoError(t, err)
	assert.Equal(t, AnswerUncertain, a)

	_, err = ParseAnswer("maybe")
	require.ErrorContains(t, err, "answer")

	cert, err := ParseCertainty("")
	require.NoError(t, err)
	assert.Equal(t, CertaintyNone, cert)

	cert, err = ParseCertainty("Very Low")
	require.NoError(t, err)
	assert.Equal(t, CertaintyVeryLow, cert)

	_, err = ParseCertainty("certain")
	require.ErrorContains(t, err, "certainty")

	l, err := ParseLevel("Weak")
	require.NoError(t, err)
	assert.Equal(t, LevelWeak, l)

	_, err = ParseLevel("")
	require.Error(t, err)

	o, err := ParseOutcome("no_effect")
	require.NoError(t, err)
	assert.Equal(t, OutcomeNoEffect, o)

	_, err = ParseOutcome("unknown")
	require.ErrorContains(t, err, "outcome")
}

func TestLevelRankOrder(t *testing.T) {
	assert.Greater(t, LevelStrong.rank(), LevelModerate.rank())
	assert.Greater(t, LevelModerate.rank(), LevelWeak.rank())
	assert.Greater(t, LevelWeak.rank(), LevelInsufficient.rank())
	assert.Equal(t, -1, Level("x").rank())
}
