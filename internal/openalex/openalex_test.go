package openalex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Search must map results into Paper, filter out retractions, and strip
// wildcard characters (* and ?) that OpenAlex rejects in stemmed search.
func TestSearchFiltersRetracted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("search"); got != "does coffee work" {
			t.Errorf("search parameter: got %q, want %q (wildcards stripped)", got, "does coffee work")
		}
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
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) != 1 {
		t.Fatalf("want 1 paper (retraction filtered out), got %d", len(papers))
	}
	if papers[0].DOI != "10.1/ok" || papers[0].Title != "Good study" || papers[0].Abstract != "coffee works" {
		t.Errorf("bad mapping: %+v", papers[0])
	}
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
	if got := w.Abstract(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAbstractEmpty(t *testing.T) {
	w := &Work{}
	if got := w.Abstract(); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// SearchTitle resolves a DOI-less title through filter=title.search.
func TestSearchTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("filter"); got != `title.search:"Phototherapy for atopic dermatitis"` {
			t.Errorf("filter: got %q", got)
		}
		_, _ = w.Write([]byte(`{"results":[{"doi":"https://doi.org/10.1/match","display_name":"Phototherapy for atopic dermatitis","publication_year":2016}]}`))
	}))
	defer srv.Close()
	c := New("")
	c.base = srv.URL

	w, err := c.SearchTitle(context.Background(), "Phototherapy for atopic dermatitis")
	if err != nil {
		t.Fatal(err)
	}
	if w == nil || w.DOI != "https://doi.org/10.1/match" || w.PublicationYear != 2016 {
		t.Fatalf("bad match: %+v", w)
	}
}

func TestSearchTitleNoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	c := New("")
	c.base = srv.URL

	w, err := c.SearchTitle(context.Background(), "No such paper anywhere")
	if err != nil {
		t.Fatal(err)
	}
	if w != nil {
		t.Fatalf("want nil for no match, got %+v", w)
	}
}

// Titles containing commas must arrive quoted: OpenAlex 400s on %2C commas.
func TestSearchTitleComma(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("filter"); got != `title.search:"Laser, sham, trial"` {
			t.Errorf("filter: got %q", got)
		}
		_, _ = w.Write([]byte(`{"results":[{"doi":"https://doi.org/10.1/comma","display_name":"Laser, sham, trial","publication_year":2020}]}`))
	}))
	defer srv.Close()
	c := New("")
	c.base = srv.URL

	w, err := c.SearchTitle(context.Background(), "Laser, sham, trial")
	if err != nil {
		t.Fatal(err)
	}
	if w == nil || w.DOI != "https://doi.org/10.1/comma" {
		t.Fatalf("bad match: %+v", w)
	}
}
