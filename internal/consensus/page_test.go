package consensus

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// Page is the pipeline's view of a search: papers with the DOIs normalized,
// plus whether the ranking ended and the page size the server applied.
func TestPageMapsResultsToPapers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "3", r.URL.Query().Get("page"))
		assert.Equal(t, "creatine", r.URL.Query().Get("query"))
		assert.Equal(t, "100", r.URL.Query().Get("page_size"), "the configured page size is sent")

		_, _ = w.Write([]byte(`{"results":[
			{"title":"A","doi":"https://doi.org/10.1/A","study_type":"rct","publish_year":2022},
			{"title":"B","doi":"","publish_year":2020}
		],"page_size":20,"is_end":true}`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, "k", 100)
	require.True(t, c.HasKey())
	require.False(t, New("", 0).HasKey())

	got, err := c.Page(t.Context(), "creatine", 3)
	require.NoError(t, err)

	assert.True(t, got.IsEnd)
	assert.Equal(t, 20, got.PageSize, "the applied size comes from the response")
	assert.Equal(t, 100, got.PageSizeAsked, "the requested size is the client's setting")
	require.Len(t, got.Papers, 2)
	assert.Equal(t, "10.1/a", got.Papers[0].DOI)
	assert.Equal(t, model.SourceConsensus, got.Papers[0].Source)
	assert.Equal(t, "rct", got.Papers[0].StudyDesign)
	assert.Empty(t, got.Papers[1].DOI, "a result without a DOI is returned as is; dropping it is the pipeline's call")
}

// Without a configured size the request leaves page_size to the server.
func TestPageWithoutSizeOmitsIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NotContains(t, r.URL.Query(), "page_size")

		_, _ = w.Write([]byte(`{"results":[],"page_size":20,"is_end":true}`))
	}))
	defer srv.Close()

	_, err := NewWithBase(srv.URL, "k", 0).Page(t.Context(), "q", 0)
	require.NoError(t, err)
}

// A 403 means the Free plan only on a page after the first.
func TestPageForbidden(t *testing.T) {
	tests := []struct {
		name       string
		page       int
		wantDenied bool
	}{
		{"first page: a bad key or a spent quota", 0, false},
		{"second page: pagination needs a paid plan", 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "forbidden", http.StatusForbidden)
			}))
			defer srv.Close()

			_, err := NewWithBase(srv.URL, "k", 100).Page(t.Context(), "q", tt.page)
			require.Error(t, err)
			assert.Equal(t, tt.wantDenied, errors.Is(err, pipeline.ErrPaginationDenied))

			var se *httpx.StatusError
			require.ErrorAs(t, err, &se, "the HTTP status stays in the chain")
			assert.Equal(t, http.StatusForbidden, se.Status)
		})
	}
}

func TestPageFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := NewWithBase(srv.URL, "bad", 0).Page(t.Context(), "q", 0)
	require.ErrorContains(t, err, "401")
}
