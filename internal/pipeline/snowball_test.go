package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/openalex"
)

func TestSnowball(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/works/doi:"):
			switch strings.TrimPrefix(r.URL.Path, "/works/doi:") {
			case "10.1000/seed1":
				_, _ = w.Write([]byte(`{"id":"W1","doi":"https://doi.org/10.1000/seed1",` +
					`"display_name":"Seed one","referenced_works":["W9","W10"]}`))
			default:
				_, _ = w.Write([]byte(`{"id":"W2","doi":"https://doi.org/10.1000/seed2",` +
					`"display_name":"Seed two","referenced_works":["W9"]}`))
			}
		case strings.Contains(r.URL.Query().Get("filter"), "cites:W1"):
			_, _ = w.Write([]byte(`{"results":[{"id":"W5","doi":"https://doi.org/10.1000/cand1",` +
				`"display_name":"Shared candidate"}]}`))
		case strings.Contains(r.URL.Query().Get("filter"), "cites:W2"):
			_, _ = w.Write([]byte(`{"results":[
				{"id":"W5","doi":"https://doi.org/10.1000/cand1","display_name":"Shared candidate"},
				{"id":"W6","doi":"https://doi.org/10.1000/one-side","display_name":"Cites seed two only"}]}`))
		case strings.Contains(r.URL.Query().Get("filter"), "ids.openalex:"):
			_, _ = w.Write([]byte(`{"results":[{"id":"W9","doi":"https://doi.org/10.1000/classic",` +
				`"display_name":"The classic"}]}`))
		default:
			assert.Fail(t, "unexpected request", "%s?%s", r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer srv.Close()

	cands, err := Snowball(context.Background(), openalex.NewWithBase(srv.URL, ""),
		[]string{"10.1000/seed1", "10.1000/seed2"}, 2, 10)
	require.NoError(t, err)

	byDOI := map[string]SnowballCandidate{}
	for _, c := range cands {
		byDOI[c.DOI] = c
	}
	// cites both seeds (appears in both citing lists) — the forward signal.
	shared := byDOI["10.1000/cand1"]
	assert.Equal(t, 2, shared.CitesSeeds)
	assert.Equal(t, "Shared candidate", shared.Title)
	// referenced by both seeds — the backward signal.
	assert.Equal(t, 2, byDOI["10.1000/classic"].CitedBySeeds)
	// connected to one seed only — filtered out by min-shared=2.
	assert.NotContains(t, byDOI, "10.1000/one-side", "one-side candidate must be filtered")
	// seeds themselves must never be candidates.
	assert.NotContains(t, byDOI, "10.1000/seed1", "seed listed as candidate")
}
