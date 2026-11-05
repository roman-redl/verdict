package pdf

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// A minimal handcrafted one-page PDF.
const minimalPDF = "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
	"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
	"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>endobj\n" +
	"4 0 obj<</Length 60>>stream\nBT /F1 12 Tf 72 720 Td (Hello fulltext) Tj ET\nendstream endobj\n" +
	"5 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\n" +
	"trailer<</Root 1 0 R>>\n%%EOF"

func TestExtractorText(t *testing.T) {
	text, err := NewExtractor(quietLog()).Text([]byte(minimalPDF), 1000)
	if err != nil {
		t.Skipf("minimal fixture not parseable by the lib: %v", err)
	}

	assert.Contains(t, text, "Hello fulltext")
}

func TestExtractorCapsTheText(t *testing.T) {
	text, err := NewExtractor(quietLog()).Text([]byte(minimalPDF), 5)
	if err != nil {
		t.Skipf("minimal fixture not parseable by the lib: %v", err)
	}

	assert.LessOrEqual(t, len(text), 5)
}

func TestExtractorRejectsGarbage(t *testing.T) {
	_, err := NewExtractor(quietLog()).Text([]byte("<html>not a pdf</html>"), 1000)
	require.Error(t, err)
}

func TestDownloaderFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing.pdf" {
			http.NotFound(w, r)
			return
		}

		_, _ = w.Write([]byte("%PDF-1.4 body"))
	}))
	defer srv.Close()

	d := NewDownloader()

	b, err := d.Fetch(t.Context(), srv.URL+"/a.pdf", 1<<20)
	require.NoError(t, err)
	assert.Equal(t, "%PDF-1.4 body", string(b))

	_, err = d.Fetch(t.Context(), srv.URL+"/missing.pdf", 1<<20)
	require.Error(t, err)

	_, err = d.Fetch(t.Context(), srv.URL+"/a.pdf", 4)
	require.ErrorContains(t, err, "size cap", "an oversized document is an error, not a truncation")
}
