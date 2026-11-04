package s2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The anonymous API is verified live: batch POST works keyless, unknown
// DOIs arrive as null entries in their position.
func TestBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/paper/batch", r.URL.Path)

		var body struct {
			IDs []string `json:"ids"`
		}

		_ = json.NewDecoder(r.Body).Decode(&body)

		if assert.Len(t, body.IDs, 2) {
			assert.Equal(t, "DOI:10.1/a", body.IDs[0])
		}

		_, _ = w.Write([]byte(`[
			{"externalIds":{"DOI":"10.1/a"},"title":"Study A","abstract":"abs",
			 "tldr":{"text":"A one-liner"},"citationCount":10,"influentialCitationCount":3,
			 "openAccessPdf":{"url":"https://x/a.pdf"}},
			null
		]`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, "")

	papers, err := c.Batch(context.Background(), []string{"10.1/a", "10.1/void"})
	require.NoError(t, err)

	require.Len(t, papers, 2)
	require.Nil(t, papers[1])

	a := papers[0]
	require.Equal(t, "Study A", a.Title)
	require.Equal(t, "A one-liner", a.TLDRText())
	require.Equal(t, 3, a.InfluentialCitations)
	require.Equal(t, "https://x/a.pdf", a.OpenAccessPDF.URL)
}

func TestBatchEmpty(t *testing.T) {
	c := New("")

	papers, err := c.Batch(context.Background(), nil)
	require.NoError(t, err, "empty batch")
	require.Nil(t, papers)
}
