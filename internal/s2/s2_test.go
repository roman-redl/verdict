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

// One batched POST; the API answers index-aligned, unknown DOIs arrive as
// null entries in their position and are absent from the result.
func TestLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/paper/batch", r.URL.Path)
		assert.Contains(t, r.URL.Query().Get("fields"), "influentialCitationCount")
		assert.Equal(t, "k", r.Header.Get("x-api-key"))

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

	c := NewWithBase(srv.URL, "k")

	got, err := c.Lookup(context.Background(), []string{"10.1/a", "10.1/void"})
	require.NoError(t, err)

	require.Len(t, got, 1)
	require.NotContains(t, got, "10.1/void")

	a := got["10.1/a"]
	require.Equal(t, "abs", a.Abstract)
	require.Equal(t, "A one-liner", a.TLDR)
	require.Equal(t, 3, a.InfluentialCitations)
	require.Equal(t, "https://x/a.pdf", a.OAPDF)
}

// A paper without a TLDR or an open-access PDF maps to empty fields.
func TestLookupSparseRecord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"externalIds":{"DOI":"10.1/b"},"title":"B","tldr":null,"openAccessPdf":null}]`))
	}))
	defer srv.Close()

	got, err := NewWithBase(srv.URL, "").Lookup(context.Background(), []string{"10.1/B"})
	require.NoError(t, err)

	require.Contains(t, got, "10.1/b", "keys are normalized DOIs")
	require.Empty(t, got["10.1/b"].TLDR)
	require.Empty(t, got["10.1/b"].OAPDF)
}

func TestLookupFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := NewWithBase(srv.URL, "").Lookup(context.Background(), []string{"10.1/a"})
	require.Error(t, err)
}

func TestLookupEmpty(t *testing.T) {
	c := New("")

	got, err := c.Lookup(context.Background(), nil)
	require.NoError(t, err, "empty batch")
	require.Empty(t, got)
}
