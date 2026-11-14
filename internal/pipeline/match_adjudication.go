package pipeline

// The outcome adjudication, the last step of the critique stage. The critic
// labels every extracted effect with outcome_match and timepoint_match, and
// the pool filters on those labels, but the same study got "same" in two runs
// and "related" in a third and left the pool by a coin flip. After the effect
// follow-up each direct primary study with usable numbers has its two labels
// settled by a vote: three independent calls of the plan-role model see the
// question and the effect's own outcome and time point (no abstracts), and the
// majority of the votes per label wins. A label without a majority stays as
// the critic wrote it. The result is saved into the critique artifact and the
// cache, with the critic's original label kept where the vote changed it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	// matchVotes is the number of independent calls per round of candidates.
	matchVotes = 3
	// matchVotesMin is the valid replies a round needs for its vote to count:
	// a single reply is the same coin flip the vote exists to remove.
	matchVotesMin = 2
	// matchCandidatesMax caps the candidates one call labels; more candidates
	// are split into several rounds of matchVotes calls.
	matchCandidatesMax = 40
)

// matchAdjudicationSystemPrompt carries the critic's own definitions of the two
// labels (critiqueOutcomeMatchDef, critiqueTimepointMatchDef).
const matchAdjudicationSystemPrompt = `You label extracted study effects for a research question. ` +
	`Each candidate is what a critic extracted from one study: the outcome the study's numbers are for ` +
	`(outcome), when they were measured (timepoint, as the text words it) and that follow-up in weeks ` +
	`(timepoint_weeks) when it is known. For every candidate decide two labels, judging ONLY from the ` +
	`question and the candidate's own fields; never invent facts about the study. Judge each candidate ` +
	`INDEPENDENTLY of the others, and by the definitions below, not by how alike the words look.

Definitions:
- ` + critiqueOutcomeMatchDef + `.
- ` + critiqueTimepointMatchDef + `.

Respond with a single JSON object, no markdown: {"matches": [...]} — one object per candidate, in the input ` +
	`order, each with exactly these fields: index (number, copied from the candidate), ` +
	`outcome_match ("same" | "related" | "different"), timepoint_match ("yes" | "no" | "unclear").`

// matchCandidate is one effect put to the vote: the question's outcome and
// time frame are in the question itself, the study's own are in these fields.
type matchCandidate struct {
	Index          int      `json:"index"`
	DOI            string   `json:"doi"`
	Outcome        string   `json:"outcome"`
	Timepoint      string   `json:"timepoint,omitempty"`
	TimepointWeeks *float64 `json:"timepoint_weeks,omitempty"`
}

// matchInput is the payload of one vote call.
type matchInput struct {
	Question   string           `json:"question"`
	Candidates []matchCandidate `json:"candidates"`
}

// matchLabels is one voter's answer for one candidate.
type matchLabels struct {
	outcome   model.OutcomeMatch
	timepoint model.TimepointMatch
}

// matchCall is the outcome of one vote call: labels is nil for a call that
// failed or answered with a malformed reply (no vote for any candidate).
type matchCall struct {
	attempted bool
	labels    []matchLabels
	usage     Usage
	err       error
}

// needsMatchAdjudication reports whether the study's effect labels are put to
// the vote: a direct primary study report with usable numbers, not voted on
// before.
func needsMatchAdjudication(e *model.EnrichedPaper, c model.Critique) bool {
	if c.Error != "" || c.MatchAdjudicated || c.Relevance != relevanceDirect || e.Flags.Retracted {
		return false
	}

	if usableEffect(c.Effect) == nil {
		return false
	}

	d := resolveDesign(e, c)

	return d.IsKnown() && !d.IsSecondary() && model.NonStudyKind(e.PublicationTypes) == ""
}

