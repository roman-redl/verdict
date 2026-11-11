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
	"log/slog"

	"github.com/roman-redl/verdict/internal/model"
)

// Critique vocabulary values shared by several stages (defaults, pivotal
// study selection, direction counts).
const (
	valueUnknown    = "unknown" // design_quality / relevance — no data
	valueUnclear    = "unclear" // sample_adequacy / result_direction — no data
	relevanceDirect = "direct"  // relevance: the study is directly about the question

	// relevanceIndirect: the abstract does not report the asked outcome. The hybrid
	// pass still gives these studies a full text when direct ones leave slots free.
	relevanceIndirect = "indirect"
)

// critiqueDesignField documents the study_design field in the field list of
// both critique prompts. The pipeline keeps reviews out of the pooled estimate
// by it (pool.go), so the critic must tell a review from the trials it covers.
const critiqueDesignField = `study_design: "rct" | "cluster_rct" | "crossover_rct" | "nonrandomized_trial" | ` +
	`"cohort" | "case_control" | "cross_sectional" | "case_series" | "systematic_review" | "meta_analysis" | ` +
	`"narrative_review" | "guideline" | "other" | "unclear" — the design of THIS paper as stated in its ` +
	`text (a meta-analysis of RCTs is "meta_analysis", not "rct"; a pooled analysis of several trials is ` +
	`"meta_analysis"; an animal, in-vitro or modelling study is "other"; "unclear" when the text does not say),`

// critiqueEffectField documents the optional effect object in the field list
// of both critique prompts. The numbers are only extracted here: the pipeline
// pools them in Go (pool.go), the model never computes.
const critiqueEffectField = `effect (OPTIONAL object; omit it entirely when the text has no numbers for the ` +
	`question's outcome): the numbers the text reports for the question's outcome, with
  outcome (short text: which outcome these numbers are for), timepoint (when it was measured; only if stated),
  and ONE of these:
  - binary outcome: intervention {events, total} and control {events, total}; when the text gives only a ` +
	`percentage of each arm, {percent, total} instead of events (percent copied as printed, 0-100);
  - continuous outcome: unit (string), intervention {mean, sd, n} and control {mean, sd, n};
  - a reported estimate: measure ("RR" | "OR" | "HR" | "MD" | "SMD"), value, ci_low, ci_high, ci_level ` +
	`(the interval's level in percent as the text states it, e.g. 90, 95, 97.2; omit when not stated), ` +
	`reversed (true only when the paper's comparison is the comparator versus the intervention; omit otherwise), ` +
	`and unit for an MD.`

// critiqueSampleRule says which number sample_size_reported is: the pool and
// the information-size rating rest on it, so a trial's non-randomized or
// screened participants must not enter it.
const critiqueSampleRule = `Report sample size only if it is explicitly stated: for a randomized trial, the ` +
	`number RANDOMIZED into the compared arms — not the number screened or eligible, not participants of ` +
	`non-randomized preference, observational or registry arms, not households when individuals were ` +
	`randomized (when households or other clusters were randomized, give the number randomized and name the ` +
	`unit in concerns); for a non-randomized study, the number analysed in the compared groups. `

