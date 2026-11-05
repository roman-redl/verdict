package pipeline

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	candShared  = "10.1000/cand1"
	candClassic = "10.1000/classic"
)

// snowballWorks scripts a small citation graph: two seeds, a work citing
// both, a work citing one, and a classic both seeds reference.
func snowballWorks() *fakeWorks {
	return &fakeWorks{
		byDOI: func(doi string) (*model.Work, error) {
			switch doi {
			case seed1:
				return &model.Work{
					ID: "W1", DOI: "https://doi.org/" + seed1, Title: "Seed one",
					References: []string{"W9", "W10"},
				}, nil
			case seed2:
				return &model.Work{
					ID: "W2", DOI: "https://doi.org/" + seed2, Title: "Seed two",
					References: []string{"W9"},
				}, nil
			}

			return nil, nil // the port's "no data" answer
		},
		cites: func(id string, _ int) ([]model.Work, error) {
			shared := model.Work{ID: "W5", DOI: "https://doi.org/" + candShared, Title: "Shared candidate"}

			switch id {
			case "W1":
				return []model.Work{shared}, nil
			case "W2":
				return []model.Work{
					shared,
					{ID: "W6", DOI: "https://doi.org/10.1000/one-side", Title: "Cites seed two only"},
				}, nil
			}

			return nil, errors.New("unexpected work " + id)
		},
		byIDs: func(ids []string) ([]model.Work, error) {
			var out []model.Work

			for _, id := range ids { // W10 is unknown to the fake: absent from the answer
				if id == "W9" {
					out = append(out, model.Work{ID: "W9", DOI: "https://doi.org/" + candClassic, Title: "The classic"})
				}
			}

			return out, nil
		},
	}
}

func TestSnowball(t *testing.T) {
	cands, err := Snowball(t.Context(), snowballWorks(), []string{seed1, seed2}, 2, 10, quietLog())
	require.NoError(t, err)

	byDOI := map[string]SnowballCandidate{}
	for _, c := range cands {
		byDOI[c.DOI] = c
	}
	// cites both seeds (appears in both citing lists) — the forward signal.
	shared := byDOI[candShared]
	assert.Equal(t, 2, shared.CitesSeeds)
	assert.Equal(t, "Shared candidate", shared.Title)
	// referenced by both seeds — the backward signal.
	assert.Equal(t, 2, byDOI[candClassic].CitedBySeeds)
	// connected to one seed only — filtered out by min-shared=2.
	assert.NotContains(t, byDOI, "10.1000/one-side", "one-side candidate must be filtered")
	// seeds themselves must never be candidates.
	assert.NotContains(t, byDOI, seed1, "seed listed as candidate")
}

// The strongest signal comes first, ties break by DOI, the list is capped.
func TestSnowballRankingAndCap(t *testing.T) {
	cands, err := Snowball(t.Context(), snowballWorks(), []string{seed1, seed2}, 1, 2, quietLog())
	require.NoError(t, err)

	require.Len(t, cands, 2, "capped at maxOut")
	// minShared 1 lets everything in; the two-seed candidates lead, ordered by DOI
	assert.Equal(t, candShared, cands[0].DOI)
	assert.Equal(t, candClassic, cands[1].DOI)
}

// A failing lookup degrades the result instead of failing the command.
func TestSnowballSurvivesLookupFailures(t *testing.T) {
	works := snowballWorks()
	works.cites = func(string, int) ([]model.Work, error) { return nil, errors.New("openalex down") }

	cands, err := Snowball(t.Context(), works, []string{seed1, seed2, "10.1000/unknown"}, 2, 10, quietLog())
	require.NoError(t, err)

	require.Len(t, cands, 1, "only the backward half survives")
	assert.Equal(t, candClassic, cands[0].DOI)

	works.byIDs = func([]string) ([]model.Work, error) { return nil, errors.New("openalex down") }

	cands, err = Snowball(t.Context(), works, []string{seed1, seed2}, 2, 10, quietLog())
	require.NoError(t, err)
	assert.Empty(t, cands)
}

func TestSnowballDefaults(t *testing.T) {
	cands, err := Snowball(t.Context(), snowballWorks(), []string{seed1, seed2}, 0, 0, quietLog())
	require.NoError(t, err)
	assert.Len(t, cands, 2, "defaults: min-shared 2, up to 10 candidates")
}
