package consensus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/httpx"
)

// studyTypeMetaAnalysis — повторяющееся значение фикстур (goconst).
const studyTypeMetaAnalysis = "meta-analysis"

// SearchPage must send the API key header and encode every set filter,
// omitting the zero-valued ones entirely.
func TestSearchPageSendsKeyAndParams(t *testing.T) {
	var gotQuery url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"), "x-api-key header")

		assert.Equal(t, "/v1/search", r.URL.Path)

		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`{"results":[],"page":0,"page_size":20,"is_end":true}`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL+"/v1", "test-key")

	_, err := c.SearchPage(context.Background(), SearchParams{
		Query: "does creatine improve cognition?", Page: 2, PageSize: 50,
		YearMin: 2015, YearMax: 2024, StudyTypes: []string{"rct", studyTypeMetaAnalysis},
		Human: true, SampleSizeMin: 30, ExcludePreprints: true,
	})
	require.NoError(t, err, "SearchPage")

	want := map[string]string{
		"query": "does creatine improve cognition?", "page": "2", "page_size": "50",
		"year_min": "2015", "year_max": "2024", "study_types": "rct,meta-analysis",
		"human": "true", "sample_size_min": "30", "exclude_preprints": "true",
	}
	for k, v := range want {
		assert.Equal(t, v, gotQuery.Get(k), "param %s", k)
	}

	// Zero values must leave the filters out of the URL entirely.
	gotQuery = url.Values{}

	_, err = c.SearchPage(context.Background(), SearchParams{Query: "vitamin d"})
	require.NoError(t, err, "SearchPage (bare)")

	for _, k := range []string{
		"page", "page_size", "year_min", "year_max",
		"study_types", "human", "sample_size_min", "exclude_preprints",
	} {
		assert.NotContains(t, gotQuery, k, "zero-valued param must be absent")
	}
}

// SearchPage must decode the envelope: results plus the applied page_size,
// is_end and next_page the page-walker relies on.
func TestSearchPageDecodesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"results": [{
				"title": "A trial", "authors": ["Doe, J."], "doi": "10.1/x",
				"abstract": "abs", "journal_name": "Lancet", "publish_year": 2023,
				"url": "https://consensus.app/papers/x", "citation_count": 7,
				"study_type": "rct", "sample_size": 120, "is_preprint": false,
				"sjr_best_quartile": "Q1", "takeaway": "it works"
			}],
			"page": 0, "page_size": 20, "is_end": false, "next_page": 1
		}`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, "test-key")

	pg, err := c.SearchPage(context.Background(), SearchParams{Query: "q"})
	require.NoError(t, err, "SearchPage")

	require.Len(t, pg.Results, 1)
	require.Equal(t, "A trial", pg.Results[0].Title)

	assert.False(t, pg.IsEnd)
	assert.Equal(t, 1, pg.NextPage)
	assert.Equal(t, 20, pg.PageSize)
}

// ToPaper must map the fields the pipeline consumes and normalize the DOI;
// OALink must stay empty (their url is not an open-access PDF).
func TestToPaper(t *testing.T) {
	r := QueryResult{
		Title: "T", Authors: []string{"A"}, DOI: "https://doi.org/10.1234/AB.CD",
		Abstract: "abs", JournalName: "BMJ", PublishYear: 2020,
		CitationCount: 42, StudyType: studyTypeMetaAnalysis, SampleSize: 900,
		SJRQuartile: "Q1", Takeaway: "helps a bit", URL: "https://consensus.app/papers/t",
	}

	p := r.ToPaper()
	assert.Equal(t, "10.1234/ab.cd", p.DOI, "DOI must normalize")

	assert.Equal(t, "consensus", p.Source)
	assert.Equal(t, studyTypeMetaAnalysis, p.StudyDesign)
	assert.Equal(t, 900, p.SampleSize)
	assert.Equal(t, "BMJ", p.Venue)
	assert.Equal(t, 42, p.CitedByCount)
	assert.Equal(t, 2020, p.Year)
	assert.Equal(t, "Q1", p.SJRQuartile)
	assert.Equal(t, "helps a bit", p.Takeaway)

	assert.Empty(t, p.OALink, "their url is not an open-access PDF")
}

// Quartile must accept both wire forms ("Q1" or 1) and null.
func TestQuartileUnmarshal(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Quartile
	}{
		{`"Q2"`, "Q2"},
		{`1`, "Q1"},
		{`"3"`, "Q3"},
		{`null`, ""},
	} {
		var q Quartile
		require.NoError(t, q.UnmarshalJSON([]byte(tc.in)), "unmarshal %s", tc.in)

		assert.Equal(t, tc.want, q, "unmarshal %s", tc.in)
	}
}

// Auth and validation failures must surface as *httpx.StatusError, not be
// swallowed — the pipeline refuses to fall back to keyword search silently.
func TestSearchPageErrorStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, 422} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"detail":[{"msg":"nope"}]}`))
		}))
		c := NewWithBase(srv.URL, "test-key")
		_, err := c.SearchPage(context.Background(), SearchParams{Query: "q"})

		srv.Close()

		require.Error(t, err, "status %d: expected an error", status)

		var se *httpx.StatusError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, status, se.Status, "status %d: want *httpx.StatusError", status)
	}
}
