package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseYesNo(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want YesNo
	}{{string(Yes), Yes}, {" YES ", Yes}, {"No", No}, {string(No), No}} {
		got, err := ParseYesNo(tc.in)
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
	}

	for _, bad := range []string{"", "maybe", "true", "1", "y"} {
		_, err := ParseYesNo(bad)
		require.Error(t, err, bad)
	}

	assert.True(t, Yes.IsYes())
	assert.False(t, No.IsYes())
	assert.False(t, YesNo("").IsYes())
}

// noStudyFlags is one per-study answer with every item no.
func noStudyFlags(doi string) RiskOfBiasStudy {
	return RiskOfBiasStudy{
		DOI: doi, Attrition: No, AllocationFlaw: No, SelectiveOutcome: No, StoppedEarlyForBenefit: No,
	}
}

// allAnswered is a checklist with every item answered no, the risk of bias
// per study (one study).
func allAnswered() GradeChecklist {
	return GradeChecklist{
		RiskOfBias:      RiskOfBiasItems{PerStudy: []RiskOfBiasStudy{noStudyFlags("10.1/a")}},
		Inconsistency:   InconsistencyItems{DirectionsConflict: No, SizesDifferBeyondChance: No},
		Indirectness:    IndirectnessItems{Population: No, Intervention: No, Comparator: No, Outcome: No},
		PublicationBias: PublicationBiasItem{Suspected: No},
	}
}

// globalAnswered is the same with the four global risk-of-bias answers of an
// older reply.
func globalAnswered() GradeChecklist {
	c := allAnswered()
	c.RiskOfBias = RiskOfBiasItems{Attrition: No, AllocationFlaw: No, SelectiveOutcome: No, StoppedEarlyForBenefit: No}

	return c
}

