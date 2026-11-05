package pdf

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// buildPDF assembles a valid one-page-per-string PDF: objects, a cross
// reference table with real byte offsets and a trailer.
func buildPDF(pages ...string) []byte {
	var (
		sb      strings.Builder
		offsets []int
	)

	sb.WriteString("%PDF-1.4\n")

	add := func(body string) {
		offsets = append(offsets, sb.Len())
		fmt.Fprintf(&sb, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}

	add("<</Type/Catalog/Pages 2 0 R>>")

	kids := make([]string, len(pages))
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}

	add(fmt.Sprintf("<</Type/Pages/Kids[%s]/Count %d>>", strings.Join(kids, " "), len(pages)))
	add("<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>")

	for i, text := range pages {
		add(fmt.Sprintf("<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents %d 0 R"+
			"/Resources<</Font<</F1 3 0 R>>>>>>", 5+2*i))

		stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
		add(fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(stream), stream))
	}

	xref := sb.Len()
	fmt.Fprintf(&sb, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)

	for _, o := range offsets {
		fmt.Fprintf(&sb, "%010d 00000 n \n", o)
	}

	fmt.Fprintf(&sb, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)

	return []byte(sb.String())
}

func TestExtractorText(t *testing.T) {
	text, err := NewExtractor(quietLog()).Text(buildPDF("Hello fulltext", "second page"), 1000)
	require.NoError(t, err)

	assert.Contains(t, text, "Hello fulltext")
	assert.Contains(t, text, "second page")
}

// The text stops at the cap, and later pages are not even read.
func TestExtractorCapsTheText(t *testing.T) {
	text, err := NewExtractor(quietLog()).Text(buildPDF("Hello fulltext", "second page"), 5)
	require.NoError(t, err)

	assert.LessOrEqual(t, len(text), 5)
	assert.NotContains(t, text, "second")
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
