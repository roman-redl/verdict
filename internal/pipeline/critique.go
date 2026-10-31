package pipeline

// The critique stage: LLM methodology review, batched. One call assesses
// several studies, each in its own JSON object and independently of the
// others. Batching is the main token lever here: every claude-cli call is a
// fresh session that pays fixed overhead (harness instructions, CLAUDE.md,
// memory) before the payload, so the call count — not the payload —
// dominates the cost. A per-topic cache carries critiques across re-runs: a
// critique is a pure function of (study data, question), and challenge
// re-runs ask the same question over the same corpus.

import (
	"context"
	"crypto/sha1" //nolint:gosec // не криптография: отпечаток входа для ключа кэша критик
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/model"
)

// Значения словаря критик, общие для нескольких стадий (заполнение по
// умолчанию, отбор ключевых исследований, подсчёт направлений).
const (
	valueUnknown    = "unknown" // design_quality / relevance — данных нет
	valueUnclear    = "unclear" // sample_adequacy / result_direction — данных нет
	relevanceDirect = "direct"  // релевантность: исследование прямо о вопросе
)

// critiqueSystemPrompt — the single-study form, used as the fallback for
// studies a batch dropped. Data comes only from what is provided; the sample
// size is reported only when it is actually stated in the abstract
// (otherwise the LLM invents one).
const critiqueSystemPrompt = `You are a rigorous methodologist and peer reviewer. You assess a single study's evidential value for a specific research question, based ONLY on the data provided (title, abstract or full text, design tags, citation tallies). Never invent facts; if data is missing, use the "unknown"/"unclear" values. Report sample size only if it is explicitly stated. When full_text is provided, it supersedes the abstract: read the methods and results sections, and report concerns invisible in the abstract (statistical issues, endpoint switching, dropout handling, unreported exclusions).

Respond with a single JSON object, no markdown, with exactly these fields:
doi (string, copy verbatim), title (string, original),
design_quality: "high" | "moderate" | "low" | "unknown",
sample_adequacy: "adequate" | "small" | "very_small" | "unclear" | "na",
sample_size_reported (integer, 0 if not stated),
relevance: "direct" | "indirect" | "tangential" | "unknown",
result_direction: "positive" | "null" | "negative" | "mixed" | "unclear" — the direction of THIS study's own findings relative to the research question (positive = supports the claim, null = no effect found, negative = opposite effect, mixed = inconsistent outcomes, unclear = not determinable from the abstract); judge from the reported results only, never from the title's framing,
strengths (array of short strings),
concerns (array of short strings).

Design quality guidance: meta-analysis / systematic review of RCTs = high; RCT = high; large prospective cohort = moderate; small cohort / case-control = low; case report / narrative review / animal or in-vitro only = low.`

// batchCritiqueSystemPrompt — the batched form of the same contract: same
// fields, same rules, several studies per call, each assessed independently
// (one study must not tilt another's assessment).
const batchCritiqueSystemPrompt = `You are a rigorous methodologist and peer reviewer. You assess several studies' evidential value for a specific research question, based ONLY on the data provided per study (title, abstract or full text, design tags, citation tallies). Assess each study INDEPENDENTLY: one study must not influence another's assessment. Never invent facts; if data is missing, use the "unknown"/"unclear" values. Report sample size only if it is explicitly stated. When a study's full_text is provided, it supersedes its abstract: read the methods and results sections, and report concerns invisible in the abstract (statistical issues, endpoint switching, dropout handling, unreported exclusions).

Respond with a single JSON object, no markdown: {"critiques": [...]} — one object per input study, in the input order, each with exactly these fields:
doi (string, copy verbatim), title (string, original),
design_quality: "high" | "moderate" | "low" | "unknown",
sample_adequacy: "adequate" | "small" | "very_small" | "unclear" | "na",
sample_size_reported (integer, 0 if not stated),
relevance: "direct" | "indirect" | "tangential" | "unknown",
result_direction: "positive" | "null" | "negative" | "mixed" | "unclear" — the direction of THIS study's own findings relative to the research question (positive = supports the claim, null = no effect found, negative = opposite effect, mixed = inconsistent outcomes, unclear = not determinable from the abstract); judge from the reported results only, never from the title's framing,
strengths (array of short strings),
concerns (array of short strings).

Design quality guidance: meta-analysis / systematic review of RCTs = high; RCT = high; large prospective cohort = moderate; small cohort / case-control = low; case report / narrative review / animal or in-vitro only = low.`