// critiqueEffectRules are the extraction and judgement rules that go with the
// effect field.
const critiqueEffectRules = `Effect extraction rules:
- "intervention" is the arm that receives the question's intervention, "control" is its comparator — ` +
	`whatever order the paper lists the arms in; never swap them.
- A reported estimate is copied as printed, with its own direction. When the paper's comparison is the ` +
	`comparator versus the intervention (e.g. "HR for carboplatin vs cisplatin" when the question's ` +
	`intervention is cisplatin), set reversed: true; never invert the value or the interval yourself.
- Copy the numbers verbatim from the provided text. Do not compute, convert, round or infer anything: ` +
	`no counts from percentages (give the percent and the arm's total instead), no SD from a standard error ` +
	`or an interval, no estimate from counts. Copy a reported interval together with its stated level ` +
	`(ci_level), whatever it is: 90%, 95%, 97.2%.
- The totals and n of the arms are those of the compared groups only: never add the participants of a ` +
	`non-randomized preference, observational or registry arm, and never use the number screened.
- Prefer counts or means for both arms; use a reported estimate with its interval only when the arms' ` +
	`numbers are not given, with two exceptions where the estimate comes first. Time-to-event outcomes ` +
	`(survival, time to relapse, progression or healing): give the reported hazard ratio with its interval, ` +
	`not counts of events, because counts ignore follow-up time; if only medians are reported, omit the ` +
	`effect and mention the medians in strengths or concerns. Cluster-randomized trials: give the reported ` +
	`adjusted estimate with its interval, because it accounts for clustering and raw counts overstate ` +
	`precision. All numbers are JSON numbers, not strings. When a number is missing, omit the effect ` +
	`rather than guess. Take the question's primary outcome at its main timepoint.

Result direction from numbers: when the text gives counts, means or an estimate for the question's outcome, ` +
	`judge result_direction from those numbers even if the authors report no significance test or effect ` +
	`estimate — "no test reported" alone is not "unclear". Compare the arms: a clear difference in the claim's ` +
	`direction is positive, a negligible difference is null, the opposite difference is negative; add the ` +
	`concern "no significance test reported" or "imprecise: few events" where it applies. Use "unclear" only ` +
	`when the outcome's numbers are absent.`

// critiqueSystemPrompt — the single-study form, used as the fallback for
// studies a batch dropped. Data comes only from what is provided; the sample
// size is reported only when it is actually stated in the abstract
// (otherwise the LLM invents one).
const critiqueSystemPrompt = `You are a rigorous methodologist and peer reviewer. ` +
	`You assess a single study's evidential value for a specific research question, ` +
	`based ONLY on the data provided (title, abstract or full text, design tags, citation tallies). ` +
	`Never invent facts; if data is missing, use the "unknown"/"unclear" values. ` +
	critiqueSampleRule +
	`When full_text is provided, it supersedes the abstract: read the methods and results sections, ` +
	`judge relevance and result_direction from them, ` +
	`and report concerns invisible in the abstract (statistical issues, endpoint switching, ` +
	`dropout handling, unreported exclusions).

Respond with a single JSON object, no markdown, with exactly these fields:
doi (string, copy verbatim), title (string, original),
design_quality: "high" | "moderate" | "low" | "unknown",
sample_adequacy: "adequate" | "small" | "very_small" | "unclear" | "na",
sample_size_reported (integer, 0 if not stated),
relevance: "direct" | "indirect" | "tangential" | "unknown",
` + critiqueDesignField + `
result_direction: "positive" | "null" | "negative" | "mixed" | "unclear" — ` +
	`the direction of THIS study's own findings relative to the research question ` +
	`(positive = supports the claim, null = no effect found, negative = opposite effect, mixed = inconsistent outcomes, ` +
	`unclear = not determinable from the provided text); judge from the reported results only, ` +
	`never from the title's framing; null means the study reports the question's outcome and finds ` +
	`no meaningful difference — say in strengths or concerns whether it was powered for that outcome,
strengths (array of short strings),
concerns (array of short strings),
` + critiqueEffectField + `

Relevance — the match with the question's population, intervention, comparator and outcome:
- direct: the study tests the question's intervention against its comparator (or a close equivalent) ` +
	`in its population. Minor differences (dose, duration, setting, a wait-list instead of a usual-care ` +
	`control) keep it direct — list them as concerns.
- indirect: a materially different population, intervention or comparator; the result transfers ` +
	`to the question only under a stated assumption.
- tangential: a different question; the study cannot inform this claim.
Relevance follows the study's design, not what the provided text happens to report: when the ` +
	`question's outcome is not reported in it, keep the relevance the design deserves, set result_direction ` +
	`to "unclear" and add the concern "outcome not reported in the provided text".

Design quality guidance: meta-analysis / systematic review of RCTs = high; RCT = high; ` +
	`large prospective cohort = moderate; small cohort / case-control = low; ` +
	`case report / narrative review / animal or in-vitro only = low.

` + critiqueEffectRules

