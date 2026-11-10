package openalex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
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
	w := &work{}
	w.AbstractInvertedIndex = map[string][]int{
		"coffee":     {2},
		"improves":   {3},
		"everything": {4},
		"Nothing":    {1},
		"Background": {0},
	}

	const want = "Background Nothing coffee improves everything"

	require.Equal(t, want, w.abstract())
}

func TestAbstractEmpty(t *testing.T) {
	w := &work{}
	require.Empty(t, w.abstract())
}

// The domain record carries everything citation chaining and enrichment
// read; the paper conversion prefers the open-access URL over the PDF URL.
func TestToModelAndPaper(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"https://openalex.org/W1","doi":"https://doi.org/10.1/X",
			"display_name":"A study","publication_year":2021,"is_retracted":true,"cited_by_count":12,
			"open_access":{"is_oa":true,"oa_url":"https://oa.test/x"},
			"authorships":[{"author":{"display_name":"Ann"}},{"author":{"display_name":""}},{"author":{"display_name":"Bo"}}],
			"primary_location":{"source":{"display_name":"BMJ"},"pdf_url":"https://pdf.test/x.pdf"},
			"referenced_works":["https://openalex.org/W9"],
			"abstract_inverted_index":{"hello":[0],"world":[1]}}`))
	}))
	defer srv.Close()

	w, err := NewWithBase(srv.URL, "").ByDOI(context.Background(), "10.1/x")
	require.NoError(t, err)
	require.NotNil(t, w)

	assert.Equal(t, "https://openalex.org/W1", w.ID)
	assert.Equal(t, "A study", w.Title)
	assert.Equal(t, 2021, w.Year)
	assert.Equal(t, []string{"Ann", "Bo"}, w.Authors, "nameless authors are dropped")
	assert.Equal(t, "BMJ", w.Venue)
	assert.Equal(t, "hello world", w.Abstract)
	assert.True(t, w.Retracted)
	assert.Equal(t, []string{"https://openalex.org/W9"}, w.References)

	p := w.ToPaper("")
	assert.Equal(t, "10.1/x", p.DOI)
	assert.Equal(t, "https://oa.test/x", p.OALink, "the OA URL wins over the PDF URL")
	assert.Equal(t, 12, p.CitedByCount)

	w.OAURL = ""
	assert.Equal(t, "https://pdf.test/x.pdf", w.ToPaper("").OALink, "the PDF URL is the fallback")
}

// Every location is kept with its host type, so the full-text fetch can try
// the repository copies the primary (publisher) location hides; a location
// without any URL, a null source and an unfamiliar host type are handled.
func TestLocationsAreKept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"https://openalex.org/W1","doi":"https://doi.org/10.1/x","display_name":"A study",
			"open_access":{"is_oa":true,"oa_url":"https://www.ncbi.nlm.nih.gov/pmc/articles/7745181"},
			"primary_location":{"source":{"display_name":"NEJM","type":"journal"},
				"landing_page_url":"https://doi.org/10.1/x","pdf_url":null},
			"locations":[
				{"is_oa":false,"landing_page_url":"https://doi.org/10.1/x","pdf_url":null,
				 "source":{"type":"journal"}},
				{"is_oa":true,"landing_page_url":"https://www.ncbi.nlm.nih.gov/pmc/articles/7745181",
				 "pdf_url":"https://repo.test/x.pdf","source":{"type":"repository"}},
				{"is_oa":true,"landing_page_url":null,"pdf_url":null,"source":{"type":"repository"}},
				{"is_oa":true,"landing_page_url":"https://nosource.test/x","pdf_url":null,"source":null},
				{"is_oa":false,"landing_page_url":"https://meta.test/x","source":{"type":"metadata"}}
			]}`))
	}))
	defer srv.Close()

	w, err := NewWithBase(srv.URL, "").ByDOI(context.Background(), "10.1/x")
	require.NoError(t, err)
	require.NotNil(t, w)

	assert.Equal(t, []model.Location{
		{LandingURL: "https://doi.org/10.1/x", Host: model.HostPublisher},
		{
			LandingURL: "https://www.ncbi.nlm.nih.gov/pmc/articles/7745181", PDFURL: "https://repo.test/x.pdf",
			Host: model.HostRepository, OA: true,
		},
		{LandingURL: "https://nosource.test/x", Host: model.HostUnknown, OA: true},
		{LandingURL: "https://meta.test/x", Host: model.HostUnknown},
	}, w.Locations, "the empty location is dropped, the rest keep their order")
	assert.Equal(t, w.Locations, w.ToPaper("").OALocations, "the paper carries them on")
}

// An artifact written before locations existed still loads: the field is
// optional on the paper.
func TestPaperWithoutLocationsLoads(t *testing.T) {
	var p model.Paper

	require.NoError(t, json.Unmarshal([]byte(`{"doi":"10.1/x","title":"Old","oa_link":"https://oa.test/x"}`), &p))
	assert.Empty(t, p.OALocations)
	assert.Equal(t, "https://oa.test/x", p.OALink)
}

// 404 means "OpenAlex does not know the DOI": no work, no error.
func TestByDOIUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	w, err := NewWithBase(srv.URL, "").ByDOI(context.Background(), "10.1/none")
	require.NoError(t, err)
	require.Nil(t, w)
}

func TestByDOIServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := NewWithBase(srv.URL, "").ByDOI(context.Background(), "10.1/x")
	require.Error(t, err)
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
	require.Equal(t, 2016, w.Year)
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
