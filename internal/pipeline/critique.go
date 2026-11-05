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
	"cmp"
	"context"
	"crypto/sha1" //nolint:gosec // not cryptography: a fingerprint of the input for the critique cache key
	"encoding/json"
	"fmt"

	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/model"
)

// Critique vocabulary values shared by several stages (defaults, pivotal
// study selection, direction counts).
const (
	valueUnknown    = "unknown" // design_quality / relevance — no data
	valueUnclear    = "unclear" // sample_adequacy / result_direction — no data
	relevanceDirect = "direct"  // relevance: the study is directly about the question
)

// critiqueSystemPrompt — the single-study form, used as the fallback for
// studies a batch dropped. Data comes only from what is provided; the sample
// size is reported only when it is actually stated in the abstract
// (otherwise the LLM invents one).
const critiqueSystemPrompt = `You are a rigorous methodologist and peer reviewer. ` +
	`You assess a single study's evidential value for a specific research question, ` +
	`based ONLY on the data provided (title, abstract or full text, design tags, citation tallies). ` +
	`Never invent facts; if data is missing, use the "unknown"/"unclear" values. ` +
	`Report sample size only if it is explicitly stated. ` +
	`When full_text is provided, it supersedes the abstract: read the methods and results sections, ` +
	`and report concerns invisible in the abstract (statistical issues, endpoint switching, ` +
	`dropout handling, unreported exclusions).

Respond with a single JSON object, no markdown, with exactly these fields:
doi (string, copy verbatim), title (string, original),
design_quality: "high" | "moderate" | "low" | "unknown",
sample_adequacy: "adequate" | "small" | "very_small" | "unclear" | "na",
sample_size_reported (integer, 0 if not stated),
relevance: "direct" | "indirect" | "tangential" | "unknown",
result_direction: "positive" | "null" | "negative" | "mixed" | "unclear" — ` +
	`the direction of THIS study's own findings relative to the research question ` +
	`(positive = supports the claim, null = no effect found, negative = opposite effect, mixed = inconsistent outcomes, ` +
	`unclear = not determinable from the abstract); judge from the reported results only, never from the title's framing,
strengths (array of short strings),
concerns (array of short strings).

Design quality guidance: meta-analysis / systematic review of RCTs = high; RCT = high; ` +
	`large prospective cohort = moderate; small cohort / case-control = low; ` +
	`case report / narrative review / animal or in-vitro only = low.`

// batchCritiqueSystemPrompt — the batched form of the same contract: same
// fields, same rules, several studies per call, each assessed independently
// (one study must not tilt another's assessment).
const batchCritiqueSystemPrompt = `You are a rigorous methodologist and peer reviewer. ` +
	`You assess several studies' evidential value for a specific research question, ` +
	`based ONLY on the data provided per study (title, abstract or full text, design tags, citation tallies). ` +
	`Assess each study INDEPENDENTLY: one study must not influence another's assessment. ` +
	`Never invent facts; if data is missing, use the "unknown"/"unclear" values. ` +
	`Report sample size only if it is explicitly stated. ` +
	`When a study's full_text is provided, it supersedes its abstract: read the methods and results sections, ` +
	`and report concerns invisible in the abstract (statistical issues, endpoint switching, ` +
	`dropout handling, unreported exclusions).

Respond with a single JSON object, no markdown: {"critiques": [...]} — ` +
	`one object per input study, in the input order, each with exactly these fields:
doi (string, copy verbatim), title (string, original),
design_quality: "high" | "moderate" | "low" | "unknown",
sample_adequacy: "adequate" | "small" | "very_small" | "unclear" | "na",
sample_size_reported (integer, 0 if not stated),
relevance: "direct" | "indirect" | "tangential" | "unknown",
result_direction: "positive" | "null" | "negative" | "mixed" | "unclear" — ` +
	`the direction of THIS study's own findings relative to the research question ` +
	`(positive = supports the claim, null = no effect found, negative = opposite effect, mixed = inconsistent outcomes, ` +
	`unclear = not determinable from the abstract); judge from the reported results only, never from the title's framing,
strengths (array of short strings),
concerns (array of short strings).

Design quality guidance: meta-analysis / systematic review of RCTs = high; RCT = high; ` +
	`large prospective cohort = moderate; small cohort / case-control = low; ` +
	`case report / narrative review / animal or in-vitro only = low.`

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
	c.DOI = cmp.Or(c.DOI, e.DOI)
	c.Title = cmp.Or(c.Title, e.Title)
	c.DesignQuality = cmp.Or(c.DesignQuality, valueUnknown)
	c.SampleAdequacy = cmp.Or(c.SampleAdequacy, valueUnclear)
	c.Relevance = cmp.Or(c.Relevance, valueUnknown)
	c.ResultDirection = cmp.Or(c.ResultDirection, valueUnclear)

	return c
}

