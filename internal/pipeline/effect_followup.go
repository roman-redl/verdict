package pipeline

// The effect follow-up, the tail of the critique stage. The critic reads the
// same text on every run but does not always extract the numbers: one run of
// three returned no effect object for a direct study whose abstract has the
// arms' counts, and the study left the pool. After the critique (and the
// hybrid deepening, which re-critiques and can drop an effect too) each direct
// primary study with a result direction and no effect is asked once more, on
// its own, for the numbers only. The answer goes through the normalization of
// a normal critique and is saved into the critique artifact and the cache.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/internal/model"
)

// effectFollowUpMax caps the follow-up calls of one run: the studies that miss
// an effect are few, and a cap keeps a corpus the critic reads badly from
// costing a call per study.
const effectFollowUpMax = 10

// effectFollowUpSystemPrompt asks for the effect object only, with the same
// field description and extraction rules the critic prompts carry.
const effectFollowUpSystemPrompt = `You extract numbers from a single study's text for a research question, ` +
	`based ONLY on the text provided (title, abstract or full text). ` +
	`The study was already reviewed and is directly about the question, but that review returned no numbers: ` +
	`look again for the counts, means or the reported estimate the text gives for the question's outcome. ` +
	`Never invent facts.

Respond with a single JSON object, no markdown: {"effect": <object>} — or {"effect": null} when the text ` +
	`has no numbers for the question's outcome (answer null instead of omitting the field). The effect ` +
	`object is described by this field entry:
` + critiqueEffectField + `

` + critiqueEffectExtractionRules

// effectFollowUpInput is the payload of a follow-up: the question and the
// paper's text, nothing else (no tallies, no tags).
type effectFollowUpInput struct {
	Question    string            `json:"question"`
	DOI         string            `json:"doi"`
	Title       string            `json:"title"`
	StudyDesign model.StudyDesign `json:"study_design"`
	Abstract    string            `json:"abstract"`
	FullText    string            `json:"full_text,omitempty"`
}

// effectFollowUp is the outcome of one follow-up call.
type effectFollowUp struct {
	attempted bool // the call was made (a worker that saw a stop never started)
	answered  bool // the reply was valid: an effect or an explicit null
	effect    *model.Effect
	usage     Usage
	err       error
}

// needsEffectFollowUp reports whether the study is worth the question: a
// direct primary study report whose critique found a direction in the results
// but returned no numbers, with a text to read, not asked before.
func needsEffectFollowUp(e *model.EnrichedPaper, c model.Critique) bool {
	if c.Error != "" || c.Effect != nil || c.EffectAsked || c.Relevance != relevanceDirect || e.Flags.Retracted {
		return false
	}

	switch c.ResultDirection {
	case resultPositive, resultNull, resultNegative, resultMixed:
	default:
		return false
	}

	if e.Abstract == "" && e.FullText == "" {
		return false
	}

	d := resolveDesign(e, c)

	return d.IsKnown() && !d.IsSecondary() && model.NonStudyKind(e.PublicationTypes) == ""
}

