package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/internal/model"
)

// critiqueRun is the state the critique workers share: the client that
// assesses the studies, the result slice (each worker writes its own
// indexes), the cache and the cost report (guarded by mu). Studies are
// addressed by their index in st.Enriched.
type critiqueRun struct {
	st        *State
	client    Completer
	critiques []model.Critique // index-aligned with st.Enriched
	cachePath string

	mu    sync.Mutex
	cache CritiqueCache
	cost  *StageCost
}

// critique is the map stage over the critic client.
func (p *Pipeline) critique(ctx context.Context, rp runParams, st *State) error {
	return p.critiqueWith(ctx, rp, st, p.clients.LLM)
}

// critiqueStage is the critique stage proper: the abstracts critique, and in
// the hybrid text mode the deepening of the pivotal studies right after it, so
// that the synthesis sees the final critiques and runs once.
func (p *Pipeline) critiqueStage(ctx context.Context, rp runParams, st *State) error {
	if err := p.critique(ctx, rp, st); err != nil {
		return err
	}

	if rp.opts.TextMode != TextHybrid || st.HybridDone {
		return nil
	}

	return p.hybridPass(ctx, rp, st)
}

// critiqueWith is the map stage with the given client (the hybrid deepening
// re-critiques the pivotal studies with the deep one). Cached critiques are
// reused as-is; the rest go through batched calls, with a per-study
// single-call fallback for whatever a batch dropped. A failure on one paper
// does not kill the stage — it goes into Critique.Error. Call usage is
// accumulated into the cost report. A backend that cannot take more calls
// (ErrLLMStop) does stop the stage: the finished chunks stay in the cache.
func (p *Pipeline) critiqueWith(ctx context.Context, rp runParams, st *State, client Completer) error {
	if client == nil {
		st.Warnings = append(st.Warnings,
			"no LLM backend configured: per-paper critique skipped, the verdict will rely on counters only")

		return nil
	}

	// PDFs that landed in fulltext/ after enrichment (the addpdf recipe, a
	// resume that skipped stage 2) enter here, before the cache checks: the
	// cache key includes the full text, so exactly those studies re-critique.
	p.pickupFullTexts(ctx, rp.dir, st)

	r := &critiqueRun{st: st, client: client, cachePath: critiqueCachePath(rp.opts), cost: st.critiqueCost()}
	r.cache = LoadCritiqueCache(r.cachePath, p.set.CritiqueCacheTTL, p.now(), p.log)

	pending := p.planCritiques(r)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(orDefault(rp.opts.LLMConcurrency, p.set.LLMConcurrency))

	for _, chunk := range chunks(pending, orDefault(rp.opts.LLMChunk, p.set.LLMChunk)) {
		g.Go(func() error {
			// a sibling chunk hit ErrLLMStop while this one waited for a slot
			select {
			case <-gctx.Done():
				return nil // the cause (a stop or the caller's cancellation) is reported after Wait
			default:
			}

			return p.assessChunk(gctx, r, chunk)
		})
	}

	if err := g.Wait(); err != nil {
		if errors.Is(err, ErrLLMStop) {
			p.log.Error("critique stopped: the LLM backend takes no more calls", "err", err)

			return errLLMStopped()
		}

		return fmt.Errorf("critiquing the studies: %w", err)
	}

	if err := ctx.Err(); err != nil {
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
// batch when its call failed). It returns an error only for ErrLLMStop: no
// single-call fallback then, the studies not assessed are marked skipped (so
// they never reach the cache as blank placeholders) and what the chunk did
// finish is still committed.
func (p *Pipeline) assessChunk(ctx context.Context, r *critiqueRun, chunk []int) error {
	question := r.st.Question

	papers := make([]model.EnrichedPaper, len(chunk))
	for i, idx := range chunk {
		papers[i] = r.st.Enriched[idx]
	}

	got, usage, err := critiqueBatch(ctx, r.client, papers, question, p.log)
	if errors.Is(err, ErrLLMStop) {
		return p.stopChunk(r, chunk, chunk, usage, err)
	}

	if err != nil {
		p.log.Warn("critique batch failed: falling back to single calls", "studies", len(papers), "err", err)
	}

	for i, idx := range chunk {
		if c, ok := got[model.NormalizeDOI(papers[i].DOI)]; ok {
			r.critiques[idx] = c
			continue
		}

		// the batch dropped this study
		c, u, err := critiquePaper(ctx, r.client, papers[i], question, p.log)
		usage = usage.Add(u)
		r.critiques[idx] = c

		if errors.Is(err, ErrLLMStop) {
			return p.stopChunk(r, chunk, chunk[i+1:], usage, err)
		}

		if err != nil {
			p.log.Warn("critique failed", "doi", papers[i].DOI, "err", err)
		}
	}

	p.commitChunk(r, chunk, usage)

	return nil
}

// stopChunk ends a chunk on ErrLLMStop: the studies in skipped were never
// assessed and get an error (a blank placeholder without one would be cached
// as a real critique), then the chunk is committed — what it did finish
// reaches the cache — and the stop error is handed to the group.
func (p *Pipeline) stopChunk(r *critiqueRun, chunk, skipped []int, usage Usage, stop error) error {
	for _, idx := range skipped {
		r.critiques[idx].Error = persistedErr(stop)
	}

	p.commitChunk(r, chunk, usage)

	return stop
}

// commitChunk accounts a finished batch: its token usage, and its successful
// critiques written through to the cache.
func (p *Pipeline) commitChunk(r *critiqueRun, chunk []int, usage Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cost.add(usage)

	added := false

	for _, idx := range chunk {
		e := &r.st.Enriched[idx]
		if c := r.critiques[idx]; c.Error == "" && model.NormalizeDOI(e.DOI) != "" {
			r.cache[critiqueCacheKey(*e, r.st.Question)] = CritiqueCacheEntry{CheckedAt: p.now(), Critique: c}
			added = true
		}
	}

	if added {
		if err := r.cache.Save(r.cachePath); err != nil {
			p.log.Warn("critique cache not saved", "err", err)
		}
	}
}