// adjudicateMatches settles the outcome and time point labels of the
// candidates by a vote (see the file comment). Like the critique itself it
// stops on ErrLLMStop: a vote cut short is not applied (a half vote is the
// coin flip again), the calls made are still counted, and a resume repeats it.
func (p *Pipeline) adjudicateMatches(ctx context.Context, rp runParams, st *State) error {
	if len(st.Critiques) != len(st.Enriched) {
		return nil
	}

	var idxs []int

	for i := range st.Enriched {
		if needsMatchAdjudication(&st.Enriched[i], st.Critiques[i]) {
			idxs = append(idxs, i)
		}
	}

	if len(idxs) == 0 {
		return nil
	}

	client := p.clients.LLMPlan
	if client == nil {
		p.log.Warn("outcome adjudication skipped: no plan-role model, the critic's labels stay", "candidates", len(idxs))

		return nil
	}

	rounds := chunks(idxs, matchCandidatesMax)
	calls, err := p.callMatchVotes(ctx, rp, st, client, rounds)

	cost := st.critiqueCost()

	for _, c := range calls {
		if c.attempted {
			cost.add(c.usage)
		}
	}

	if errors.Is(err, ErrLLMStop) {
		p.log.Error("outcome adjudication stopped: the LLM backend takes no more calls", "err", err)

		return errLLMStopped()
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("adjudicating the outcome labels: %w", err)
	}

	p.applyMatchVotes(rp, st, rounds, calls)

	return nil
}

// callMatchVotes makes the vote calls of every round (matchVotes per round, in
// parallel under the LLM concurrency limit); the result is index-aligned with
// the rounds, round r owning calls r*matchVotes up to (r+1)*matchVotes. The
// error is the ErrLLMStop that ended the group, if any.
func (p *Pipeline) callMatchVotes(
	ctx context.Context, rp runParams, st *State, client Completer, rounds [][]int,
) ([]matchCall, error) {
	calls := make([]matchCall, len(rounds)*matchVotes)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(orDefault(rp.opts.LLMConcurrency, p.set.LLMConcurrency))

	for r, round := range rounds {
		for v := range matchVotes {
			k := r*matchVotes + v

			g.Go(func() error {
				select {
				case <-gctx.Done(): // a sibling hit ErrLLMStop while this one waited for a slot
					return nil
				default:
				}

				calls[k] = p.askMatches(gctx, client, st, round)
				if errors.Is(calls[k].err, ErrLLMStop) {
					return calls[k].err
				}

				return nil
			})
		}
	}

	if err := g.Wait(); err != nil {
		return calls, fmt.Errorf("calling the vote: %w", err)
	}

	return calls, nil
}

// askMatches puts one vote call for the candidates of one round. A failed
// call or a malformed reply is no vote (with a warning).
func (p *Pipeline) askMatches(ctx context.Context, client Completer, st *State, round []int) matchCall {
	res := matchCall{attempted: true}

	in := matchInput{Question: st.Question, Candidates: make([]matchCandidate, len(round))}

	for i, idx := range round {
		ef := st.Critiques[idx].Effect
		in.Candidates[i] = matchCandidate{
			Index: i, DOI: st.Enriched[idx].DOI, Outcome: ef.Outcome, Timepoint: ef.Timepoint,
			TimepointWeeks: ef.TimepointWeeks,
		}
	}

	b, err := json.Marshal(in)
	if err != nil {
		res.err = fmt.Errorf("encoding the candidates: %w", err)
		return res
	}

	var out struct {
		Matches []struct {
			Index          *int   `json:"index"`
			OutcomeMatch   string `json:"outcome_match"`
			TimepointMatch string `json:"timepoint_match"`
		} `json:"matches"`
	}

	res.usage, err = AskJSON(ctx, client, matchAdjudicationSystemPrompt,
		"Label each candidate:\n\n"+string(b), &out, 300+80*len(round))
	if err != nil {
		res.err = err

		if !errors.Is(err, ErrLLMStop) {
			p.log.Warn("outcome adjudication call failed: no vote", "err", err)
		}

		return res
	}

	labels := make([]matchLabels, len(round))
	seen := make([]bool, len(round))

	for _, m := range out.Matches {
		om, errO := model.ParseOutcomeMatch(m.OutcomeMatch)
		tm, errT := model.ParseTimepointMatch(m.TimepointMatch)

		if m.Index == nil || *m.Index < 0 || *m.Index >= len(round) || seen[*m.Index] || errO != nil || errT != nil {
			p.log.Warn("outcome adjudication reply is malformed: no vote", "candidates", len(round))

			return res
		}

		seen[*m.Index] = true
		labels[*m.Index] = matchLabels{outcome: om, timepoint: tm}
	}

	for i := range seen {
		if !seen[i] {
			p.log.Warn("outcome adjudication reply misses a candidate: no vote", "candidates", len(round))

			return res
		}
	}

	res.labels = labels

	return res
}