func TestGradeChecklistNormalize(t *testing.T) {
	t.Run("answers are canonical after it", func(t *testing.T) {
		c := allAnswered()
		c.RiskOfBias.PerStudy[0].Attrition = " Yes "
		c.Indirectness.Outcome = "NO"
		c.LargeEffect = "YES"

		require.NoError(t, c.Normalize(false))
		assert.Equal(t, Yes, c.RiskOfBias.PerStudy[0].Attrition)
		assert.Equal(t, No, c.Indirectness.Outcome)
		assert.Equal(t, Yes, c.LargeEffect)
	})

	t.Run("an omitted large_effect is no", func(t *testing.T) {
		c := allAnswered()
		require.NoError(t, c.Normalize(false))
		assert.Equal(t, No, c.LargeEffect)
	})

	t.Run("an empty per_study list is an answer: no direct primary study", func(t *testing.T) {
		c := allAnswered()
		c.RiskOfBias.PerStudy = []RiskOfBiasStudy{}

		require.NoError(t, c.Normalize(false))
		assert.NotNil(t, c.RiskOfBias.PerStudy)
	})

	t.Run("per_study wins: the global answers beside it are dropped", func(t *testing.T) {
		c := allAnswered()
		c.RiskOfBias.Attrition = Yes

		require.NoError(t, c.Normalize(false))
		assert.Empty(t, c.RiskOfBias.Attrition)
	})

	t.Run("the global answers of an older reply are accepted without per_study", func(t *testing.T) {
		c := globalAnswered()
		c.RiskOfBias.Attrition = " YES "

		require.NoError(t, c.Normalize(false))
		assert.Nil(t, c.RiskOfBias.PerStudy)
		assert.Equal(t, Yes, c.RiskOfBias.Attrition)
	})

	t.Run("a per-study entry is repaired instead of rejecting the reply", func(t *testing.T) {
		for name, tc := range map[string]struct {
			entries []RiskOfBiasStudy
			want    []RiskOfBiasStudy
			repairs PerStudyRepairs
		}{
			"a missing item is no": {
				entries: []RiskOfBiasStudy{{DOI: " 10.1/b ", Attrition: Yes}},
				want: []RiskOfBiasStudy{
					{DOI: "10.1/b", Attrition: Yes, AllocationFlaw: No, SelectiveOutcome: No, StoppedEarlyForBenefit: No},
				},
				repairs: PerStudyRepairs{ItemsFilled: 3},
			},
			"an unknown word drops the entry": {
				entries: []RiskOfBiasStudy{
					noStudyFlags("10.1/a"),
					{DOI: "10.1/c", Attrition: No, AllocationFlaw: No, SelectiveOutcome: No, StoppedEarlyForBenefit: "unsure"},
				},
				want:    []RiskOfBiasStudy{noStudyFlags("10.1/a")},
				repairs: PerStudyRepairs{EntriesDropped: 1},
			},
			"an unknown word beside missing items drops the entry": {
				entries: []RiskOfBiasStudy{{DOI: "10.1/d", Attrition: "dunno"}},
				want:    []RiskOfBiasStudy{},
				repairs: PerStudyRepairs{EntriesDropped: 1},
			},
			"an empty answer list stays an answer": {
				entries: []RiskOfBiasStudy{},
				want:    []RiskOfBiasStudy{},
			},
		} {
			t.Run(name, func(t *testing.T) {
				c := allAnswered()
				c.RiskOfBias.PerStudy = tc.entries

				require.NoError(t, c.Normalize(false))
				assert.Equal(t, tc.want, c.RiskOfBias.PerStudy)
				assert.NotNil(t, c.RiskOfBias.PerStudy, "an answered list is not nil")
				assert.Equal(t, tc.repairs, c.RiskOfBias.Repairs)
			})
		}
	})

	t.Run("imprecision_without_pool is kept when the pipeline computed no imprecision", func(t *testing.T) {
		for _, n := range []int{0, 1, 2} {
			c := allAnswered()
			c.ImprecisionWithoutPool = &n

			require.NoError(t, c.Normalize(true))
			require.NotNil(t, c.ImprecisionWithoutPool)
			assert.Equal(t, n, *c.ImprecisionWithoutPool)
		}
	})

	t.Run("imprecision_without_pool is dropped when the pipeline computed the imprecision", func(t *testing.T) {
		c := allAnswered()
		c.ImprecisionWithoutPool = new(7)

		require.NoError(t, c.Normalize(false), "even a value outside 0..2 is no error then")
		assert.Nil(t, c.ImprecisionWithoutPool)
	})

	for name, tc := range map[string]struct {
		mod  func(c *GradeChecklist)
		want string
	}{
		"an unanswered item": {
			mod: func(c *GradeChecklist) { c.Indirectness.Comparator = "" }, want: "indirectness.comparator",
		},
		"an invalid answer": {
			mod: func(c *GradeChecklist) { c.Inconsistency.DirectionsConflict = "perhaps" }, want: "directions_conflict",
		},
		"an invalid large_effect": {
			mod: func(c *GradeChecklist) { c.LargeEffect = "big" }, want: "large_effect",
		},
		"neither per_study nor the global answers": {
			mod: func(c *GradeChecklist) { c.RiskOfBias = RiskOfBiasItems{} }, want: "risk_of_bias.per_study",
		},
		"a partial set of global answers": {
			mod: func(c *GradeChecklist) {
				c.RiskOfBias = RiskOfBiasItems{Attrition: Yes}
			},
			want: "risk_of_bias.allocation_flaw",
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := allAnswered()
			tc.mod(&c)

			err := c.Normalize(false)
			require.Error(t, err)
			require.ErrorContains(t, err, tc.want)
		})
	}

	for name, tc := range map[string]struct {
		answer *int
		want   string
	}{
		"a missing imprecision_without_pool": {answer: nil, want: "imprecision_without_pool: missing"},
		"a value above 2":                    {answer: new(3), want: "unknown answer 3"},
		"a negative value":                   {answer: new(-1), want: "unknown answer -1"},
	} {
		t.Run(name+" is rejected without a pooled imprecision", func(t *testing.T) {
			c := allAnswered()
			c.ImprecisionWithoutPool = tc.answer

			err := c.Normalize(true)
			require.Error(t, err)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// The checklist and the assessment round-trip through JSON, with the keys the
// prompt describes.
func TestGradeJSON(t *testing.T) {
	c := allAnswered()
	c.RiskOfBias.PerStudy[0].Attrition = Yes
	c.ImprecisionWithoutPool = new(2)

	raw, err := json.Marshal(GradeAssessment{
		Start: "strong",
		RiskOfBias: GradeDomain{
			Levels: 1, Causes: []string{CauseAttrition},
			Shares: map[string]float64{CauseAttrition: 0.64, CauseAllocationFlaw: 0},
		},
		Level: "moderate", Checklist: c,
	})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"risk_of_bias":{"levels":1,"causes":["attrition"],`+
		`"shares":{"allocation_flaw":0,"attrition":0.64}}`)
	assert.Contains(t, string(raw), `"per_study":[{"doi":"10.1/a","attrition":"yes","allocation_flaw":"no"`)
	assert.Contains(t, string(raw), `"imprecision_without_pool":2`)
	assert.NotContains(t, string(raw), `"upgrade"`, "a zero raise is omitted")

	var back GradeAssessment

	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, "moderate", back.Level)
	assert.Equal(t, c, back.Checklist)
	assert.InDelta(t, 0.64, back.RiskOfBias.Shares[CauseAttrition], 1e-12)

	// the global answers of an older reply keep their keys; per_study is null
	old, err := json.Marshal(globalAnswered())
	require.NoError(t, err)
	assert.Contains(t, string(old), `"risk_of_bias":{"per_study":null,"attrition":"no","allocation_flaw":"no"`)

	var oldBack GradeChecklist

	require.NoError(t, json.Unmarshal(old, &oldBack))
	assert.Nil(t, oldBack.RiskOfBias.PerStudy, "null is not an answer")

	// an answered empty list is kept, not omitted
	none := allAnswered()
	none.RiskOfBias.PerStudy = []RiskOfBiasStudy{}

	noneRaw, err := json.Marshal(none)
	require.NoError(t, err)
	assert.Contains(t, string(noneRaw), `"per_study":[]`)

	var noneBack GradeChecklist

	require.NoError(t, json.Unmarshal(noneRaw, &noneBack))
	assert.NotNil(t, noneBack.RiskOfBias.PerStudy)
	assert.Empty(t, noneBack.RiskOfBias.PerStudy)

	var r Report

	require.NoError(t, json.Unmarshal([]byte(`{"question":"q","verdict":"weak"}`), &r))
	assert.Nil(t, r.Grade, "a report from before the field has no grade")
}

// An answer of any JSON type decodes; Normalize then accepts what reads as yes
// or no and rejects the rest by name.
func TestYesNoUnmarshalJSON(t *testing.T) {
	const unknownAnswer = "unknown answer"

	for _, tc := range []struct {
		in      string
		want    YesNo
		wantErr string // of ParseYesNo
	}{
		{in: `"yes"`, want: Yes},
		{in: `"No"`, want: "No"},
		{in: `true`, want: Yes},
		{in: `false`, want: No},
		{in: `null`, want: "", wantErr: unknownAnswer},
		{in: `1`, want: "1", wantErr: `unknown answer "1"`},
		{in: `"possibly"`, want: "possibly", wantErr: unknownAnswer},
		{in: `{"a":1}`, want: `{"a":1}`, wantErr: unknownAnswer},
		{in: `["yes"]`, want: `["yes"]`, wantErr: unknownAnswer},
	} {
		t.Run(tc.in, func(t *testing.T) {
			var got struct {
				A YesNo `json:"a"`
			}

			require.NoError(t, json.Unmarshal([]byte(`{"a":`+tc.in+`}`), &got))
			assert.Equal(t, tc.want, got.A)

			_, err := ParseYesNo(string(got.A))
			if tc.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// The checklist decodes whatever the types are: a wrong one is remembered and
// Normalize rejects the checklist with it; a whole-number imprecision is read
// from a float or a numeric text.
func TestGradeChecklistUnmarshalJSON(t *testing.T) {
	const rest = `"inconsistency":{"directions_conflict":"no","sizes_differ_beyond_chance":"no"},` +
		`"indirectness":{"population":"no","intervention":"no","comparator":"no","outcome":"no"},` +
		`"publication_bias":{"suspected":"no"}`

	t.Run("the imprecision as a number, a float or a text", func(t *testing.T) {
		for in, want := range map[string]int{
			`0`: 0, `1`: 1, `2`: 2, `1.0`: 1, `2e0`: 2, `"1"`: 1, `" 2 "`: 2, `"0.0"`: 0, `3`: 3, `-1.0`: -1,
		} {
			var c GradeChecklist

			require.NoError(t, json.Unmarshal([]byte(`{"imprecision_without_pool":`+in+`}`), &c), in)
			require.NotNil(t, c.ImprecisionWithoutPool, in)
			assert.Equal(t, want, *c.ImprecisionWithoutPool, in)
		}
	})

	t.Run("a value that is no whole number is rejected by Normalize, not by the decode", func(t *testing.T) {
		for _, in := range []string{`1.5`, `"high"`, `[true]`, `{}`, `[1]`, `1e30`, `"NaN"`, `"Inf"`} {
			c := allAnswered()

			require.NoError(t, json.Unmarshal([]byte(`{"imprecision_without_pool":`+in+`}`), &c), in)
			assert.Nil(t, c.ImprecisionWithoutPool, in)

			c = decodeChecklist(t, `{"risk_of_bias":{"per_study":[]},`+rest+`,"imprecision_without_pool":`+in+`}`)

			err := c.Normalize(true)
			require.Error(t, err, in)
			require.ErrorContains(t, err, "imprecision_without_pool: unknown answer", in)
			require.NoError(t, c.Normalize(false), "dropped when not required: "+in)
			assert.Nil(t, c.ImprecisionWithoutPool, in)
		}
	})

	t.Run("a null or empty imprecision is a missing one", func(t *testing.T) {
		for _, in := range []string{`null`, `""`, `"  "`} {
			c := decodeChecklist(t, `{"risk_of_bias":{"per_study":[]},`+rest+`,"imprecision_without_pool":`+in+`}`)

			require.ErrorContains(t, c.Normalize(true), "imprecision_without_pool: missing", in)
		}
	})

	t.Run("a value of a wrong type is remembered, the rest still decodes", func(t *testing.T) {
		c := decodeChecklist(t, `{"risk_of_bias":{"per_study":"none"},`+rest+`,"imprecision_without_pool":1}`)

		assert.Equal(t, No, c.Inconsistency.DirectionsConflict, "the other fields are read")
		require.NotNil(t, c.ImprecisionWithoutPool)
		require.ErrorContains(t, c.Normalize(false), "wrong JSON type")
		require.ErrorContains(t, c.Normalize(false), "per_study")
	})

	t.Run("a second decode into the same value starts clean", func(t *testing.T) {
		c := decodeChecklist(t, `{"risk_of_bias":{"per_study":"none"},`+rest+`}`)
		require.Error(t, c.Normalize(false))

		require.NoError(t, json.Unmarshal([]byte(`{"risk_of_bias":{"per_study":[]},`+rest+`}`), &c))
		require.NoError(t, c.Normalize(false))
	})

	t.Run("a syntax error in the object is still a decode error", func(t *testing.T) {
		var c GradeChecklist

		require.Error(t, c.UnmarshalJSON([]byte(`{"risk_of_bias":`)))
	})
}

func decodeChecklist(t *testing.T, raw string) GradeChecklist {
	t.Helper()

	var c GradeChecklist

	require.NoError(t, json.Unmarshal([]byte(raw), &c), raw)

	return c
}