// critiqueInput is the per-study payload sent to the critic — the explicit
// field allowlist that keeps Consensus's own takeaways out of the evidence.
func critiqueInput(e model.EnrichedPaper, question string) map[string]any {
	return map[string]any{
		"question":           question,
		"doi":                e.DOI,
		"title":              e.Title,
		"year":               e.Year,
		"authors":            cappedAuthors(e.Authors, 15),
		"venue":              e.Venue,
		"study_design":       e.StudyDesign,
		"cited_by_count":     e.CitedByCount,
		"abstract":           truncate(e.Abstract, 3500),
		"full_text":          truncate(e.FullText, 12000),
		"tldr":               e.TLDR, // when no abstract exists at all
		"scite_tally":        e.Tally,
		"contradicting_dois": e.ContradictingDOIs,
		"red_flags":          e.Flags,
	}
}

// normalizeCritique fills the guaranteed fields from the study when the LLM
// omitted or garbled them.
func normalizeCritique(c model.Critique, e model.EnrichedPaper) model.Critique {
	c.DOI = firstNonEmpty(c.DOI, e.DOI)
	c.Title = firstNonEmpty(c.Title, e.Title)
	c.DesignQuality = firstNonEmpty(c.DesignQuality, valueUnknown)
	c.SampleAdequacy = firstNonEmpty(c.SampleAdequacy, valueUnclear)
	c.Relevance = firstNonEmpty(c.Relevance, valueUnknown)
	c.ResultDirection = firstNonEmpty(c.ResultDirection, valueUnclear)
	return c
}

// critiquePaper reviews a single paper in its own call; a call failure does
// not panic — it is returned in Critique.Error and the stage keeps working.
// The second return is the call's token usage (zeroed when the backend
// reports none).
func critiquePaper(ctx context.Context, client llm.Completer, e model.EnrichedPaper, question string) (model.Critique, llm.Usage) {
	c := model.Critique{
		DOI:             e.DOI,
		Title:           e.Title,
		DesignQuality:   valueUnknown,
		SampleAdequacy:  valueUnclear,
		Relevance:       valueUnknown,
		ResultDirection: valueUnclear,
	}
	b, _ := json.Marshal(critiqueInput(e, question))
	var out model.Critique
	usage, err := llm.AskJSON(ctx, client, critiqueSystemPrompt, "Assess this study:\n\n"+string(b), &out, 2000)
	if err != nil {
		c.Error = err.Error()
		return c, usage
	}
	return normalizeCritique(out, e), usage
}

// critiqueBatch assesses several studies in one call. Critiques are matched
// back to the studies by DOI (position as a fallback when counts line up);
// studies the batch omitted are simply absent from the result, and the
// caller falls back to single calls for them. A total call failure returns a
// nil map — every study of the chunk then falls back.
func critiqueBatch(ctx context.Context, client llm.Completer, papers []model.EnrichedPaper, question string) (map[string]model.Critique, llm.Usage) {
	type payload struct {
		Question string           `json:"question"`
		Studies  []map[string]any `json:"studies"`
	}
	p := payload{Question: question, Studies: make([]map[string]any, len(papers))}
	for i := range papers {
		p.Studies[i] = critiqueInput(papers[i], question)
	}
	b, _ := json.Marshal(p)
	var out struct {
		Critiques []model.Critique `json:"critiques"`
	}
	maxTokens := 1000 + 500*len(papers)
	usage, err := llm.AskJSON(ctx, client, batchCritiqueSystemPrompt,
		"Assess each study independently:\n\n"+string(b), &out, maxTokens)
	if err != nil {
		return nil, usage
	}

	slots := make([]*model.Critique, len(papers)) // matched critique per study
	byDOI := map[string]int{}
	for i := range papers {
		if d := model.NormalizeDOI(papers[i].DOI); d != "" {
			byDOI[d] = i
		}
	}
	for i := range out.Critiques {
		pi, ok := byDOI[model.NormalizeDOI(out.Critiques[i].DOI)]
		if ok && slots[pi] == nil {
			slots[pi] = &out.Critiques[i]
		}
	}
	if len(out.Critiques) == len(papers) { // positional fallback: DOI garbled
		for i := range slots {
			if slots[i] == nil {
				slots[i] = &out.Critiques[i]
			}
		}
	}
	res := map[string]model.Critique{}
	for i := range papers {
		if slots[i] == nil {
			continue
		}
		if d := model.NormalizeDOI(papers[i].DOI); d != "" {
			res[d] = normalizeCritique(*slots[i], papers[i])
		}
	}
	return res, usage
}

// critiqueCacheKey identifies the critique of one study for one question:
// DOI + question + a fingerprint of the inputs the critique reads (abstract,
// full text, design tag). A challenge re-run over an unchanged corpus hits
// the cache; a PDF dropped into fulltext/, a changed abstract or design tag
// misses it, so the study is re-critiqued instead of being served stale.
func critiqueCacheKey(e model.EnrichedPaper, question string) string {
	//nolint:gosec // некриптографический отпечаток входа в ключе кэша; смена алгоритма инвалидировала бы готовые кэши
	h := sha1.Sum([]byte(e.Abstract + "\x1f" + truncate(e.FullText, 12000) + "\x1f" + e.StudyDesign))
	return model.NormalizeDOI(e.DOI) + "\x00" + question + "\x00" + fmt.Sprintf("%x", h)[:12]
}

