package pipeline

// Review reference mining: a systematic review or meta-analysis in the pool
// lists the trials of its field, including ones no search ranks high. Their
// reference lists are read through OpenAlex (free) and the referenced works
// with a DOI join the pool as candidates; the screening then judges them.

import (
	"context"
	"fmt"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	// maxMinedReviews bounds the reviews whose reference lists are read,
	// strongest Consensus rank first.
	maxMinedReviews = 8
	// maxRefsPerReview bounds the references taken from one review.
	maxRefsPerReview = 150
	// maxMinedCandidates bounds the references that enter the pool: each is
	// screened, so the screening cost (a call per screenBatchSize titles) is
	// bounded by it. Those cited by the most reviews are kept.
	maxMinedCandidates = 300
)

// mineReviewRefs adds the references of the pool's reviews to the pool. It
// needs the metadata source and the screening backend: unscreened references
// would be hundreds of unjudged papers, so without screening nothing is mined —
// unless the screening is off on purpose (noScreen: every candidate counts as
// included, the channel audit wants to see what the references add).
// An OpenAlex failure on one review is a warning, not a failure. An excluded
// DOI (excl, may be nil) is not taken from a reference list either.
func (p *Pipeline) mineReviewRefs(ctx context.Context, info *DiscoveryInfo, noScreen bool, excl *doiExclusion) {
	if p.clients.Works == nil || (p.clients.LLMPlan == nil && !noScreen) {
		p.log.Info("review reference mining skipped: it needs OpenAlex and the screening model")
		return
	}

	reviews := pickReviews(info.Pool, maxMinedReviews)
	if len(reviews) == 0 {
		return
	}

	refs := p.fetchReferences(ctx, info.Pool, reviews)

	for _, r := range refs {
		if r.ok {
			info.ReviewsMined++
		}
	}

	pool, added := mergeReferences(info.Pool, refs, excl)
	info.Pool = pool
	info.RefsAdded = added
	p.log.Info("review references mined", "reviews", info.ReviewsMined, "of", len(reviews), "added", added)
}

// pickReviews returns the pool indexes of the systematic reviews and
// meta-analyses with a DOI (by the discovery tag, Consensus's or PubMed's, or
// the title — there is no critique yet), best ranked first, at most limit.
func pickReviews(pool []Candidate, limit int) []int {
	var idx []int

	for i := range pool {
		if pool[i].isReview() {
			idx = append(idx, i)
		}
	}

	slices.SortStableFunc(idx, func(a, b int) int { return betterRanked(&pool[a], &pool[b]) })

	return idx[:min(len(idx), limit)]
}

// isReview reports whether the candidate is a systematic review or
// meta-analysis with a DOI, by the same pre-critique design as the mining.
func (c *Candidate) isReview() bool {
	if c.DOI == "" {
		return false
	}

	d := c.design()

	return d == model.DesignSystematicReview || d == model.DesignMetaAnalysis
}

// countReviews is the number of reviews in the pool that the mining could read.
func countReviews(pool []Candidate) int {
	n := 0

	for i := range pool {
		if pool[i].isReview() {
			n++
		}
	}

	return n
}

// reviewRefs is the outcome of reading one review's reference list.
type reviewRefs struct {
	works []model.Work
	ok    bool // the review was found and its references were resolved
}

// fetchReferences reads the reference lists of the reviews in parallel, the
// results index-aligned with reviews. A failing review adds nothing.
func (p *Pipeline) fetchReferences(ctx context.Context, pool []Candidate, reviews []int) []reviewRefs {
	out := make([]reviewRefs, len(reviews))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(snowballParallelism)

	for i, at := range reviews {
		g.Go(func() error {
			works, err := p.reviewReferences(gctx, pool[at].DOI)
			if err != nil {
				p.log.Warn("review reference mining failed, the review adds no candidates",
					"doi", pool[at].DOI, "err", err)

				return nil // best-effort per review
			}

			out[i] = reviewRefs{works: works, ok: true}

			return nil
		})
	}

	// the goroutines swallow their errors (best-effort per review), so Wait
	// has nothing to report; a "can't happen" error is still logged
	if err := g.Wait(); err != nil {
		p.log.Error("review reference mining returned an unexpected error", "err", err)
	}

	return out
}

// reviewReferences resolves the first maxRefsPerReview references of a review
// to works. A review OpenAlex does not know has none.
func (p *Pipeline) reviewReferences(ctx context.Context, doi string) ([]model.Work, error) {
	w, err := p.clients.Works.ByDOI(ctx, doi)
	if err != nil {
		return nil, fmt.Errorf("looking up the review: %w", err)
	}

	if w == nil || len(w.References) == 0 {
		return nil, nil
	}

	works, err := p.clients.Works.ByIDs(ctx, w.References[:min(len(w.References), maxRefsPerReview)])
	if err != nil {
		return nil, fmt.Errorf("resolving the references of the review: %w", err)
	}

	return works, nil
}

// mined is a referenced work on its way into the pool.
type mined struct {
	work  model.Work
	count int // reviews citing it
}

// mergeReferences adds the referenced works with a DOI to the pool and
// returns the new pool and how many works were new. A work already in the pool
// only gains a citing review; a work listed twice by one review counts once.
// The new ones are kept by the number of citing reviews, at most
// maxMinedCandidates, ties by the order of the reviews (best ranked first).
// An excluded work is skipped and counted by excl (may be nil).
func mergeReferences(pool []Candidate, refs []reviewRefs, excl *doiExclusion) (merged []Candidate, added int) {
	at := make(map[string]int, len(pool))
	for i := range pool {
		if pool[i].DOI != "" {
			at[pool[i].DOI] = i
		}
	}

	var fresh []*mined

	byDOI := map[string]*mined{}

	for _, r := range refs {
		seen := map[string]bool{}

		for i := range r.works {
			doi := model.NormalizeDOI(r.works[i].DOI)
			if doi == "" || seen[doi] || excl.drop(doi) {
				continue
			}

			seen[doi] = true

			if pi, known := at[doi]; known {
				pool[pi].addSource(FoundBy{Source: model.SourceReviewRefs, CitingReviews: 1})
				continue
			}

			m, ok := byDOI[doi]
			if !ok {
				m = &mined{work: r.works[i]}
				byDOI[doi] = m
				fresh = append(fresh, m)
			}

			m.count++
		}
	}

	slices.SortStableFunc(fresh, func(a, b *mined) int { return b.count - a.count })

	fresh = fresh[:min(len(fresh), maxMinedCandidates)]

	for _, m := range fresh {
		p := m.work.ToPaper("")
		p.Source = model.SourceReviewRefs

		pool = append(pool, newCandidate(p, FoundBy{Source: model.SourceReviewRefs, CitingReviews: m.count}))
	}

	return pool, len(fresh)
}
