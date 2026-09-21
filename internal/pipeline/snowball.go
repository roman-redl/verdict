package pipeline

import (
	"context"
	"sort"

	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/openalex"
)

// SnowballCandidate is a corpus-expansion candidate found through citation
// chaining. A work connected to several seed papers at once (cited by them,
// or citing them) is rarely irrelevant — that shared count is the signal.
// Candidates are offered for curation; nothing is added automatically.
type SnowballCandidate struct {
	DOI          string `json:"doi"`
	Title        string `json:"title"`
	Year         int    `json:"year"`
	CitedBySeeds int    `json:"cited_by_seeds"` // how many seeds cite this work
	CitesSeeds   int    `json:"cites_seeds"`    // how many seeds this work cites
}

// Snowball expands a seed DOI list both directions along the citation graph:
// the seeds' reference lists (older foundational work) and the works citing
// the seeds (newer evidence). A candidate qualifies when either shared count
// reaches minShared; results are capped at maxOut, strongest signal first.
func Snowball(ctx context.Context, oa *openalex.Client, dois []string, minShared, maxOut int) ([]SnowballCandidate, error) {
	if minShared < 1 {
		minShared = 2
	}
	if maxOut < 1 {
		maxOut = 10
	}

	// Phase A: seed works (indexed — no shared state in the goroutines).
	works := make([]*openalex.Work, len(dois))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4)
	for i, doi := range dois {
		g.Go(func() error {
			w, err := oa.ByDOI(gctx, model.NormalizeDOI(doi))
			if err == nil {
				works[i] = w
			}
			return nil // an unknown seed is skipped, not fatal
		})
	}
	_ = g.Wait()

	seedDOIs := map[string]bool{}
	for i, doi := range dois {
		seedDOIs[model.NormalizeDOI(doi)] = true
		if works[i] == nil {
			continue
		}
		if d := model.NormalizeDOI(works[i].DOI); d != "" {
			seedDOIs[d] = true
		}
	}

	type tally struct {
		citedBySeeds, citesSeeds int
		work                     *openalex.Work
	}
	tallies := map[string]*tally{}
	add := func(w *openalex.Work, citedBy, cites int) {
		d := model.NormalizeDOI(w.DOI)
		if d == "" || seedDOIs[d] {
			return
		}
		t, ok := tallies[d]
		if !ok {
			t = &tally{}
			tallies[d] = t
		}
		t.citedBySeeds += citedBy
		t.citesSeeds += cites
		if t.work == nil {
			t.work = w
		}
	}

	// Phase B (forward): works citing each seed.
	citing := make([][]openalex.Work, len(works))
	g, gctx = errgroup.WithContext(ctx)
	g.SetLimit(4)
	for i := range works {
		if works[i] == nil || works[i].ID == "" {
			continue
		}
		g.Go(func() error {
			ws, err := oa.CitesWork(gctx, works[i].ID, 100)
			if err == nil {
				citing[i] = ws
			}
			return nil // best-effort per seed
		})
	}
	_ = g.Wait()
	for _, ws := range citing {
		seenInSeed := map[string]bool{}
		for j := range ws {
			d := model.NormalizeDOI(ws[j].DOI)
			if d == "" || seenInSeed[d] {
				continue // a work appears once per seed, not once per position
			}
			seenInSeed[d] = true
			add(&ws[j], 0, 1) // the work cites the seed
		}
	}

	// Phase C (backward): reference lists shared by several seeds.
	refCount := map[string]int{}
	for _, w := range works {
		if w == nil {
			continue
		}
		for _, id := range w.ReferencedWorks {
			refCount[id]++
		}
	}
	var sharedIDs []string
	for id, n := range refCount {
		if n >= minShared {
			sharedIDs = append(sharedIDs, id)
		}
	}
	if len(sharedIDs) > 0 {
		if ws, err := oa.ByIDs(ctx, sharedIDs); err == nil {
			for i := range ws {
				add(&ws[i], refCount[ws[i].ID], 0) // cited by that many seeds
			}
		}
	}

	var out []SnowballCandidate
	for d, t := range tallies {
		if t.citedBySeeds < minShared && t.citesSeeds < minShared {
			continue
		}
		c := SnowballCandidate{DOI: d, CitedBySeeds: t.citedBySeeds, CitesSeeds: t.citesSeeds}
		if t.work != nil {
			c.Title, c.Year = t.work.DisplayName, t.work.PublicationYear
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		si, sj := out[i].CitedBySeeds+out[i].CitesSeeds, out[j].CitedBySeeds+out[j].CitesSeeds
		if si != sj {
			return si > sj
		}
		return out[i].DOI < out[j].DOI
	})
	if len(out) > maxOut {
		out = out[:maxOut]
	}
	return out, nil
}
