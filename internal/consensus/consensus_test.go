package consensus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/roman-redl/verdict/internal/httpx"
)

// studyTypeMetaAnalysis — повторяющееся значение фикстур (goconst).
const studyTypeMetaAnalysis = "meta-analysis"

// SearchPage must send the API key header and encode every set filter,
// omitting the zero-valued ones entirely.
func TestSearchPageSendsKeyAndParams(t *testing.T) {
	var gotQuery url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key header: got %q, want %q", r.Header.Get("x-api-key"), "test-key")
		}

		if r.URL.Path != "/v1/search" {
			t.Errorf("path: got %q, want /v1/search", r.URL.Path)
		}

		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`{"results":[],"page":0,"page_size":20,"is_end":true}`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL+"/v1", "test-key")

	if _, err := c.SearchPage(context.Background(), SearchParams{
		Query: "does creatine improve cognition?", Page: 2, PageSize: 50,
		YearMin: 2015, YearMax: 2024, StudyTypes: []string{"rct", studyTypeMetaAnalysis},
		Human: true, SampleSizeMin: 30, ExcludePreprints: true,
	}); err != nil {
		t.Fatalf("SearchPage: %v", err)
	}

	want := map[string]string{
		"query": "does creatine improve cognition?", "page": "2", "page_size": "50",
		"year_min": "2015", "year_max": "2024", "study_types": "rct,meta-analysis",
		"human": "true", "sample_size_min": "30", "exclude_preprints": "true",
	}
	for k, v := range want {
		if got := gotQuery.Get(k); got != v {
			t.Errorf("param %s: got %q, want %q", k, got, v)
		}
	}

	// Zero values must leave the filters out of the URL entirely.
	gotQuery = url.Values{}

	if _, err := c.SearchPage(context.Background(), SearchParams{Query: "vitamin d"}); err != nil {
		t.Fatalf("SearchPage (bare): %v", err)
	}

	for _, k := range []string{
		"page", "page_size", "year_min", "year_max",
		"study_types", "human", "sample_size_min", "exclude_preprints",
	} {
		if _, ok := gotQuery[k]; ok {
			t.Errorf("zero-valued param %s should be absent, got %q", k, gotQuery[k])
		}
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
	if err != nil {
		t.Fatalf("SearchPage: %v", err)
	}

	if len(pg.Results) != 1 || pg.Results[0].Title != "A trial" {
		t.Fatalf("results: got %+v", pg.Results)
	}

	if pg.IsEnd || pg.NextPage != 1 || pg.PageSize != 20 {
		t.Errorf("envelope: got is_end=%v next_page=%d page_size=%d, want false/1/20",
			pg.IsEnd, pg.NextPage, pg.PageSize)
	}
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
	if p.DOI != "10.1234/ab.cd" {
		t.Errorf("doi: got %q, want normalized 10.1234/ab.cd", p.DOI)
	}

	if p.Source != "consensus" || p.StudyDesign != studyTypeMetaAnalysis || p.SampleSize != 900 ||
		p.Venue != "BMJ" || p.CitedByCount != 42 || p.Year != 2020 ||
		p.SJRQuartile != "Q1" || p.Takeaway != "helps a bit" {
		t.Errorf("mapping: got %+v", p)
	}

	if p.OALink != "" {
		t.Errorf("oa_link must stay empty, got %q", p.OALink)
	}
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
		if err := q.UnmarshalJSON([]byte(tc.in)); err != nil {
			t.Fatalf("unmarshal %s: %v", tc.in, err)
		}

		if q != tc.want {
			t.Errorf("unmarshal %s: got %q, want %q", tc.in, q, tc.want)
		}
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

		if err == nil {
			t.Fatalf("status %d: expected an error", status)
		}

		var se *httpx.StatusError
		if !errors.As(err, &se) || se.Status != status {
			t.Errorf("status %d: got %v, want *httpx.StatusError(%d)", status, err, status)
		}
	}
}