// applyMatchVotes counts the valid votes of each round and writes the winning
// labels into the effects; the changed critiques go through to the cache.
func (p *Pipeline) applyMatchVotes(rp runParams, st *State, rounds [][]int, calls []matchCall) {
	var settled []int

	candidates, changedOutcome, changedTimepoint := 0, 0, 0

	for r, round := range rounds {
		var votes [][]matchLabels

		for _, c := range calls[r*matchVotes : (r+1)*matchVotes] {
			if c.labels != nil {
				votes = append(votes, c.labels)
			}
		}

		candidates += len(round)

		if len(votes) < matchVotesMin {
			p.log.Warn("outcome adjudication has too few valid replies: the critic's labels stay",
				"valid", len(votes), "needed", matchVotesMin, "candidates", len(round))

			continue
		}

		for i, idx := range round {
			co, ct := applyMatchMajority(st.Critiques[idx].Effect, votes, i)
			if co {
				changedOutcome++
			}

			if ct {
				changedTimepoint++
			}

			st.Critiques[idx].MatchAdjudicated = true
		}

		settled = append(settled, round...)
	}

	p.log.Info("outcome adjudication", "candidates", candidates, "settled", len(settled),
		"changed_outcome", changedOutcome, "changed_timepoint", changedTimepoint)

	p.cacheCritiques(rp, st, settled)
}

// applyMatchMajority sets the labels of the effect to the majority of the
// votes for candidate i and reports whether the outcome and the time point
// label changed. Without a majority (three different answers) the critic's
// label stays. A missing critic label reads as "same" / "unclear", so the vote
// confirming that reading is no change.
func applyMatchMajority(ef *model.Effect, votes [][]matchLabels, i int) (outcomeChanged, timepointChanged bool) {
	outcomes := make([]model.OutcomeMatch, len(votes))
	timepoints := make([]model.TimepointMatch, len(votes))

	for v := range votes {
		outcomes[v], timepoints[v] = votes[v][i].outcome, votes[v][i].timepoint
	}

	if won, ok := voteMajority(outcomes); ok && won != effectiveOutcome(ef.OutcomeMatch) {
		ef.OutcomeMatchCritic = effectiveOutcome(ef.OutcomeMatch)
		ef.OutcomeMatch = won
		outcomeChanged = true
	}

	if won, ok := voteMajority(timepoints); ok && won != effectiveTimepoint(ef.TimepointMatch) {
		ef.TimepointMatchCritic = effectiveTimepoint(ef.TimepointMatch)
		ef.TimepointMatch = won
		timepointChanged = true
	}

	return outcomeChanged, timepointChanged
}

// voteMajority returns the value more than half of the votes agree on.
func voteMajority[T comparable](votes []T) (T, bool) {
	counts := make(map[T]int, len(votes))

	for _, v := range votes {
		counts[v]++

		if counts[v]*2 > len(votes) {
			return v, true
		}
	}

	var zero T

	return zero, false
}

// effectiveOutcome is the outcome match as the pool reads it: a missing label
// is the same outcome.
func effectiveOutcome(m model.OutcomeMatch) model.OutcomeMatch {
	if m == "" {
		return model.OutcomeSame
	}

	return m
}

// effectiveTimepoint is the time point match as the pool reads it: a missing
// label is unclear.
func effectiveTimepoint(m model.TimepointMatch) model.TimepointMatch {
	if m == "" {
		return model.TimepointUnclear
	}

	return m
}

// critiqueTail is what the critique stage does after the abstracts critique
// and the hybrid deepening, both of which can drop or relabel an effect: the
// follow-up for the missing numbers, then the vote on the labels of the
// effects that are there.
func (p *Pipeline) critiqueTail(ctx context.Context, rp runParams, st *State) error {
	if err := p.followUpEffects(ctx, rp, st); err != nil {
		return err
	}

	return p.adjudicateMatches(ctx, rp, st)
}
