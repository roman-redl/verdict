package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOutcomeMatch(t *testing.T) {
	for in, want := range map[string]OutcomeMatch{
		string(OutcomeSame): OutcomeSame, "Related": OutcomeRelated, "DIFFERENT": OutcomeDifferent,
	} {
		got, err := ParseOutcomeMatch(" " + in + " ")
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	for _, in := range []string{"", "yes", "similar"} {
		_, err := ParseOutcomeMatch(in)
		require.ErrorContains(t, err, "unknown outcome match", in)
	}
}

func TestParseTimepointMatch(t *testing.T) {
	for in, want := range map[string]TimepointMatch{
		"yes": TimepointYes, "NO": TimepointNo, "UNCLEAR": TimepointUnclear,
	} {
		got, err := ParseTimepointMatch(" " + in + " ")
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	for _, in := range []string{"", "similar", "maybe"} {
		_, err := ParseTimepointMatch(in)
		require.ErrorContains(t, err, "unknown timepoint match", in)
	}
}

// "Unknown" is not "different": a missing label never excludes an effect.
func TestMatchLabelsOfAnOldEffect(t *testing.T) {
	var ef Effect

	assert.True(t, ef.OutcomeMatch.IsSame(), "a missing outcome match reads as the same outcome")
	assert.False(t, ef.TimepointMatch.IsOff(), "a missing time point match is unclear, not no")

	assert.True(t, OutcomeSame.IsSame())
	assert.False(t, OutcomeRelated.IsSame())
	assert.False(t, OutcomeDifferent.IsSame())
	assert.True(t, TimepointNo.IsOff())
	assert.False(t, TimepointYes.IsOff())
	assert.False(t, TimepointUnclear.IsOff())
}

// An effect, a critique and a pooled estimate written before the matching
// fields existed load, and carry no value for them.
func TestEffectWithoutTheMatchingFieldsLoads(t *testing.T) {
	old := `{"effect":{"outcome":"mortality","timepoint":"12 months",` +
		`"intervention":{"events":10,"total":100},"control":{"events":20,"total":100}}}`

	var c Critique

	require.NoError(t, json.Unmarshal([]byte(old), &c))
	require.NotNil(t, c.Effect)
	assert.Equal(t, "mortality", c.Effect.Outcome)
	assert.Empty(t, c.Effect.OutcomeMatch)
	assert.Empty(t, c.Effect.TimepointMatch)
	assert.Nil(t, c.Effect.TimepointWeeks)
	assert.False(t, c.Effect.Adjusted)
	assert.Nil(t, c.Effect.Cluster)

	b, err := json.Marshal(c.Effect)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "outcome_match", "omitted when empty: old artifacts round-trip unchanged")
	assert.NotContains(t, string(b), "cluster")
}

func TestEffectMatchingFieldsRoundTrip(t *testing.T) {
	raw := `{"outcome":"mortality","outcome_match":"related","timepoint_weeks":52,"timepoint_match":"no",` +
		`"adjusted":true,"measure":"HR","value":0.7,"ci_low":0.5,"ci_high":0.9,` +
		`"cluster":{"unit":"village","clusters":12,"mean_size":30,"icc":0.03,"adjusted_for_clustering":true}}`

	var ef Effect

	require.NoError(t, json.Unmarshal([]byte(raw), &ef))
	assert.Equal(t, OutcomeRelated, ef.OutcomeMatch)
	assert.Equal(t, TimepointNo, ef.TimepointMatch)
	require.NotNil(t, ef.TimepointWeeks)
	assert.InDelta(t, 52, *ef.TimepointWeeks, 1e-9)
	assert.True(t, ef.Adjusted)
	require.NotNil(t, ef.Cluster)
	assert.Equal(t, "village", ef.Cluster.Unit)
	assert.InDelta(t, 12, *ef.Cluster.Clusters, 1e-9)
	assert.InDelta(t, 30, *ef.Cluster.MeanSize, 1e-9)
	assert.InDelta(t, 0.03, *ef.Cluster.ICC, 1e-9)
	assert.True(t, ef.Cluster.AdjustedForClustering)

	b, err := json.Marshal(&ef)
	require.NoError(t, err)
	assert.JSONEq(t, raw, string(b))
}

func TestStudyDesignIsRandomized(t *testing.T) {
	for _, d := range StudyDesigns() {
		want := d == DesignRCT || d == DesignClusterRCT || d == DesignCrossoverRCT
		assert.Equal(t, want, d.IsRandomized(), string(d))
	}

	assert.False(t, StudyDesign("").IsRandomized(), "an unknown design is not randomized")
}
