package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/roman-redl/verdict/internal/openalex"
)

func TestSnowball(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/works/doi:"):
			switch strings.TrimPrefix(r.URL.Path, "/works/doi:") {
			case "10.1000/seed1":
				_, _ = w.Write([]byte(`{"id":"W1","doi":"https://doi.org/10.1000/seed1","display_name":"Seed one","referenced_works":["W9","W10"]}`))
			default:
				_, _ = w.Write([]byte(`{"id":"W2","doi":"https://doi.org/10.1000/seed2","display_name":"Seed two","referenced_works":["W9"]}`))
			}
		case strings.Contains(r.URL.Query().Get("filter"), "cites:W1"):
			_, _ = w.Write([]byte(`{"results":[{"id":"W5","doi":"https://doi.org/10.1000/cand1","display_name":"Shared candidate"}]}`))
		case strings.Contains(r.URL.Query().Get("filter"), "cites:W2"):
			_, _ = w.Write([]byte(`{"results":[
				{"id":"W5","doi":"https://doi.org/10.1000/cand1","display_name":"Shared candidate"},
				{"id":"W6","doi":"https://doi.org/10.1000/one-side","display_name":"Cites seed two only"}]}`))
		case strings.Contains(r.URL.Query().Get("filter"), "ids.openalex:"):
			_, _ = w.Write([]byte(`{"results":[{"id":"W9","doi":"https://doi.org/10.1000/classic","display_name":"The classic"}]}`))
		default:
			t.Errorf("unexpected request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer srv.Close()

	cands, err := Snowball(context.Background(), openalex.NewWithBase(srv.URL, ""),
		[]string{"10.1000/seed1", "10.1000/seed2"}, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	byDOI := map[string]SnowballCandidate{}
	for _, c := range cands {
		byDOI[c.DOI] = c
	}
	// cites both seeds (appears in both citing lists) — the forward signal.
	if c := byDOI["10.1000/cand1"]; c.CitesSeeds != 2 || c.Title != "Shared candidate" {
		t.Errorf("shared candidate: %+v", c)
	}
	// referenced by both seeds — the backward signal.
	if c := byDOI["10.1000/classic"]; c.CitedBySeeds != 2 {
		t.Errorf("classic: %+v", c)
	}
	// connected to one seed only — filtered out by min-shared=2.
	if _, ok := byDOI["10.1000/one-side"]; ok {
		t.Errorf("one-side candidate must be filtered: %+v", byDOI["10.1000/one-side"])
	}
	// seeds themselves must never be candidates.
	if _, ok := byDOI["10.1000/seed1"]; ok {
		t.Errorf("seed listed as candidate")
	}
}
