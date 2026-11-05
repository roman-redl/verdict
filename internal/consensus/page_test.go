package consensus

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// Page is the pipeline's view of a search: papers with the DOIs normalized,
// plus whether the ranking ended.
func TestPageMapsResultsToPapers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "3", r.URL.Query().Get("page"))
		assert.Equal(t, "creatine", r.URL.Query().Get("query"))

		_, _ = w.Write([]byte(`{"results":[
			{"title":"A","doi":"https://doi.org/10.1/A","study_type":"rct","publish_year":2022},
			{"title":"B","doi":"","publish_year":2020}
		],"page_size":20,"is_end":true}`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, "k")
	require.True(t, c.HasKey())
	require.False(t, New("").HasKey())

	got, err := c.Page(t.Context(), "creatine", 3)
	require.NoError(t, err)

	assert.True(t, got.IsEnd)
	require.Len(t, got.Papers, 2)
	assert.Equal(t, "10.1/a", got.Papers[0].DOI)
	assert.Equal(t, model.SourceConsensus, got.Papers[0].Source)
	assert.Equal(t, "rct", got.Papers[0].StudyDesign)
	assert.Empty(t, got.Papers[1].DOI, "a result without a DOI is returned as is; dropping it is the pipeline's call")
}

func TestPageFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := NewWithBase(srv.URL, "bad").Page(t.Context(), "q", 0)
	require.ErrorContains(t, err, "401")
}
