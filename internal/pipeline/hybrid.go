package pipeline

// The hybrid text mode's deepening, the tail of the critique stage. The first
// pass runs on abstracts (as lean); once the critic has marked each study's
// relevance and quality, the studies the verdict will rest on are known
// deterministically — no extra LLM call, and no synthesis needed to choose
// them. Exactly those get OA full texts and are re-critiqued (the cache key
// includes the full text, so only they miss). The synthesis stage then runs
// once, on the final critiques.

import (
	"cmp"
	"context"
	"fmt"
	"sort"
)

const (
	// defaultPivotalMax is the cap when the settings leave it unset.
	defaultPivotalMax = 7
	// pivotalAttemptsPerSlot bounds the full-text fetch attempts at this many
	// per slot: a run of paywalled papers passes its slot to the next
	// candidate, but must not walk the whole corpus.
	pivotalAttemptsPerSlot = 3
)

// pivotalStudies ranks the studies the verdict rests on, and so the ones that
// get a full text: direct studies first, then — only for the slots the direct
// ones leave free — indirect ones. Within each tier: design quality (high >
// moderate > low > unknown), then sample size, largest first (a larger trial
// weighs more in the pooled estimate; citation counts measure popularity, not
// weight). An unknown size counts as 0 and sorts last within its quality
// tier; the sort is stable, so equal studies keep their corpus order.
// Tangential and unknown studies are never chosen.
//
// Indirect studies qualify because the abstract is what made them indirect:
// a secondary outcome is often reported only in the full text, and the deep
// re-critique of that text can turn the study direct. Choosing direct studies
// only would deny exactly those studies the text that settles their relevance.
//
// Returns indices into st.Enriched / st.Critiques (they are parallel), in
// that order, at most maxStudies.
func pivotalStudies(st *State, maxStudies int) []int {
	type candidate struct {
		idx      int
		indirect bool
		rank     int
		size     int
	}

	qualityRank := map[string]int{"high": 3, "moderate": 2, "low": 1} // unknown → 0

	var out []candidate

	for i := range st.Critiques {
		if i >= len(st.Enriched) {
			continue
		}

		relevance := st.Critiques[i].Relevance
		if relevance != relevanceDirect && relevance != relevanceIndirect {
			continue
		}

		out = append(out, candidate{
			idx: i, indirect: relevance == relevanceIndirect,
			rank: qualityRank[st.Critiques[i].DesignQuality],
			size: cmp.Or(st.Enriched[i].SampleSize, st.Critiques[i].SampleSizeReported),
		})
	}

	sort.SliceStable(out, func(a, b int) bool {
		if out[a].indirect != out[b].indirect {
			return !out[a].indirect
		}

		if out[a].rank != out[b].rank {
			return out[a].rank > out[b].rank
		}

		return out[a].size > out[b].size
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

// pivotalFetch is the outcome of the full-text walk over the pivotal studies.
type pivotalFetch struct {
	tried    int // fetch attempts made
	obtained int // studies that now have a full text, fetched or already there
	fetched  int // the part of obtained that this pass fetched
}

// fetchPivotalTexts walks the pivotal studies in rank order until maxStudies of
// them have a full text or the candidates (or the attempt bound) run out: a
// slot whose text cannot be obtained passes to the next candidate. A study
// that already has its text (a pre-placed PDF, an earlier pass, full mode)
// counts as obtained without an attempt.
func (p *Pipeline) fetchPivotalTexts(
	ctx context.Context, rp runParams, st *State, maxStudies int,
) (pivotalFetch, error) {
	var res pivotalFetch

	maxTries := maxStudies * pivotalAttemptsPerSlot

	for _, i := range pivotalStudies(st, len(st.Critiques)) {
		if res.obtained >= maxStudies || res.tried >= maxTries {
			break
		}

		if err := ctx.Err(); err != nil {
			return res, fmt.Errorf("fetching pivotal full texts: %w", err)
		}

		e := &st.Enriched[i]
		if e.FullText != "" {
			res.obtained++
			continue
		}

		res.tried++

		if txt, src := p.fetchFullText(ctx, rp.dir, e, true); txt != "" {
			e.FullText, e.FullTextSource = txt, src
			res.obtained++
			res.fetched++
		}
	}

	return res, nil
}

// hybridDeepen fetches OA full texts for the pivotal studies (Europe PMC XML,
// else the PDF) and re-critiques the changed ones with the deep client
// (Clients.LLMDeep, the critic when unset). Up to HybridPivotalMax studies get
// a text: a paywalled candidate does not use up a slot, the next one in the
// pivotal order is tried, within a bounded number of attempts. The
// re-critique replaces the whole abstract critique of a study — relevance and
// direction included — so an indirect study whose full text reports the
// outcome comes out direct. It does not synthesize. When no pivotal study
// yields a new full text, the pass is a no-op — the abstracts critiques stand.
func (p *Pipeline) hybridDeepen(ctx context.Context, rp runParams, st *State) error {
	maxStudies := p.set.HybridPivotalMax
	if maxStudies <= 0 {
		maxStudies = defaultPivotalMax
	}

	res, err := p.fetchPivotalTexts(ctx, rp, st, maxStudies)
	if err != nil {
		return err
	}

	p.log.Info("hybrid: pivotal full texts",
		"cap", maxStudies, "tried", res.tried, "obtained", res.obtained, "fetched", res.fetched)

	if res.fetched == 0 {
		p.log.Info("hybrid: no OA full text for the pivotal studies — keeping the abstracts critiques")
		return nil
	}

	p.log.Info("hybrid: deepening pivotal studies with full texts", "studies", res.obtained)

	// the pivotal studies are the ones the verdict rests on: the deep client
	// re-critiques them (the first pass ran on the cheap critic)
	deep := p.clients.LLMDeep
	if deep == nil {
		deep = p.clients.LLM
	}

	return p.critiqueWith(ctx, rp, st, deep)
}