// followUpEffects asks the critic for the missing numbers of the studies that
// need them (corpus order, at most effectFollowUpMax). Like the critique
// itself it stops on ErrLLMStop, keeping what the finished calls filled.
func (p *Pipeline) followUpEffects(ctx context.Context, rp runParams, st *State) error {
	client := p.clients.LLM
	if client == nil || len(st.Critiques) != len(st.Enriched) {
		return nil
	}

	var idxs []int

	for i := range st.Enriched {
		if needsEffectFollowUp(&st.Enriched[i], st.Critiques[i]) {
			idxs = append(idxs, i)
		}
	}

	if len(idxs) > effectFollowUpMax {
		p.log.Warn("effect follow-up capped: the rest keep no effect",
			"missing", len(idxs), "cap", effectFollowUpMax)

		idxs = idxs[:effectFollowUpMax]
	}

	if len(idxs) == 0 {
		return nil
	}

	results := make([]effectFollowUp, len(idxs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(orDefault(rp.opts.LLMConcurrency, p.set.LLMConcurrency))

	for k, idx := range idxs {
		g.Go(func() error {
			select {
			case <-gctx.Done(): // a sibling hit ErrLLMStop while this one waited for a slot
				return nil
			default:
			}

			results[k] = p.askEffect(gctx, client, &st.Enriched[idx], st.Critiques[idx], st.Question)
			if errors.Is(results[k].err, ErrLLMStop) {
				return results[k].err
			}

			return nil
		})
	}

	err := g.Wait()

	p.applyEffectFollowUps(rp, st, idxs, results)

	if errors.Is(err, ErrLLMStop) {
		p.log.Error("effect follow-up stopped: the LLM backend takes no more calls", "err", err)

		return errLLMStopped()
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("extracting the missing effects: %w", err)
	}

	return nil
}

// askEffect puts the follow-up question for one study. A failed call or an
// unreadable reply leaves the study as it was (with a warning).
func (p *Pipeline) askEffect(
	ctx context.Context, client Completer, e *model.EnrichedPaper, c model.Critique, question string,
) effectFollowUp {
	res := effectFollowUp{attempted: true}

	in, err := json.Marshal(effectFollowUpInput{
		Question: question, DOI: e.DOI, Title: e.Title, StudyDesign: resolveDesign(e, c),
		Abstract: truncate(e.Abstract, 3500), FullText: truncate(e.FullText, critiqueFullTextChars),
	})
	if err != nil {
		res.err = fmt.Errorf("encoding study %s: %w", e.DOI, err)
		return res
	}

	var out struct {
		Effect *model.Effect `json:"effect"`
	}

	res.usage, err = AskJSON(ctx, client, effectFollowUpSystemPrompt,
		"Extract the effect from this study:\n\n"+string(in), &out, 1500)
	if err != nil {
		res.err = err

		if !errors.Is(err, ErrLLMStop) {
			p.log.Warn("effect follow-up failed: the study keeps no effect", "doi", e.DOI, "err", err)
		}

		return res
	}

	normalizeEffect(out.Effect, e.DOI, p.log)

	if out.Effect != nil && usableEffect(out.Effect) == nil {
		p.log.Warn("effect follow-up returned no usable numbers: the study keeps no effect", "doi", e.DOI)

		out.Effect = nil
	}

	res.answered, res.effect = true, out.Effect

	return res
}

// applyEffectFollowUps accounts the calls (the critique stage's cost: the
// critic made them), puts the filled effects into the critiques and writes
// the changed critiques through to the cache, so that a resume or a re-run
// does not ask again.
func (p *Pipeline) applyEffectFollowUps(rp runParams, st *State, idxs []int, results []effectFollowUp) {
	cost := st.critiqueCost()
	calls, filled := 0, 0

	var changed []int

	for k, idx := range idxs {
		r := results[k]
		if !r.attempted {
			continue
		}

		calls++

		cost.add(r.usage)

		if !r.answered {
			continue
		}

		c := &st.Critiques[idx]
		c.EffectAsked = true

		if r.effect != nil {
			c.Effect = r.effect
			filled++
		}

		changed = append(changed, idx)
	}

	p.log.Info("effect follow-up", "missing", len(idxs), "asked", calls, "filled", filled)

	p.cacheCritiques(rp, st, changed)
}

// cacheCritiques writes the critiques at the given indexes through to the
// critique cache (the tail stages change critiques after the stage's own
// commit, and a resume or a re-run must find the changes there). An entry that
// exists keeps its CheckedAt: the critique was made then, and adding a marker
// or a number to it does not make it any fresher against the cache's TTL.
func (p *Pipeline) cacheCritiques(rp runParams, st *State, idxs []int) {
	if len(idxs) == 0 {
		return
	}

	path := critiqueCachePath(rp.opts)
	cache := LoadCritiqueCache(path, p.set.CritiqueCacheTTL, p.now(), p.log)

	for _, idx := range idxs {
		e := &st.Enriched[idx]
		if model.NormalizeDOI(e.DOI) == "" {
			continue
		}

		key := critiqueCacheKey(*e, st.Question)
		checkedAt := p.now()

		if old, ok := cache[key]; ok {
			checkedAt = old.CheckedAt
		}

		cache[key] = CritiqueCacheEntry{CheckedAt: checkedAt, Critique: st.Critiques[idx]}
	}

	if err := cache.Save(path); err != nil {
		p.log.Warn("critique cache not saved", "err", err)
	}
}
