package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/model"
)

// critiqueRun is the state the critique workers share: the result slice (each
// worker writes its own indexes), the cache and the cost report (guarded by
// mu). Studies are addressed by their index in st.Enriched.
type critiqueRun struct {
	st        *State
	critiques []model.Critique // index-aligned with st.Enriched
	cachePath string

	mu    sync.Mutex
	cache CritiqueCache
	cost  *StageCost
}

// critique is the map stage. Cached critiques are reused as-is; the rest go
// through batched calls, with a per-study single-call fallback for whatever a
// batch dropped. A failure on one paper does not kill the stage — it goes
// into Critique.Error. Call usage is accumulated into the cost report.
func (p *Pipeline) critique(ctx context.Context, rp runParams, st *State) error {
	if p.clients.LLM == nil {
		st.Warnings = append(st.Warnings,
			"no LLM backend configured: per-paper critique skipped, the verdict will rely on counters only")

		return nil
	}

	// PDFs that landed in fulltext/ after enrichment (the addpdf recipe, a
	// resume that skipped stage 2) enter here, before the cache checks: the
	// cache key includes the full text, so exactly those studies re-critique.
	p.pickupFullTexts(ctx, rp.dir, st)

	r := &critiqueRun{st: st, cachePath: critiqueCachePath(rp.opts), cost: st.critiqueCost()}
	r.cache = LoadCritiqueCache(r.cachePath, p.set.CritiqueCacheTTL, p.log)

	pending := p.planCritiques(r)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(orDefault(rp.opts.LLMConcurrency, p.set.LLMConcurrency))

	for _, chunk := range chunks(pending, orDefault(rp.opts.LLMChunk, p.set.LLMChunk)) {
		g.Go(func() error {
			p.assessChunk(gctx, r, chunk)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("critiquing the studies: %w", err)
	}

	st.Critiques = r.critiques

	p.log.Info("critique llm usage", "calls", r.cost.Calls,
		"in", r.cost.InputTokens, "out", r.cost.OutputTokens)

	return nil
}

// planCritiques fills the result slice with cache hits and blank
// placeholders and returns the indexes of the studies that still need an LLM
// assessment.
func (p *Pipeline) planCritiques(r *critiqueRun) []int {
	st := r.st
	r.critiques = make([]model.Critique, len(st.Enriched))

	var pending []int

	for i := range st.Enriched {
		e := &st.Enriched[i]
		if model.NormalizeDOI(e.DOI) != "" {
			if c, ok := r.cache[critiqueCacheKey(*e, st.Question)]; ok {
				r.critiques[i] = c.Critique
				continue
			}
		}

		r.critiques[i] = blankCritique(*e)
		pending = append(pending, i)
	}

	if hits := len(st.Enriched) - len(pending); hits > 0 {
		p.log.Info("critique cache hits", "papers", hits, "to assess", len(pending))
	}

	return pending
}

// assessChunk critiques one batch of studies in a single call, falling back
// to one call per study for whatever the batch dropped (or for the whole
// batch when its call failed).
func (p *Pipeline) assessChunk(ctx context.Context, r *critiqueRun, chunk []int) {
	question := r.st.Question

	papers := make([]model.EnrichedPaper, len(chunk))
	for i, idx := range chunk {
		papers[i] = r.st.Enriched[idx]
	}

	got, usage, err := critiqueBatch(ctx, p.clients.LLM, papers, question)
	if err != nil {
		p.log.Warn("critique batch failed: falling back to single calls", "studies", len(papers), "err", err)
	}

	for i, idx := range chunk {
		if c, ok := got[model.NormalizeDOI(papers[i].DOI)]; ok {
			r.critiques[idx] = c
			continue
		}

		// the batch dropped this study
		c, u, err := critiquePaper(ctx, p.clients.LLM, papers[i], question)
		usage = usage.Add(u)
		r.critiques[idx] = c

		if err != nil {
			p.log.Warn("critique failed", "doi", papers[i].DOI, "err", err)
		}
	}

	p.commitChunk(r, chunk, usage)
}

// commitChunk accounts a finished batch: its token usage, and its successful
// critiques written through to the cache.
func (p *Pipeline) commitChunk(r *critiqueRun, chunk []int, usage llm.Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cost.add(usage)

	added := false

	for _, idx := range chunk {
		e := &r.st.Enriched[idx]
		if c := r.critiques[idx]; c.Error == "" && model.NormalizeDOI(e.DOI) != "" {
			r.cache[critiqueCacheKey(*e, r.st.Question)] = CritiqueCacheEntry{CheckedAt: time.Now(), Critique: c}
			added = true
		}
	}

	if added {
		if err := r.cache.Save(r.cachePath); err != nil {
			p.log.Warn("critique cache not saved", "err", err)
		}
	}
}
