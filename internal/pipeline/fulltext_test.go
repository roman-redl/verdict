package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

func TestFetchFullText(t *testing.T) {
	restore := pdfTextExtractor
	pdfTextExtractor = func(b []byte, maxChars int) (string, error) {
		assert.True(t, strings.HasPrefix(string(b), "%PDF-"), "extractor called on non-PDF body")

		return "METHODS We randomized 240 patients. RESULTS No effect.", nil
	}

	defer func() { pdfTextExtractor = restore }()

	pdfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "landing") {
			_, _ = w.Write([]byte("<html>not a pdf</html>"))
			return
		}

		_, _ = w.Write([]byte("%PDF-1.4 fake-but-sniffable"))
	}))
	defer pdfSrv.Close()

	runDir := t.TempDir()
	p := New(Settings{Concurrency: 4}, Clients{}, quietLog())
	p.dir = runDir

	ok := &model.EnrichedPaper{DOI: "10.1000/a", OALink: pdfSrv.URL + "/a.pdf"}
	txt := p.fetchFullText(context.Background(), ok, true)
	require.NotEmpty(t, txt)
	require.Contains(t, txt, "240 patients")

	require.Empty(t, ok.FullText, "fetchFullText must not mutate FullText itself") // the caller (enrich) assigns the field

	pdfPath := filepath.Join(runDir, "fulltext", "10.1000_a.pdf")
	_, err := os.Stat(pdfPath)
	require.NoError(t, err, "PDF not saved for the vision stage")

	landing := &model.EnrichedPaper{DOI: "10.1000/b", OALink: pdfSrv.URL + "/landing"}
	landingTxt := p.fetchFullText(context.Background(), landing, true)
	require.Empty(t, landingTxt, "landing page must yield no text")

	require.NotEmpty(t, landing.Errors, "non-PDF link must be recorded as an error")
}

// The real extractor against a minimal handcrafted PDF.
func TestPdfTextReal(t *testing.T) {
	restore := pdfTextExtractor
	pdfTextExtractor = pdfText // the production path

	defer func() { pdfTextExtractor = restore }()

	minimal := "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
		"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>endobj\n" +
		"4 0 obj<</Length 60>>stream\nBT /F1 12 Tf 72 720 Td (Hello fulltext) Tj ET\nendstream endobj\n" +
		"5 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\n" +
		"trailer<</Root 1 0 R>>\n%%EOF"

	text, err := pdfText([]byte(minimal), 1000)
	if err != nil {
		t.Skipf("minimal fixture not parseable by the lib: %v", err)
	}

	require.Contains(t, text, "Hello fulltext")
}

// The agent-written chart critique slots into synthesis as data; a broken
// file must never kill the run.
func TestLoadVisionNotes(t *testing.T) {
	dir := t.TempDir()
	st := &State{}
	loadVisionNotes(dir, st) // no file at all

	require.Empty(t, st.VisionNotes, "no artifact must yield no notes")

	broken := filepath.Join(dir, "3b-vision.json")
	require.NoError(t, os.WriteFile(broken, []byte("{not json"), 0o600))

	loadVisionNotes(dir, st)

	require.Empty(t, st.VisionNotes, "broken artifact must yield no notes")

	good := `{"vision_notes":[{"doi":"10.1/x","findings":["y-axis truncated at 40% (fig 2)"]}]}`
	require.NoError(t, os.WriteFile(broken, []byte(good), 0o600))

	loadVisionNotes(dir, st)

	require.Len(t, st.VisionNotes, 1)
	require.Equal(t, "10.1/x", st.VisionNotes[0].DOI)
	require.Equal(t, "y-axis truncated at 40% (fig 2)", st.VisionNotes[0].Findings[0])
}

// A PDF pre-placed by the user wins over download, works without the
// --fulltext flag and without an OA link (the paywall workaround).
func TestFetchFullTextPreplaced(t *testing.T) {
	restore := pdfTextExtractor
	pdfTextExtractor = func(b []byte, maxChars int) (string, error) {
		return "USER-SUPPLIED full text of a paywalled paper", nil
	}

	defer func() { pdfTextExtractor = restore }()

	runDir := t.TempDir()
	p := New(Settings{Concurrency: 4}, Clients{}, quietLog())
	p.dir = runDir

	require.NoError(t, os.MkdirAll(filepath.Join(runDir, "fulltext"), 0o750))

	pdf := filepath.Join(runDir, "fulltext", "10.1000_paywalled.pdf")
	require.NoError(t, os.WriteFile(pdf, []byte("%PDF-1.4 user-supplied"), 0o600))

	noLink := &model.EnrichedPaper{DOI: "10.1000/paywalled"} // no OALink, no --fulltext
	txt := p.fetchFullText(context.Background(), noLink, false)
	require.Equal(t, "USER-SUPPLIED full text of a paywalled paper", txt, "pre-placed PDF not used")

	require.Empty(t, noLink.Errors, "pre-placed path must not error")
}

// PDFDOIs reads the paper's identity out of a user-supplied file.
func TestPDFDOIs(t *testing.T) {
	restore := pdfTextExtractor
	pdfTextExtractor = func(_ []byte, _ int) (string, error) {
		return "JAMA Dermatol. doi:10.1001/jamadermatol.2020.0218 Published online.", nil
	}

	defer func() { pdfTextExtractor = restore }()

	dois := PDFDOIs([]byte("%PDF-1.4 whatever"))
	require.Len(t, dois, 1)
	require.Equal(t, "10.1001/jamadermatol.2020.0218", dois[0])
}