// batchCritiqueSystemPrompt — the batched form of the same contract: same
// fields, same rules, several studies per call, each assessed independently
// (one study must not tilt another's assessment).
const batchCritiqueSystemPrompt = `You are a rigorous methodologist and peer reviewer. ` +
	`You assess several studies' evidential value for a specific research question, ` +
	`based ONLY on the data provided per study (title, abstract or full text, design tags, citation tallies). ` +
	`Assess each study INDEPENDENTLY: one study must not influence another's assessment. ` +
	`Never invent facts; if data is missing, use the "unknown"/"unclear" values. ` +
	critiqueSampleRule +
	`When a study's full_text is provided, it supersedes its abstract: read the methods and results sections, ` +
	`judge relevance and result_direction from them, ` +
	`and report concerns invisible in the abstract (statistical issues, endpoint switching, ` +
	`dropout handling, unreported exclusions).

Respond with a single JSON object, no markdown: {"critiques": [...]} — ` +
	`one object per input study, in the input order, each with exactly these fields:
doi (string, copy verbatim), title (string, original),
design_quality: "high" | "moderate" | "low" | "unknown",
sample_adequacy: "adequate" | "small" | "very_small" | "unclear" | "na",
sample_size_reported (integer, 0 if not stated),
relevance: "direct" | "indirect" | "tangential" | "unknown",
` + critiqueDesignField + `
result_direction: "positive" | "null" | "negative" | "mixed" | "unclear" — ` +
	`the direction of THIS study's own findings relative to the research question ` +
	`(positive = supports the claim, null = no effect found, negative = opposite effect, mixed = inconsistent outcomes, ` +
	`unclear = not determinable from the provided text); judge from the reported results only, ` +
	`never from the title's framing; null means the study reports the question's outcome and finds ` +
	`no meaningful difference — say in strengths or concerns whether it was powered for that outcome,
strengths (array of short strings),
concerns (array of short strings),
` + critiqueEffectField + `

Relevance — the match with the question's population, intervention, comparator and outcome:
- direct: the study tests the question's intervention against its comparator (or a close equivalent) ` +
	`in its population. Minor differences (dose, duration, setting, a wait-list instead of a usual-care ` +
	`control) keep it direct — list them as concerns.
- indirect: a materially different population, intervention or comparator; the result transfers ` +
	`to the question only under a stated assumption.
- tangential: a different question; the study cannot inform this claim.
Relevance follows the study's design, not what the provided text happens to report: when the ` +
	`question's outcome is not reported in it, keep the relevance the design deserves, set result_direction ` +
	`to "unclear" and add the concern "outcome not reported in the provided text".

Design quality guidance: meta-analysis / systematic review of RCTs = high; RCT = high; ` +
	`large prospective cohort = moderate; small cohort / case-control = low; ` +
	`case report / narrative review / animal or in-vitro only = low.

` + critiqueEffectRules

// critiqueInput is the per-study payload sent to the critic — the explicit
// field allowlist that keeps Consensus's own takeaways out of the evidence.
// The question is not part of it: the single-study form adds it to its
// payload, the batch carries it once at the top level.
func critiqueInput(e model.EnrichedPaper) map[string]any {
	return map[string]any{
		"doi":                e.DOI,
		"title":              e.Title,
		"year":               e.Year,
		"authors":            cappedAuthors(e.Authors, 15),
		"venue":              e.Venue,
		"study_design":       e.StudyDesign,
		"cited_by_count":     e.CitedByCount,
		"abstract":           truncate(e.Abstract, 3500),
		"full_text":          truncate(e.FullText, critiqueFullTextChars),
		"tldr":               e.TLDR, // when no abstract exists at all
		"scite_tally":        e.Tally,
		"contradicting_dois": e.ContradictingDOIs,
		"red_flags":          e.Flags,
	}
}

