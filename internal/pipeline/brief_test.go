package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

func TestSaveBrief(t *testing.T) {
	out := t.TempDir()

	b := &Brief{
		Problem:   "Which creatine form should I buy, and does it help my focus?",
		Decision:  "buy or skip",
		Questions: []BriefQuestion{{Question: "Does creatine improve cognition?", Priority: "high"}},
	}

	path, err := SaveBrief(out, b, testNow)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(out, "which-creatine-form-should-i-buy-and-doe-20261101-120000", "brief.md"), path)

	//nolint:gosec // reads the file SaveBrief just wrote into the test's temp directory
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "# Research brief")
	assert.Contains(t, string(body), "Does creatine improve cognition?")
}

// A problem with no letters or digits still gets a usable directory name.
func TestSaveBriefFallbackName(t *testing.T) {
	path, err := SaveBrief(t.TempDir(), &Brief{Problem: "???"}, testNow)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(filepath.Base(filepath.Dir(path)), "brief-20261101-120000"))
}

func TestSaveBriefUnwritable(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))

	_, err := SaveBrief(blocker, &Brief{Problem: "p"}, testNow)
	require.Error(t, err, "an output path under a regular file cannot be created")
}

func TestAddPDF(t *testing.T) {
	runDir := t.TempDir()
	x := fakePDFText{text: "JAMA Dermatol. doi:10.1001/jamadermatol.2020.0218 Published online."}

	added, err := AddPDF(x, runDir, []byte("%PDF-1.4 content"))
	require.NoError(t, err)

	assert.Equal(t, "10.1001/jamadermatol.2020.0218", added.DOI)
	assert.Equal(t, filepath.Join(runDir, "fulltext", "10.1001_jamadermatol.2020.0218.pdf"), added.Path)
	assert.FileExists(t, added.Path)

	// the pipeline finds the dropped file under the same name
	assert.Equal(t, added.Path, fullTextPath(runDir, added.DOI))
}

func TestAddPDFRejects(t *testing.T) {
	runDir := t.TempDir()

	var ue *UserError

	_, err := AddPDF(fakePDFText{text: "x"}, runDir, []byte("<html>"))
	require.ErrorAs(t, err, &ue)
	assert.Equal(t, "not a PDF", ue.Msg)

	_, err = AddPDF(fakePDFText{text: "no identifiers here"}, runDir, []byte("%PDF-1.4"))
	require.ErrorAs(t, err, &ue)
	assert.Contains(t, ue.Msg, "no DOI found")
	assert.Contains(t, ue.Msg, filepath.Join("fulltext", "<doi>.pdf"))

	_, err = AddPDF(fakePDFText{err: os.ErrInvalid}, runDir, []byte("%PDF-1.4"))
	require.ErrorIs(t, err, os.ErrInvalid)
	assert.NoDirExists(t, filepath.Join(runDir, "fulltext"), "nothing is created for a rejected file")
}

func TestPaperCount(t *testing.T) {
	st := &State{}
	assert.Zero(t, st.PaperCount())

	st.Papers = make([]model.Paper, 3)
	assert.Equal(t, 3, st.PaperCount())

	st.Enriched = make([]model.EnrichedPaper, 2)
	assert.Equal(t, 2, st.PaperCount(), "the enriched list wins once it exists")
}