// critiquePaper reviews a single paper in its own call; a call failure does
// not stop the stage — it is recorded in Critique.Error and returned as the
// third value for the caller to log. The second return is the call's token
// usage (zeroed when the backend reports none).
func critiquePaper(
	ctx context.Context, client llm.Completer, e model.EnrichedPaper, question string,
) (model.Critique, llm.Usage, error) {
	c := blankCritique(e)

	b, err := json.Marshal(critiqueInput(e, question))
	if err != nil {
		err = fmt.Errorf("encoding study %s: %w", e.DOI, err)
		c.Error = persistedErr(err)

		return c, llm.Usage{}, err
	}

	var out model.Critique

	usage, err := llm.AskJSON(ctx, client, critiqueSystemPrompt, "Assess this study:\n\n"+string(b), &out, 2000)
	if err != nil {
		c.Error = persistedErr(err)
		return c, usage, err
	}

	return normalizeCritique(out, e), usage, nil
}

// critiqueBatch assesses several studies in one call. Critiques are matched
// back to the studies by DOI (position as a fallback when counts line up);
// studies the batch omitted are simply absent from the result, and the
// caller falls back to single calls for them. A total call failure returns a
// nil map and the error — every study of the chunk then falls back.
func critiqueBatch(
	ctx context.Context, client llm.Completer, papers []model.EnrichedPaper, question string,
) (map[string]model.Critique, llm.Usage, error) {
	type payload struct {
		Question string           `json:"question"`
		Studies  []map[string]any `json:"studies"`
	}

	p := payload{Question: question, Studies: make([]map[string]any, len(papers))}
	for i := range papers {
		p.Studies[i] = critiqueInput(papers[i], question)
	}

	b, err := json.Marshal(p)
	if err != nil {
		return nil, llm.Usage{}, fmt.Errorf("encoding the studies: %w", err)
	}

	var out struct {
		Critiques []model.Critique `json:"critiques"`
	}

	maxTokens := 1000 + 500*len(papers)

	usage, err := llm.AskJSON(ctx, client, batchCritiqueSystemPrompt,
		"Assess each study independently:\n\n"+string(b), &out, maxTokens)
	if err != nil {
		return nil, usage, err
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

	return res, usage, nil
}

// critiqueCacheKey identifies the critique of one study for one question:
// DOI + question + a fingerprint of the inputs the critique reads (abstract,
// full text, design tag). A challenge re-run over an unchanged corpus hits
// the cache; a PDF dropped into fulltext/, a changed abstract or design tag
// misses it, so the study is re-critiqued instead of being served stale.
func critiqueCacheKey(e model.EnrichedPaper, question string) string {
	//nolint:gosec // a non-cryptographic fingerprint in the cache key; another algorithm would invalidate existing caches
	h := sha1.Sum([]byte(e.Abstract + "\x1f" + truncate(e.FullText, 12000) + "\x1f" + e.StudyDesign))
	return model.NormalizeDOI(e.DOI) + "\x00" + question + "\x00" + fmt.Sprintf("%x", h)[:12]
}

// blankCritique is the placeholder for a study not (yet) assessed: every
// field says "unknown", so a failed call degrades to "no information".
func blankCritique(e model.EnrichedPaper) model.Critique {
	return model.Critique{
		DOI:             e.DOI,
		Title:           e.Title,
		DesignQuality:   valueUnknown,
		SampleAdequacy:  valueUnclear,
		Relevance:       valueUnknown,
		ResultDirection: valueUnclear,
	}
}