// normalizeCritique fills the guaranteed fields from the study when the LLM
// omitted or garbled them; an unknown study design becomes "unclear" with a
// warning in the log.
func normalizeCritique(c model.Critique, e model.EnrichedPaper, log *slog.Logger) model.Critique {
	c.DOI = cmp.Or(c.DOI, e.DOI)
	c.StudyDesign = normalizeDesign(c.StudyDesign, c.DOI, log)
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
	ctx context.Context, client Completer, e model.EnrichedPaper, question string, log *slog.Logger,
) (model.Critique, Usage, error) {
	c := blankCritique(e)

	in := critiqueInput(e)
	in["question"] = question

	b, err := json.Marshal(in)
	if err != nil {
		err = fmt.Errorf("encoding study %s: %w", e.DOI, err)
		c.Error = persistedErr(err)

		return c, Usage{}, err
	}

	var out model.Critique

	usage, err := AskJSON(ctx, client, critiqueSystemPrompt, "Assess this study:\n\n"+string(b), &out, 2000)
	if err != nil {
		c.Error = persistedErr(err)
		return c, usage, err
	}

	return normalizeCritique(out, e, log), usage, nil
}

// critiqueBatch assesses several studies in one call. Critiques are matched
// back to the studies by DOI (position as a fallback when counts line up);
// studies the batch omitted are simply absent from the result, and the
// caller falls back to single calls for them. A total call failure returns a
// nil map and the error — every study of the chunk then falls back.
func critiqueBatch(
	ctx context.Context, client Completer, papers []model.EnrichedPaper, question string, log *slog.Logger,
) (map[string]model.Critique, Usage, error) {
	type payload struct {
		Question string           `json:"question"`
		Studies  []map[string]any `json:"studies"`
	}

	p := payload{Question: question, Studies: make([]map[string]any, len(papers))}
	for i := range papers {
		p.Studies[i] = critiqueInput(papers[i])
	}

	b, err := json.Marshal(p)
	if err != nil {
		return nil, Usage{}, fmt.Errorf("encoding the studies: %w", err)
	}

	var out struct {
		Critiques []model.Critique `json:"critiques"`
	}

	maxTokens := 1000 + 700*len(papers) // the extracted effect adds ~150 tokens per study

	usage, err := AskJSON(ctx, client, batchCritiqueSystemPrompt,
		"Assess each study independently:\n\n"+string(b), &out, maxTokens)
	if err != nil {
		return nil, usage, err
	}

	return matchCritiques(papers, out.Critiques, log), usage, nil
}

// matchCritiques pairs the critiques of a batch answer with the studies:
// by DOI first, by position when the counts line up (the model garbled the
// DOIs). The result is keyed by normalized DOI; a study without a match is
// absent.
func matchCritiques(
	papers []model.EnrichedPaper, critiques []model.Critique, log *slog.Logger,
) map[string]model.Critique {
	slots := make([]*model.Critique, len(papers)) // matched critique per study
	byDOI := map[string]int{}

	for i := range papers {
		if d := model.NormalizeDOI(papers[i].DOI); d != "" {
			byDOI[d] = i
		}
	}

	for i := range critiques {
		pi, ok := byDOI[model.NormalizeDOI(critiques[i].DOI)]
		if ok && slots[pi] == nil {
			slots[pi] = &critiques[i]
		}
	}

	if len(critiques) == len(papers) { // positional fallback: DOI garbled
		for i := range slots {
			if slots[i] == nil {
				slots[i] = &critiques[i]
			}
		}
	}

	res := map[string]model.Critique{}

	for i := range papers {
		d := model.NormalizeDOI(papers[i].DOI)
		if slots[i] != nil && d != "" {
			res[d] = normalizeCritique(*slots[i], papers[i], log)
		}
	}

	return res
}

// critiqueCacheKey identifies the critique of one study for one question:
// DOI + question + a fingerprint of the inputs the critique reads (abstract,
// full text, design tag). A challenge re-run over an unchanged corpus hits
// the cache; a PDF dropped into fulltext/, a changed abstract or design tag
// misses it, so the study is re-critiqued instead of being served stale.
func critiqueCacheKey(e model.EnrichedPaper, question string) string {
	//nolint:gosec // a non-cryptographic fingerprint in the cache key; another algorithm would invalidate existing caches
	h := sha1.Sum([]byte(e.Abstract + "\x1f" + truncate(e.FullText, critiqueFullTextChars) + "\x1f" + e.StudyDesign))
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
