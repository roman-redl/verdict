package pipeline

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Role names as the model writes them.
const (
	namePopulation   = string(RolePopulation)
	nameIntervention = string(RoleIntervention)
	nameComparator   = string(RoleComparator)
)

func TestParseConceptRole(t *testing.T) {
	tests := []struct {
		in      string
		want    ConceptRole
		wantErr bool
	}{
		{namePopulation, RolePopulation, false},
		{" Condition ", RolePopulation, false},
		{nameIntervention, RoleIntervention, false},
		{"Exposure", RoleIntervention, false},
		{nameComparator, RoleComparator, false},
		{"control", RoleComparator, false},
		{outcomeName, "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseConceptRole(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBalanced(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{`(a[tiab] OR b[tiab]) AND (c[tiab])`, true},
		{`((a) AND (b OR (c)))`, true},
		{`"vitamin D (cholecalciferol)"[tiab]`, true},
		{`"an open (quote"[tiab]`, true},
		{`(a[tiab]`, false},
		{`a[tiab])`, false},
		{`)a(`, false},
		{`"unclosed[tiab]`, false},
		{pmGERD, true},
		{``, true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, balanced(tt.in))
		})
	}
}

func TestTopicQuery(t *testing.T) {
	concepts := []PubMedConcept{
		{Role: RolePopulation, Terms: []string{`"Gastroesophageal Reflux"[Mesh]`, pmGERD}},
		{Role: RoleIntervention, Terms: []string{pmFundo, "Nissen[tiab] AND surgery[tiab]"}},
		{Role: RoleComparator, Terms: []string{pmOmep}},
	}

	tests := []struct {
		name           string
		concepts       []PubMedConcept
		withComparator bool
		want           string
	}{
		{
			name: "OR inside a concept, AND between concepts; a compound term stays one unit", concepts: concepts,
			withComparator: true,
			want: `("Gastroesophageal Reflux"[Mesh] OR GERD[tiab]) AND ` +
				`(fundoplicat*[tiab] OR (Nissen[tiab] AND surgery[tiab])) AND (omeprazole[tiab])`,
		},
		{
			name: "without the comparator", concepts: concepts, withComparator: false,
			want: `("Gastroesophageal Reflux"[Mesh] OR GERD[tiab]) AND ` +
				`(fundoplicat*[tiab] OR (Nissen[tiab] AND surgery[tiab]))`,
		},
		{
			name: "a single concept", concepts: concepts[:1], withComparator: true,
			want: `("Gastroesophageal Reflux"[Mesh] OR GERD[tiab])`,
		},
		{name: "no concepts", concepts: nil, withComparator: true, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := topicQuery(tt.concepts, tt.withComparator)
			assert.Equal(t, tt.want, got)
			assert.True(t, balanced(got))
		})
	}
}

func TestValidConcepts(t *testing.T) {
	many := make([]string, maxTermsPerConcept+5)
	for i := range many {
		many[i] = fmt.Sprintf("term%d[tiab]", i)
	}

	type concept = struct {
		Name  string   `json:"name"`
		Terms []string `json:"terms"`
	}

	tests := []struct {
		name     string
		concepts []concept
		want     []PubMedConcept
		wantErr  string
	}{
		{
			name: "the outcome is dropped, the order is fixed, a comparator stays",
			concepts: []concept{
				{nameComparator, []string{pmOmep}},
				{outcomeName, []string{"pain[tiab]"}},
				{nameIntervention, []string{pmFundo}},
				{namePopulation, []string{pmGERD}},
			},
			want: []PubMedConcept{
				{RolePopulation, []string{pmGERD}},
				{RoleIntervention, []string{pmFundo}},
				{RoleComparator, []string{pmOmep}},
			},
		},
		{
			name: "two concepts of one role are merged, repeats and blanks dropped",
			concepts: []concept{
				{nameIntervention, []string{"drugA[tiab]", " ", "DRUGA[tiab]"}},
				{"treatment", []string{"drugB[tiab]"}},
			},
			want: []PubMedConcept{{RoleIntervention, []string{"drugA[tiab]", "drugB[tiab]"}}},
		},
		{
			name:     "too many terms are cut",
			concepts: []concept{{namePopulation, many}},
			want:     []PubMedConcept{{RolePopulation, many[:maxTermsPerConcept]}},
		},
		{
			name:     "an unbalanced term is an error",
			concepts: []concept{{namePopulation, []string{"(GERD[tiab]"}}},
			wantErr:  "unbalanced",
		},
		{
			name:     "only an outcome leaves nothing",
			concepts: []concept{{outcomeName, []string{"pain[tiab]"}}},
			wantErr:  pmNoTerms,
		},
		{
			name:     "a comparator alone is not a topic",
			concepts: []concept{{nameComparator, []string{pmOmep}}},
			wantErr:  pmNoTerms,
		},
		{name: "no concepts", concepts: nil, wantErr: pmNoTerms},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTest(t, Settings{}, Clients{}, quietLog())
			ans := &pubmedQueryAnswer{Concepts: tt.concepts}

			got, err := p.validConcepts(ans)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWritePubMedQueryNeedsThePlanModel(t *testing.T) {
	p := newTest(t, Settings{}, Clients{}, quietLog())

	_, _, err := p.writePubMedQuery(t.Context(), &State{}, "q")

	var ue *UserError

	require.ErrorAs(t, err, &ue)
	assert.Contains(t, ue.Msg, "--pubmed-query")
}

// The prompt must keep the rules the recall depends on.
func TestPubMedQueryPromptStatesTheRules(t *testing.T) {
	for _, want := range []string{
		"Never make a concept of the outcome", "[Mesh]", "[tiab]", "brand names", "fundoplicat*",
		"never truncate a short stem", `"concepts"`, "comparator: only when it is a specific named",
	} {
		assert.Contains(t, pubmedQuerySystemPrompt, want)
	}
}
