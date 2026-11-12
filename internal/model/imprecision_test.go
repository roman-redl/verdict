package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseImprecisionTarget(t *testing.T) {
	for in, want := range map[string]ImprecisionTarget{
		"lower": TargetLower, "HIGHER": TargetHigher, "Little_Or_No_Difference": TargetLittleOrNoDifference,
		"Uncertain": TargetUncertain,
	} {
		got, err := ParseImprecisionTarget(" " + in + " ")
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	for _, in := range []string{"", "none", "no effect", "undetermined"} {
		_, err := ParseImprecisionTarget(in)
		require.ErrorContains(t, err, "unknown imprecision target", in)
	}
}

func TestParseClaimChange(t *testing.T) {
	for in, want := range map[string]ClaimChange{"lower": ClaimLower, "Higher": ClaimHigher} {
		got, err := ParseClaimChange(" " + in + " ")
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	for _, in := range []string{"", "same", "little_or_no_difference"} {
		_, err := ParseClaimChange(in)
		require.ErrorContains(t, err, "unknown claim change", in)
	}
}

func TestDirectionFor(t *testing.T) {
	for _, tc := range []struct {
		target ImprecisionTarget
		claim  ClaimChange
		want   EffectDirection
	}{
		{TargetLower, ClaimLower, EffectSupports},
		{TargetHigher, ClaimHigher, EffectSupports},
		{TargetLower, ClaimHigher, EffectOpposite},
		{TargetHigher, ClaimLower, EffectOpposite},
		{TargetLittleOrNoDifference, ClaimLower, EffectNoEffect},
		{TargetLittleOrNoDifference, ClaimHigher, EffectNoEffect},
		{TargetUncertain, ClaimLower, EffectUnclear},
		{TargetUncertain, ClaimHigher, EffectUnclear},
	} {
		got, ok := tc.target.DirectionFor(tc.claim)
		require.True(t, ok)
		assert.Equal(t, tc.want, got, "%s vs a claim of %s", tc.target, tc.claim)
	}

	// no direction is implied by an unknown target
	for _, claim := range []ClaimChange{ClaimLower, ClaimHigher} {
		_, ok := ImprecisionTarget("odd").DirectionFor(claim)
		assert.False(t, ok, "a claim of %s", claim)
	}
}

func TestDependsOnClaim(t *testing.T) {
	for target, want := range map[ImprecisionTarget]bool{
		TargetLower: true, TargetHigher: true, TargetLittleOrNoDifference: false, TargetUncertain: false,
	} {
		assert.Equal(t, want, target.DependsOnClaim(), target)
	}
}
