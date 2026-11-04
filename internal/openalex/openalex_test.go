package openalex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Search must map results into Paper, filter out retractions, and strip
// wildcard characters (* and ?) that OpenAlex rejects in stemmed search.
func TestSearchFiltersRetracted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "does coffee work", r.URL.Query().Get("search"), "wildcards must be stripped")

		_, _ = w.Write([]byte(`{"results":[
			{"doi":"https://doi.org/10.1/ok","display_name":"Good study","publication_year":2024,
			 "abstract_inverted_index":{"coffee":[0],"works":[1]}},
			{"doi":"https://doi.org/10.1/bad","display_name":"Retracted study","is_retracted":true}
		]}`))
	}))
	defer srv.Close()

	c := New("")
	c.base = srv.URL

	papers, err := c.Search(context.Background(), "does coffee work?", 15)
	require.NoError(t, err)

	require.Len(t, papers, 1, "retraction filtered out")

	assert.Equal(t, "10.1/ok", papers[0].DOI)
	assert.Equal(t, "Good study", papers[0].Title)
	assert.Equal(t, "coffee works", papers[0].Abstract)
}

// Abstract must reconstruct text from the inverted index by word positions.
func TestAbstractReinversion(t *testing.T) {
	w := &Work{}
	w.AbstractInvertedIndex = map[string][]int{
		"coffee":     {2},
		"improves":   {3},
		"everything": {4},
		"Nothing":    {1},
		"Background": {0},
	}

	const want = "Background Nothing coffee improves everything"

	require.Equal(t, want, w.Abstract())
}

func TestAbstractEmpty(t *testing.T) {
	w := &Work{}
	require.Empty(t, w.Abstract())
}

// SearchTitle resolves a DOI-less title through filter=title.search.
func TestSearchTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, `title.search:"Phototherapy for atopic dermatitis"`, r.URL.Query().Get("filter"))

		_, _ = w.Write([]byte(`{"results":[{"doi":"https://doi.org/10.1/match",` +
			`"display_name":"Phototherapy for atopic dermatitis","publication_year":2016}]}`))
	}))
	defer srv.Close()

	c := New("")
	c.base = srv.URL

	w, err := c.SearchTitle(context.Background(), "Phototherapy for atopic dermatitis")
	require.NoError(t, err)

	require.NotNil(t, w)
	require.Equal(t, "https://doi.org/10.1/match", w.DOI)
	require.Equal(t, 2016, w.PublicationYear)
}

func TestSearchTitleNoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := New("")
	c.base = srv.URL

	w, err := c.SearchTitle(context.Background(), "No such paper anywhere")
	require.NoError(t, err)

	require.Nil(t, w, "want nil for no match")
}

// Titles containing commas must arrive quoted: OpenAlex 400s on %2C commas.
func TestSearchTitleComma(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, `title.search:"Laser, sham, trial"`, r.URL.Query().Get("filter"))

		_, _ = w.Write([]byte(`{"results":[{"doi":"https://doi.org/10.1/comma",` +
			`"display_name":"Laser, sham, trial","publication_year":2020}]}`))
	}))
	defer srv.Close()

	c := New("")
	c.base = srv.URL

	w, err := c.SearchTitle(context.Background(), "Laser, sham, trial")
	require.NoError(t, err)

	require.NotNil(t, w)
	require.Equal(t, "https://doi.org/10.1/comma", w.DOI)
}
