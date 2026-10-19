package pipeline

// The hybrid text mode's second half. The first pass runs on abstracts (as
// lean); once the critic has marked each study's relevance and quality, the
// studies the verdict rests on are known deterministically — no extra LLM
// call. Exactly those get OA full texts, are re-critiqued (the cache key
// includes the full text, so only they miss) and the verdict is rebuilt.

import (
	"context"
	"sort"
)

// pivotalStudies ranks the studies the verdict rests on: direct relevance
// only, then design quality (high > moderate > low > unknown), then Scite
// support heat (supporting + contradicting citations). Returns indices into
// st.Enriched / st.Critiques (they are parallel), at most max.
func pivotalStudies(st *State, max int) []int {
	type candidate struct {
		idx  int
		rank int
		heat int
	}
	qualityRank := map[string]int{"high": 3, "moderate": 2, "low": 1} // unknown → 0
	var out []candidate
	for i, c := range st.Critiques {
		if i >= len(st.Enriched) || c.Relevance != "direct" {
			continue
		}
		heat := 0
		if t := st.Enriched[i].Tally; t != nil {
			heat = t.Supporting + t.Contradicting
		}
		out = append(out, candidate{idx: i, rank: qualityRank[c.DesignQuality], heat: heat})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].rank != out[b].rank {
			return out[a].rank > out[b].rank
		}
		return out[a].heat > out[b].heat
	})
	if len(out) > max {
		out = out[:max]
	}
	idxs := make([]int, 0, len(out))
	for _, c := range out {
		idxs = append(idxs, c.idx)
	}
	return idxs
}

// hybridDeepen fetches OA full texts for the pivotal studies, re-critiques
// the changed ones and re-synthesizes. Called by Run once, after a fresh
// first synthesis; State.HybridDone (set by the caller) keeps resume from
// repeating the pass. When no pivotal study yields a full text, the pass is
// a no-op — the abstracts-only verdict stands.
func (p *Pipeline) hybridDeepen(ctx context.Context, st *State, opts Options) error {
	max := p.cfg.HybridPivotalMax
	if max <= 0 {
		max = 5
	}
	got := 0
	for _, i := range pivotalStudies(st, max) {
		e := &st.Enriched[i]
		if e.FullText != "" {
			continue // already deep (a pre-placed PDF, or an earlier pass)
		}
		if txt := p.fetchFullText(ctx, e, true); txt != "" {
			e.FullText = txt
			got++
		}
	}
	if got == 0 {
		p.log.Info("hybrid: no OA full text for the pivotal studies — keeping the abstracts verdict")
		return nil
	}
	p.log.Info("hybrid: deepening pivotal studies with full texts", "studies", got)
	if err := p.critique(ctx, st, opts); err != nil {
		return err
	}
	return p.synthesize(ctx, st, opts)
}
