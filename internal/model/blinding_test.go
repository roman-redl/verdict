package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBlinding(t *testing.T) {
	for in, want := range map[string]Blinding{
		"double_blind": BlindingDouble,
		"Double-Blind": BlindingDouble,
		"single blind": BlindingSingle,
		"OPEN":         BlindingOpen,
		"unclear":      BlindingUnclear,
		// the assessor-only value, with and without the formatting noise
		"assessor_blind":    BlindingAssessorOnly,
		"Assessor-Blind":    BlindingAssessorOnly,
		"assessor blind":    BlindingAssessorOnly,
		" Assessor_Blind  ": BlindingAssessorOnly,
		// aliases
		"open_label":       BlindingOpen,
		"Open-label":       BlindingOpen,
		"unblinded":        BlindingOpen,
		"non_blinded":      BlindingOpen,
		"triple_blind":     BlindingDouble,
		"Quadruple-blind":  BlindingDouble,
		"assessor_blinded": BlindingAssessorOnly,
		"assessor-blinded": BlindingAssessorOnly,
	} {
		got, err := ParseBlinding(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	for _, bad := range []string{"", "blinded", "partly masked", "label", "assessor"} {
		_, err := ParseBlinding(bad)
		require.Error(t, err, bad)
	}
}

// An artifact written before assessor_blind existed keeps its values; the new
// value round-trips, and the evidence group's counter is left out when zero.
func TestBlindingValuesInArtifacts(t *testing.T) {
	for _, b := range []Blinding{BlindingDouble, BlindingSingle, BlindingOpen, BlindingUnclear, BlindingAssessorOnly} {
		var c Critique

		require.NoError(t, json.Unmarshal([]byte(`{"doi":"10.1/x","blinding":"`+string(b)+`"}`), &c))
		assert.Equal(t, b, c.Blinding)

		parsed, err := ParseBlinding(string(c.Blinding))
		require.NoError(t, err)
		assert.Equal(t, b, parsed, "a stored value parses back to itself")
	}

	var old EvidenceGroup

	require.NoError(t, json.Unmarshal([]byte(`{"studies":3,"double_blind":2,"single_blind":1}`), &old))
	assert.Equal(t, EvidenceGroup{Studies: 3, DoubleBlind: 2, SingleBlind: 1}, old)

	out, err := json.Marshal(old)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "assessor_blind")

	out, err = json.Marshal(EvidenceGroup{Studies: 1, AssessorBlind: 1})
	require.NoError(t, err)
	assert.Contains(t, string(out), `"assessor_blind":1`)
}

// The new critique fields load when present; a critique written before them
// loads with the fields unset (unknown), and marshals back without them.
func TestBlindingFieldsJSON(t *testing.T) {
	var c Critique

	require.NoError(t, json.Unmarshal([]byte(`{"doi":"10.1/x","blinding":"open","effect":{
		"outcome":"pain","outcome_objective":false,"outcome_self_reported":true,"events_total":212,
		"measure":"RR","value":0.8,"ci_low":0.6,"ci_high":1.1}}`), &c))

	assert.Equal(t, BlindingOpen, c.Blinding)
	require.NotNil(t, c.Effect.OutcomeObjective)
	assert.False(t, *c.Effect.OutcomeObjective)
	require.NotNil(t, c.Effect.OutcomeSelfReported)
	assert.True(t, *c.Effect.OutcomeSelfReported)
	require.NotNil(t, c.Effect.EventsTotal)
	assert.InDelta(t, 212, *c.Effect.EventsTotal, 0)

	var old Critique

	require.NoError(t, json.Unmarshal([]byte(`{"doi":"10.1/x","effect":{"outcome":"death","measure":"RR"}}`), &old))
	assert.Empty(t, old.Blinding)
	assert.Nil(t, old.Effect.OutcomeObjective)
	assert.Nil(t, old.Effect.OutcomeSelfReported)
	assert.Nil(t, old.Effect.EventsTotal)

	out, err := json.Marshal(old)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "blinding")
	assert.NotContains(t, string(out), "outcome_objective")
	assert.NotContains(t, string(out), "outcome_self_reported")
	assert.NotContains(t, string(out), "events_total")

	pe, err := json.Marshal(PooledEstimate{Method: PoolSingleStudy, Measure: MeasureRR})
	require.NoError(t, err)
	assert.NotContains(t, string(pe), "events")
}
