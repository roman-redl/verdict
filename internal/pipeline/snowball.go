package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/internal/model"
)

// snowballParallelism bounds the concurrent OpenAlex lookups of one phase.
const snowballParallelism = 4

// citingWorksLimit is how many citing works are fetched per seed.
const citingWorksLimit = 100

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
func Snowball(
	ctx context.Context, works Works, dois []string, minShared, maxOut int, log *slog.Logger,
) ([]SnowballCandidate, error) {
	if minShared < 1 {
		minShared = 2
	}

	if maxOut < 1 {
		maxOut = 10
	}

	seeds, err := lookupSeeds(ctx, works, dois, log)
	if err != nil {
		return nil, err
	}

	set := newCandidateSet(dois, seeds)

	citing, err := lookupCiting(ctx, works, seeds, log)
	if err != nil {
		return nil, err
	}

	set.addCiting(citing)
	set.addReferenced(ctx, works, seeds, minShared, log)

	return set.rank(minShared, maxOut), nil
}

// lookupSeeds resolves the seed DOIs, index-aligned with dois; a seed
// OpenAlex does not know (or fails on) is nil and simply skipped.
func lookupSeeds(ctx context.Context, works Works, dois []string, log *slog.Logger) ([]*model.Work, error) {
	seeds := make([]*model.Work, len(dois))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(snowballParallelism)

	for i, doi := range dois {
		g.Go(func() error {
			w, err := works.ByDOI(gctx, model.NormalizeDOI(doi))
			if err != nil {
				log.Warn("snowball: seed lookup failed, the seed is skipped", "doi", doi, "err", err)
				return nil
			}

			seeds[i] = w // nil for a DOI OpenAlex does not know

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("looking up the seed papers: %w", err)
	}

	return seeds, nil
}

// lookupCiting fetches the works citing each seed (the forward half); a seed
// whose lookup fails adds no forward links.
func lookupCiting(ctx context.Context, works Works, seeds []*model.Work, log *slog.Logger) ([][]model.Work, error) {
	citing := make([][]model.Work, len(seeds))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(snowballParallelism)

	for i := range seeds {
		if seeds[i] == nil || seeds[i].ID == "" {
			continue
		}

		g.Go(func() error {
			ws, err := works.CitesWork(gctx, seeds[i].ID, citingWorksLimit)
			if err != nil {
				log.Warn("snowball: citing-works lookup failed, the seed adds no forward links",
					"doi", seeds[i].DOI, "err", err)

				return nil // best-effort per seed
			}

			citing[i] = ws

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("looking up the citing works: %w", err)
	}

	return citing, nil
}

// candidateTally counts how many seeds a candidate connects to.
type candidateTally struct {
	citedBySeeds, citesSeeds int
	work                     *model.Work
}

// candidateSet accumulates the candidates of all phases, keyed by DOI.
type candidateSet struct {
	seedDOIs map[string]bool
	byDOI    map[string]*candidateTally
}

func newCandidateSet(dois []string, seeds []*model.Work) *candidateSet {
	seedDOIs := map[string]bool{}

	for i, doi := range dois {
		seedDOIs[model.NormalizeDOI(doi)] = true

		if seeds[i] == nil {
			continue
		}

		if d := model.NormalizeDOI(seeds[i].DOI); d != "" {
			seedDOIs[d] = true
		}
	}

	return &candidateSet{seedDOIs: seedDOIs, byDOI: map[string]*candidateTally{}}
}

// add counts a connection; seeds themselves and works without a DOI are not
// candidates.
func (s *candidateSet) add(w *model.Work, citedBy, cites int) {
	d := model.NormalizeDOI(w.DOI)
	if d == "" || s.seedDOIs[d] {
		return
	}

	t, ok := s.byDOI[d]
	if !ok {
		t = &candidateTally{}
		s.byDOI[d] = t
	}

	t.citedBySeeds += citedBy

	t.citesSeeds += cites
	if t.work == nil {
		t.work = w
	}
}

// addCiting counts the forward links: a work appears once per seed, not once
// per position in the seed's list.
func (s *candidateSet) addCiting(citing [][]model.Work) {
	for _, ws := range citing {
		seenInSeed := map[string]bool{}

		for j := range ws {
			d := model.NormalizeDOI(ws[j].DOI)
			if d == "" || seenInSeed[d] {
				continue
			}

			seenInSeed[d] = true

			s.add(&ws[j], 0, 1) // the work cites the seed
		}
	}
}

// addReferenced counts the backward links: works in the reference lists of at
// least minShared seeds. A failed lookup leaves the backward half out.
func (s *candidateSet) addReferenced(
	ctx context.Context, works Works, seeds []*model.Work, minShared int, log *slog.Logger,
) {
	refCount := map[string]int{}

	for _, w := range seeds {
		if w == nil {
			continue
		}

		for _, id := range w.References {
			refCount[id]++
		}
	}

	var sharedIDs []string

	for id, n := range refCount {
		if n >= minShared {
			sharedIDs = append(sharedIDs, id)
		}
	}

	if len(sharedIDs) == 0 {
		return
	}

	ws, err := works.ByIDs(ctx, sharedIDs)
	if err != nil {
		log.Warn("snowball: reference lookup failed, the backward half is missing", "err", err)
	}

	for i := range ws {
		s.add(&ws[i], refCount[ws[i].ID], 0) // cited by that many seeds
	}
}

// rank keeps the candidates that reach minShared in either direction,
// strongest signal first, at most maxOut.
func (s *candidateSet) rank(minShared, maxOut int) []SnowballCandidate {
	var out []SnowballCandidate

	for d, t := range s.byDOI {
		if t.citedBySeeds < minShared && t.citesSeeds < minShared {
			continue
		}

		c := SnowballCandidate{DOI: d, CitedBySeeds: t.citedBySeeds, CitesSeeds: t.citesSeeds}
		if t.work != nil {
			c.Title, c.Year = t.work.Title, t.work.Year
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

	return out
}
