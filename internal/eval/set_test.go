package eval

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validQuestion = `
  - id: %ID%
    question: q
    claim: c
    works_if: higher
    answer: higher
    certainty: high
    specialty: s
    review_pmid: "1"
    review_doi: 10.1/r
    studies:
      - {pmid: "2", doi: 10.1/a}
`

func TestLoadSet(t *testing.T) {
	qs, err := LoadSet(filepath.Join("testdata", "set.yaml"))
	require.NoError(t, err)
	require.Len(t, qs, 3)

	q := qs[0]
	assert.Equal(t, 10, q.ID)
	assert.Equal(t, ChangeHigher, q.WorksIf)
	assert.Equal(t, AnswerHigher, q.Answer)
	assert.Equal(t, CertaintyHigh, q.Certainty)
	assert.Equal(t, "Oncology & Hematology", q.Specialty)
	assert.Equal(t, "36999589", q.ReviewPMID)
	assert.Equal(t, "10.1002/14651858.CD009632.pub3", q.ReviewDOI)
	assert.True(t, strings.HasPrefix(q.Claim, "In women with dense breasts"))
	assert.Len(t, q.Studies, 3)
	assert.Equal(t, []string{"10.1001/jamanetworkopen.2021.21505", "10.1016/j.ejrad.2018.01.022"}, q.DOIs(),
		"a study without a DOI cannot enter a DOI-list run")
	assert.Equal(t, OutcomeWorks, q.Expected())

	assert.Equal(t, OutcomeNoEffect, qs[1].Expected())
	assert.Equal(t, CertaintyNone, qs[2].Certainty)
	assert.Equal(t, OutcomeNotProven, qs[2].Expected())
}

// The shipped benchmark set must stay loadable.
func TestShippedSetLoads(t *testing.T) {
	qs, err := LoadSet(filepath.Join("..", "..", "evals", "medevidence.yaml"))
	require.NoError(t, err)
	assert.NotEmpty(t, qs)
}

func TestLoadSetErrors(t *testing.T) {
	_, err := LoadSet(filepath.Join("testdata", "missing.yaml"))
	require.ErrorContains(t, err, "opening the set")

	_, err = LoadSet("testdata")
	require.Error(t, err)
}

func TestParseSetErrors(t *testing.T) {
	q := func(id string) string { return strings.ReplaceAll(validQuestion, "%ID%", id) }
	// broken is a one-question set with one substring of the valid question replaced
	broken := func(old, repl string) string { return "questions:" + strings.Replace(q("1"), old, repl, 1) }

	const studiesBlock = "    studies:\n      - {pmid: \"2\", doi: 10.1/a}\n"

	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{"empty input", "", "empty"},
		{"no questions", "questions: []\n", "no questions"},
		{"unknown top-level field", "questions:" + q("1") + "extra: 1\n", "field extra not found"},
		{"unknown question field", "questions:" + q("1") + "    color: red\n", "field color not found"},
		{"duplicate id", "questions:" + q("1") + q("1"), "duplicate question id 1"},
		{"bad works_if", broken("works_if: higher", "works_if: same"), "works_if"},
		{"bad answer", broken("answer: higher", "answer: maybe"), "answer"},
		{"bad certainty", broken("certainty: high", "certainty: sure"), "certainty"},
		{"empty claim", broken("claim: c", `claim: " "`), "empty claim"},
		{"no studies", broken(studiesBlock, ""), "no study with a DOI"},
		{"studies without DOI", broken("doi: 10.1/a", `doi: ""`), "no study with a DOI"},
		{"not yaml", "questions: [", "decoding the set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSet(strings.NewReader(tc.yaml))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestParseSetNamesTheBrokenQuestion(t *testing.T) {
	yaml := "questions:" + strings.ReplaceAll(validQuestion, "%ID%", "1") +
		strings.Replace(strings.ReplaceAll(validQuestion, "%ID%", "7"), "answer: higher", "answer: maybe", 1)

	_, err := ParseSet(strings.NewReader(yaml))
	require.ErrorContains(t, err, "question #2 (id 7)")
}

func TestDOIsNormalizedAndDeduplicated(t *testing.T) {
	q := Question{Studies: []Study{
		{DOI: "https://doi.org/10.1/A"}, {DOI: "10.1/a"}, {DOI: "10.1/b"}, {DOI: " "},
	}}
	assert.Equal(t, []string{"10.1/a", "10.1/b"}, q.DOIs())
}

func TestSelect(t *testing.T) {
	qs := []Question{{ID: 10}, {ID: 30}, {ID: 230}}

	all, err := Select(qs, nil)
	require.NoError(t, err)
	assert.Len(t, all, 3)

	got, err := Select(qs, []int{230, 10})
	require.NoError(t, err)
	assert.Equal(t, []Question{{ID: 10}, {ID: 230}}, got, "the order of the set, not of the flag")

	_, err = Select(qs, []int{10, 99, 100})
	require.ErrorContains(t, err, "99, 100")
}
