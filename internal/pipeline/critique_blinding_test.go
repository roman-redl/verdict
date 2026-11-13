package pipeline

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

func TestNormalizeBlinding(t *testing.T) {
	for _, tt := range []struct {
		name     string
		raw      model.Blinding
		want     model.Blinding
		wantWarn bool
	}{
		{name: "valid", raw: model.BlindingDouble, want: model.BlindingDouble},
		{name: "case and hyphen", raw: "Single-Blind", want: model.BlindingSingle},
		{name: "open-label is open", raw: "open-label", want: model.BlindingOpen},
		{name: "unblinded is open", raw: "Unblinded", want: model.BlindingOpen},
		{name: "triple blind is double blind", raw: "triple_blind", want: model.BlindingDouble},
		{name: "assessor-blinded is single blind", raw: "assessor-blinded", want: model.BlindingSingle},
		{name: "unclear", raw: model.BlindingUnclear, want: model.BlindingUnclear},
		{name: "missing stays missing", raw: "", want: ""},
		{name: "unknown becomes unclear", raw: "partly masked", want: model.BlindingUnclear, wantWarn: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer

			log := slog.New(slog.NewTextHandler(&logs, nil))

			assert.Equal(t, tt.want, normalizeBlinding(tt.raw, doiA, log))
			assert.Equal(t, tt.wantWarn, strings.Contains(logs.String(), "level=WARN"), logs.String())
		})
	}
}

// The critic's new fields survive the batch parsing; an invalid blinding
// becomes unclear and an unusable events total is dropped, each with a log
// warning, never an error; a critique without the fields stays without them.
func TestCritiqueParsesTheBlindingFields(t *testing.T) {
	batch := `{"critiques":[` +
		`{"doi":"10.1/a","blinding":"Double-Blind","effect":{"outcome_objective":true,"events_total":212,` +
		`"measure":"HR","value":0.8,"ci_low":0.6,"ci_high":1.0}},` +
		`{"doi":"10.1/b","blinding":"partly masked","effect":{"outcome_objective":false,"events_total":-4,` +
		`"measure":"HR","value":0.8,"ci_low":0.6,"ci_high":1.0}},` +
		`{"doi":"10.1/c","effect":{"measure":"HR","value":0.8,"ci_low":0.6,"ci_high":1.0}}]}`
	s := &scriptCompleter{replies: []string{batch}}

	var logs bytes.Buffer

	log := slog.New(slog.NewTextHandler(&logs, nil))

	got, _, err := critiqueBatch(context.Background(), s,
		[]model.EnrichedPaper{ep(doiA), ep(doiB), ep("10.1/c")}, "q", log)
	require.NoError(t, err)

	a := got[doiA]
	assert.Equal(t, model.BlindingDouble, a.Blinding)
	require.NotNil(t, a.Effect.OutcomeObjective)
	assert.True(t, *a.Effect.OutcomeObjective)
	require.NotNil(t, a.Effect.EventsTotal)
	assert.InDelta(t, 212, *a.Effect.EventsTotal, 0)

	b := got[doiB]
	assert.Equal(t, model.BlindingUnclear, b.Blinding)
	require.NotNil(t, b.Effect.OutcomeObjective)
	assert.False(t, *b.Effect.OutcomeObjective)
	assert.Nil(t, b.Effect.EventsTotal)

	c := got["10.1/c"]
	assert.Empty(t, c.Blinding)
	assert.Nil(t, c.Effect.OutcomeObjective)
	assert.Nil(t, c.Effect.EventsTotal)

	assert.Equal(t, 2, strings.Count(logs.String(), "level=WARN"), logs.String())
	assert.Contains(t, logs.String(), "partly masked")
	assert.Contains(t, logs.String(), "unusable events total")
}

// The single-study fallback reads the same fields.
func TestCritiquePaperParsesTheBlindingFields(t *testing.T) {
	s := &scriptCompleter{replies: []string{
		`{"doi":"10.1/a","blinding":"open","effect":{"outcome_objective":false,"events_total":90,` +
			`"measure":"RR","value":0.7,"ci_low":0.5,"ci_high":0.98}}`,
	}}

	got, _, err := critiquePaper(context.Background(), s, ep(doiA), "q", quietLog())
	require.NoError(t, err)

	assert.Equal(t, model.BlindingOpen, got.Blinding)
	require.NotNil(t, got.Effect.OutcomeObjective)
	assert.False(t, *got.Effect.OutcomeObjective)
	require.NotNil(t, got.Effect.EventsTotal)
	assert.InDelta(t, 90, *got.Effect.EventsTotal, 0)
}

// Both critique prompts ask for blinding, the objectivity of the outcome and
// the total of events, and name every blinding value.
func TestCritiquePromptsAskForTheBlindingFields(t *testing.T) {
	for name, prompt := range map[string]string{
		"one study per call": critiqueSystemPrompt, "studies in batches": batchCritiqueSystemPrompt,
	} {
		assert.Contains(t, prompt, "blinding (only for a randomized trial; omit for any other design)", name)
		assert.Contains(t, prompt, "outcome_objective (true when the outcome is objective", name)
		assert.Contains(t, prompt, "events_total (number:", name)
		assert.Contains(t, prompt, "never derive it from percentages", name)

		for _, b := range []model.Blinding{
			model.BlindingDouble, model.BlindingSingle, model.BlindingOpen, model.BlindingUnclear,
		} {
			assert.Contains(t, prompt, `"`+string(b)+`"`, name+": "+string(b))
		}
	}
}
