package openalex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// The polite-pool address is a query value like any other: '+' and '&' in it
// must reach the server intact (the old string concatenation turned '+' into
// a space and let '&' start a new parameter).
func TestByDOIEscapesMailto(t *testing.T) {
	const email = "me+verdict&x@example.org"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, email, r.URL.Query().Get("mailto"))
		assert.Len(t, r.URL.Query(), 1, "nothing else may leak into the query")

		_, _ = w.Write([]byte(`{"doi":"https://doi.org/10.1/a","display_name":"A"}`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, email)

	w, err := c.ByDOI(context.Background(), "10.1/a")
	require.NoError(t, err)
	require.NotNil(t, w)
}

// Old-style DOIs carry brackets, colons, semicolons and angle brackets.
func TestByDOIKeepsAwkwardDOIIntact(t *testing.T) {
	const doi = "10.1002/(sici)1097-0258(19980430)17:8<849::aid-sim777>3.0.co;2-f"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/works/doi:"+doi, r.URL.Path)

		_, _ = w.Write([]byte(`{"doi":"https://doi.org/` + doi + `"}`))
	}))
	defer srv.Close()

	w, err := NewWithBase(srv.URL, "").ByDOI(context.Background(), doi)
	require.NoError(t, err)
	require.NotNil(t, w)
}

// Filters with '|' and ':' are values, so they are escaped like any other.
func TestByIDsAndCitesWorkQueries(t *testing.T) {
	var got []url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Query())

		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, "a@b.org")

	_, err := c.ByIDs(context.Background(), []string{"W1", "W2"})
	require.NoError(t, err)

	_, err = c.CitesWork(context.Background(), "W9", 5)
	require.NoError(t, err)

	require.Len(t, got, 2)
	assert.Equal(t, "ids.openalex:W1|W2", got[0].Get("filter"))
	assert.Equal(t, "50", got[0].Get("per-page"))
	assert.Equal(t, "a@b.org", got[0].Get("mailto"))
	assert.Equal(t, "cites:W9", got[1].Get("filter"))
	assert.Equal(t, "cited_by_count:desc", got[1].Get("sort"))
	assert.Equal(t, "5", got[1].Get("per-page"))
}
