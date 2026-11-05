package pipeline

// The hybrid text mode's deepening, the tail of the critique stage. The first
// pass runs on abstracts (as lean); once the critic has marked each study's
// relevance and quality, the studies the verdict will rest on are known
// deterministically — no extra LLM call, and no synthesis needed to choose
// them. Exactly those get OA full texts and are re-critiqued (the cache key
// includes the full text, so only they miss). The synthesis stage then runs
// once, on the final critiques.

import (
	"context"
	"fmt"
	"sort"
)

// pivotalStudies ranks the studies the verdict rests on: direct relevance
// only, then design quality (high > moderate > low > unknown), then Scite
// support heat (supporting + contradicting citations). Returns indices into
// st.Enriched / st.Critiques (they are parallel), at most maxStudies.
func pivotalStudies(st *State, maxStudies int) []int {
	type candidate struct {
		idx  int
		rank int
		heat int
	}

	qualityRank := map[string]int{"high": 3, "moderate": 2, "low": 1} // unknown → 0

	var out []candidate

	for i := range st.Critiques {
		if i >= len(st.Enriched) || st.Critiques[i].Relevance != relevanceDirect {
			continue
		}

		heat := 0
		if t := st.Enriched[i].Tally; t != nil {
			heat = t.Supporting + t.Contradicting
		}

		out = append(out, candidate{idx: i, rank: qualityRank[st.Critiques[i].DesignQuality], heat: heat})
	}

	sort.SliceStable(out, func(a, b int) bool {
		if out[a].rank != out[b].rank {
			return out[a].rank > out[b].rank
		}

		return out[a].heat > out[b].heat
	})

	if len(out) > maxStudies {
		out = out[:maxStudies]
	}

	idxs := make([]int, 0, len(out))
	for _, c := range out {
		idxs = append(idxs, c.idx)
	}

	return idxs
}

// hybridPass runs the deepening and records that it happened: HybridDone
// is set even when no pivotal study had a full text (the pass ran, there was
// nothing to deepen), so a resume does not repeat it. Saving the critique
// artifact is the caller's job.
func (p *Pipeline) hybridPass(ctx context.Context, rp runParams, st *State) error {
	if err := p.hybridDeepen(ctx, rp, st); err != nil {
		return fmt.Errorf("hybrid deepening: %w", err)
	}

	st.HybridDone = true

	return nil
}

// hybridDeepen fetches OA full texts for the pivotal studies and re-critiques
// the changed ones with the deep client (Clients.LLMDeep, the critic when
// unset). It does not synthesize. When no pivotal study yields a full text,
// the pass is a no-op — the abstracts critiques stand.
func (p *Pipeline) hybridDeepen(ctx context.Context, rp runParams, st *State) error {
	maxStudies := p.set.HybridPivotalMax
	if maxStudies <= 0 {
		maxStudies = 5
	}

	got := 0

	for _, i := range pivotalStudies(st, maxStudies) {
		e := &st.Enriched[i]
		if e.FullText != "" {
			continue // already deep (a pre-placed PDF, or an earlier pass)
		}

		if txt := p.fetchFullText(ctx, rp.dir, e, true); txt != "" {
			e.FullText = txt
			got++
		}
	}

	if got == 0 {
		p.log.Info("hybrid: no OA full text for the pivotal studies — keeping the abstracts critiques")
		return nil
	}

	p.log.Info("hybrid: deepening pivotal studies with full texts", "studies", got)

	// the pivotal studies are the ones the verdict rests on: the deep client
	// re-critiques them (the first pass ran on the cheap critic)
	deep := p.clients.LLMDeep
	if deep == nil {
		deep = p.clients.LLM
	}

	return p.critiqueWith(ctx, rp, st, deep)
}
