package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// pdfServer serves "%PDF" bytes for every URL except the landing page.
func pdfServer() *fakePDFs {
	return &fakePDFs{fetch: func(url string) ([]byte, error) {
		if strings.Contains(url, "landing") {
			return []byte("<html>not a pdf</html>"), nil
		}

		return []byte("%PDF-1.4 fake-but-sniffable"), nil
	}}
}

func TestFetchFullText(t *testing.T) {
	runDir := t.TempDir()
	pdfs := pdfServer()
	p := New(Settings{Concurrency: 4}, Clients{
		PDFs:    pdfs,
		PDFText: fakePDFText{text: "METHODS We randomized 240 patients. RESULTS No effect."},
	}, quietLog())

	ok := &model.EnrichedPaper{DOI: listDOIA, OALink: oaPDFA}
	txt := p.fetchFullText(context.Background(), runDir, ok, true)
	require.NotEmpty(t, txt)
	require.Contains(t, txt, "240 patients")

	require.Empty(t, ok.FullText, "fetchFullText must not mutate FullText itself") // the caller (enrich) assigns the field

	require.FileExists(t, filepath.Join(runDir, "fulltext", "10.1000_a.pdf"), "PDF not saved for the vision stage")

	landing := &model.EnrichedPaper{DOI: listDOIB, OALink: "https://oa.test/landing"}
	landingTxt := p.fetchFullText(context.Background(), runDir, landing, true)
	require.Empty(t, landingTxt, "landing page must yield no text")

	require.NotEmpty(t, landing.Errors, "non-PDF link must be recorded as an error")
	assert.NoFileExists(t, filepath.Join(runDir, "fulltext", "10.1000_b.pdf"))
}

// Without the download permission an OA link is not followed.
func TestFetchFullTextNoDownloadWhenNotAllowed(t *testing.T) {
	pdfs := pdfServer()
	p := New(Settings{}, Clients{PDFs: pdfs, PDFText: fakePDFText{text: "x"}}, quietLog())

	e := &model.EnrichedPaper{DOI: listDOIA, OALink: oaPDFA}

	assert.Empty(t, p.fetchFullText(context.Background(), t.TempDir(), e, false))
	assert.Zero(t, pdfs.calls.total())
	assert.Empty(t, e.Errors)
}

// Download and extraction failures are recorded on the paper, not raised.
func TestFetchFullTextFailuresAreRecorded(t *testing.T) {
	tests := []struct {
		name    string
		pdfs    PDFSource
		pdftext PDFText
		want    string
	}{
		{
			name:    "download fails",
			pdfs:    &fakePDFs{fetch: func(string) ([]byte, error) { return nil, errors.New("HTTP 403") }},
			pdftext: fakePDFText{text: "x"},
			want:    "HTTP 403",
		},
		{
			name:    "extraction fails",
			pdfs:    pdfServer(),
			pdftext: fakePDFText{err: errors.New("corrupt xref")},
			want:    "text extraction failed: corrupt xref",
		},
		{
			name:    "no text in the PDF",
			pdfs:    pdfServer(),
			pdftext: fakePDFText{text: ""},
			want:    "no extractable text",
		},
		{
			name: "no extractor wired",
			pdfs: pdfServer(),
			want: "no PDF text extractor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(Settings{}, Clients{PDFs: tt.pdfs, PDFText: tt.pdftext}, quietLog())
			e := &model.EnrichedPaper{DOI: listDOIA, OALink: oaPDFA}

			assert.Empty(t, p.fetchFullText(context.Background(), t.TempDir(), e, true))
			require.Len(t, e.Errors, 1)
			assert.Contains(t, e.Errors[0], "fulltext: ")
			assert.Contains(t, e.Errors[0], tt.want)
		})
	}
}

// The agent-written chart critique slots into synthesis as data; a broken
// file must never kill the run.
func TestLoadVisionNotes(t *testing.T) {
	dir := t.TempDir()
	st := &State{}
	p := New(Settings{}, Clients{}, quietLog())
	p.loadVisionNotes(dir, st) // no file at all

	require.Empty(t, st.VisionNotes, "no artifact must yield no notes")

	broken := filepath.Join(dir, "3b-vision.json")
	require.NoError(t, os.WriteFile(broken, []byte("{not json"), 0o600))

	p.loadVisionNotes(dir, st)

	require.Empty(t, st.VisionNotes, "broken artifact must yield no notes")

	good := `{"vision_notes":[{"doi":"10.1/x","findings":["y-axis truncated at 40% (fig 2)"]}]}`
	require.NoError(t, os.WriteFile(broken, []byte(good), 0o600))

	p.loadVisionNotes(dir, st)

	require.Len(t, st.VisionNotes, 1)
	require.Equal(t, "10.1/x", st.VisionNotes[0].DOI)
	require.Equal(t, "y-axis truncated at 40% (fig 2)", st.VisionNotes[0].Findings[0])
}

// A PDF pre-placed by the user wins over download, works without the
// --fulltext flag and without an OA link (the paywall workaround).
func TestFetchFullTextPreplaced(t *testing.T) {
	runDir := t.TempDir()
	pdfs := pdfServer()
	p := New(Settings{Concurrency: 4}, Clients{
		PDFs:    pdfs,
		PDFText: fakePDFText{text: "USER-SUPPLIED full text of a paywalled paper"},
	}, quietLog())

	require.NoError(t, os.MkdirAll(filepath.Join(runDir, "fulltext"), 0o750))

	pdf := filepath.Join(runDir, "fulltext", "10.1000_paywalled.pdf")
	require.NoError(t, os.WriteFile(pdf, []byte("%PDF-1.4 user-supplied"), 0o600))

	noLink := &model.EnrichedPaper{DOI: "10.1000/paywalled"} // no OALink, no --fulltext
	txt := p.fetchFullText(context.Background(), runDir, noLink, false)
	require.Equal(t, "USER-SUPPLIED full text of a paywalled paper", txt, "pre-placed PDF not used")

	require.Empty(t, noLink.Errors, "pre-placed path must not error")
	require.Zero(t, pdfs.calls.total(), "the user's file wins over any download")
}

// PDFDOIs reads the paper's identity out of a user-supplied file.
func TestPDFDOIs(t *testing.T) {
	x := fakePDFText{text: "JAMA Dermatol. doi:10.1001/jamadermatol.2020.0218 Published online."}

	dois, err := PDFDOIs(x, []byte("%PDF-1.4 whatever"))
	require.NoError(t, err)
	require.Equal(t, []string{"10.1001/jamadermatol.2020.0218"}, dois)

	_, err = PDFDOIs(fakePDFText{err: errors.New("corrupt")}, []byte("%PDF"))
	require.ErrorContains(t, err, "corrupt")
}
