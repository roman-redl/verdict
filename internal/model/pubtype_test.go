package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Publication types as the sources spell them (PubMed capitalizes, OpenAlex does not).
const (
	typeArticle        = "article"
	typeJournalArticle = "journal-article"
	typeErratum        = "erratum"
	typeComment        = "Comment"
	typeNews           = "News"
	typeTrial          = "Randomized Controlled Trial"
	lowerComment       = "comment"
)

func TestNonStudyKind(t *testing.T) {
	for _, tc := range []struct {
		name  string
		types []string
		want  string
	}{
		{"nothing known", nil, ""},
		{"an article", []string{typeArticle, typeJournalArticle}, ""},
		{"a review article is a design question, not a type one", []string{"review"}, ""},
		{"OpenAlex erratum", []string{typeErratum}, kindErratum},
		{"PubMed published erratum", []string{"Published Erratum", "Journal Article"}, kindErratum},
		{"a retraction notice", []string{"Retraction of Publication"}, kindRetraction},
		{"OpenAlex retraction", []string{"retraction"}, kindRetraction},
		{"an expression of concern", []string{"Expression of Concern"}, "expression of concern"},
		{"a comment", []string{typeComment, "Journal Article"}, lowerComment},
		{"an editorial", []string{"Editorial"}, "editorial"},
		{"a letter", []string{"Letter"}, "letter"},
		{"news", []string{typeNews}, kindNews},
		{"a newspaper article", []string{"Newspaper Article"}, kindNews},
		{"front matter", []string{"paratext"}, "front or back matter"},
		{"a peer review report", []string{"peer-review"}, "peer review report"},
		{"the retracted paper itself is a study, the notice is not", []string{"Retracted Publication"}, ""},
		{"a letter that is a trial report", []string{"Letter", typeTrial}, ""},
		{"a comment on a clinical trial typed as one", []string{typeComment, "Clinical Trial, Phase III"}, ""},
		{"an editorial typed as an observational study", []string{"editorial", "Observational Study"}, ""},
		{"spaces and case do not matter", []string{"  PUBLISHED   ERRATUM "}, kindErratum},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, NonStudyKind(tc.types))
		})
	}
}

func TestNormalizeAndMergePublicationTypes(t *testing.T) {
	assert.Equal(t, []string{typeArticle, lowerComment},
		NormalizePublicationTypes([]string{typeComment, " article", "COMMENT", ""}))
	assert.Nil(t, NormalizePublicationTypes(nil))
	assert.Equal(t, []string{typeArticle, lowerComment, typeErratum},
		MergePublicationTypes([]string{"Article"}, nil, []string{lowerComment, typeArticle}, []string{"Erratum"}))
}

// A paper written before the field existed loads without types and is a study.
func TestPaperWithoutPublicationTypesLoads(t *testing.T) {
	var p Paper

	require.NoError(t, json.Unmarshal([]byte(`{"doi":"10.1/a","title":"T","source":"consensus"}`), &p))
	assert.Empty(t, p.PublicationTypes)
	assert.Empty(t, NonStudyKind(p.PublicationTypes))

	b, err := json.Marshal(p)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "publication_types")
}

func TestWorkToPaperCarriesTheTypes(t *testing.T) {
	w := Work{DOI: "https://doi.org/10.1/x", Types: []string{typeErratum, typeJournalArticle}}

	assert.Equal(t, []string{typeErratum, typeJournalArticle}, w.ToPaper("").PublicationTypes)
}