// critique: the map stage. Cached critiques are reused as-is; the rest go
// through batched calls, with a per-study single-call fallback for whatever
// a batch dropped. A failure on one paper does not kill the stage — it goes
// into Critique.Error. Call usage is accumulated into the cost report.
func (p *Pipeline) critique(ctx context.Context, st *State, opts Options) error {
	if p.clients.LLM == nil {
		st.Warnings = append(st.Warnings,
			"no LLM backend configured: per-paper critique skipped, the verdict will rely on counters only")
		return nil
	}
	if st.Cost == nil {
		st.Cost = &CostReport{}
	}
	// Accumulate across re-invocations (the hybrid deepening pass re-enters
	// this stage) instead of overwriting the first pass's accounting.
	cost := st.Cost.Critique
	if cost == nil {
		cost = &StageCost{}
		st.Cost.Critique = cost
	}

	// PDFs that landed in fulltext/ after enrichment (the addpdf recipe, a
	// resume that skipped stage 2) enter here, before the cache checks: the
	// cache key includes the full text, so exactly those studies re-critique.
	p.pickupFullTexts(ctx, st)

	cachePath := critiqueCachePath(opts)
	cache := LoadCritiqueCache(cachePath, time.Duration(p.cfg.CritiqueCacheTTLDays)*24*time.Hour)

	critiques := make([]model.Critique, len(st.Enriched))
	var pending []int
	for i := range st.Enriched {
		e := st.Enriched[i]
		if model.NormalizeDOI(e.DOI) != "" {
			if c, ok := cache[critiqueCacheKey(e, st.Question)]; ok {
				critiques[i] = c.Critique
				continue
			}
		}
		critiques[i] = model.Critique{
			DOI:             e.DOI,
			Title:           e.Title,
			DesignQuality:   valueUnknown,
			SampleAdequacy:  valueUnclear,
			Relevance:       valueUnknown,
			ResultDirection: valueUnclear,
		}
		pending = append(pending, i)
	}
	if hits := len(st.Enriched) - len(pending); hits > 0 {
		p.log.Info("critique cache hits", "papers", hits, "to assess", len(pending))
	}

	type job struct {
		idx   int
		paper model.EnrichedPaper
	}
	var mu sync.Mutex
	cacheDirty := false
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(orDefault(opts.LLMConcurrency, p.cfg.LLMConcurrency))
	chunkSize := orDefault(opts.LLMChunk, p.cfg.LLMChunk)
	for start := 0; start < len(pending); start += chunkSize {
		end := min(start+chunkSize, len(pending))
		chunk := make([]job, 0, end-start)
		for _, i := range pending[start:end] {
			chunk = append(chunk, job{idx: i, paper: st.Enriched[i]})
		}
		g.Go(func() error {
			papers := make([]model.EnrichedPaper, len(chunk))
			for i := range chunk {
				papers[i] = chunk[i].paper
			}
			got, u := critiqueBatch(gctx, p.clients.LLM, papers, st.Question)
			var singles []int
			for i := range chunk {
				if c, ok := got[model.NormalizeDOI(chunk[i].paper.DOI)]; ok {
					critiques[chunk[i].idx] = c
					continue
				}
				singles = append(singles, i) // the batch dropped this study
			}
			for _, i := range singles {
				c, u1 := critiquePaper(gctx, p.clients.LLM, chunk[i].paper, st.Question)
				u = u.Add(u1)
				critiques[chunk[i].idx] = c
				if c.Error != "" {
					p.log.Warn("critique failed", "doi", chunk[i].paper.DOI, "err", c.Error)
				}
			}
			mu.Lock()
			cost.add(u)
			for i := range chunk {
				if c := critiques[chunk[i].idx]; c.Error == "" && model.NormalizeDOI(chunk[i].paper.DOI) != "" {
					cache[critiqueCacheKey(chunk[i].paper, st.Question)] = CritiqueCacheEntry{CheckedAt: time.Now(), Critique: c}
					cacheDirty = true
				}
			}
			if cacheDirty {
				if err := cache.Save(cachePath); err != nil {
					p.log.Warn("critique cache not saved", "err", err)
					cacheDirty = false
				}
			}
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	st.Critiques = critiques
	p.log.Info("critique llm usage", "calls", cost.Calls,
		"in", cost.InputTokens, "out", cost.OutputTokens)
	return nil
}
