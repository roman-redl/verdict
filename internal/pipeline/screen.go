package pipeline

// Discovery screening: one cheap LLM (the plan role) reads the titles of the
// candidate pool in batches and says which belong in the corpus. It sees the
// title, the year, the design tag and a short snippet — not the full text — so
// it is lenient on purpose: a doubtful title stays in, the critic judges the
// abstract later.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

const (
	// screenBatchSize is how many candidates one screening call judges: the
	// screening calls (one per batch) are the discovery stage's LLM cost.
	screenBatchSize = 50
	// screenMaxTokens fits a short decision for every candidate of a batch.
	screenMaxTokens = 4000
	// screenReasonChars caps the stored reason.
	screenReasonChars = 200
)

const screenSystemPrompt = `You screen candidate papers for a systematic evidence assessment. You get a research ` +
	`question and a JSON list of candidates: id, title, year and, when known, a design tag and a short snippet.

Include a candidate when:
- it directly tests the question's intervention against its comparator, or against the main components of the ` +
	`intervention, in the question's population — whatever the study design; or
- it is a systematic review or a meta-analysis of the question.
Exclude: papers off the topic, animal or in-vitro studies, editorials, commentaries, letters, trial protocols ` +
	`and registrations, and papers on another population or another intervention.
When the title alone does not let you tell, include the candidate: the next stage reads the abstract.

For every candidate also give your best guess of its design from the title and snippet:
- rct: a randomized trial (parallel, crossover or cluster)
- other_primary: any other original study
- review: a systematic review, a meta-analysis or another review
- other: not a study (editorial, protocol, guideline, errata)

Answer with {"decisions":[{"id":1,"include":true,"design":"rct","reason":"at most 12 words"}]} ` +
	`and cover every id of the list exactly once.`

// screenItem is a candidate as the screening model sees it; the id is the
// position in the batch.
type screenItem struct {
	ID      int    `json:"id"`
	Title   string `json:"title"`
	Year    int    `json:"year,omitempty"`
	Design  string `json:"design_tag,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

// screenAnswer is the model's reply. Include is a pointer: a decision that
// leaves it out is not an "exclude", it is no decision.
type screenAnswer struct {
	Decisions []struct {
		ID      int    `json:"id"`
		Include *bool  `json:"include"`
		Design  string `json:"design"`
		Reason  string `json:"reason"`
	} `json:"decisions"`
}

// screenOutcome is what one batch brought back; decisions are keyed by the
// candidate's index in the pool.
type screenOutcome struct {
	decisions map[int]ScreenDecision
	usage     Usage
	attempted bool
	err       error
}

// screenPool screens the whole pool in batches and records the decisions in
// it. Without the screening model it does nothing. A failed batch is a
// warning: its candidates stay unscreened (a Consensus hit stays eligible, a
// mined reference does not — see Candidate.eligible), never a failed run.
// That includes a usage limit: the critique stage, which stops on it, runs
// next, and the saved discovery artifact spares a repeat of the paid search.
func (p *Pipeline) screenPool(ctx context.Context, st *State, info *DiscoveryInfo) error {
	if len(info.Pool) == 0 {
		return nil
	}

	if p.clients.LLMPlan == nil {
		p.log.Info("discovery screening skipped: no screening model configured")
		return nil
	}

	// a record without a DOI cannot enter the corpus: it is not worth a call
	var idx []int

	for i := range info.Pool {
		if info.Pool[i].DOI != "" {
			idx = append(idx, i)
		}
	}

	batches := chunks(idx, screenBatchSize)
	outcomes := make([]screenOutcome, len(batches))

	var stopped atomic.Bool // set by a batch that hit the usage limit: later ones are not tried

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(orDefault(p.set.LLMConcurrency, 1))

	for i, batch := range batches {
		g.Go(func() error {
			if stopped.Load() {
				return nil
			}

			outcomes[i] = p.screenBatch(gctx, st.Question, info.Pool, batch)
			if errors.Is(outcomes[i].err, ErrLLMStop) {
				stopped.Store(true)
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("screening the pool: %w", err)
	}

	p.applyScreening(st, info, outcomes)

	calls := 0
	if st.Cost != nil && st.Cost.Screening != nil {
		calls = st.Cost.Screening.Calls
	}

	p.log.Info("discovery screening done", "candidates", len(info.Pool), "batches", len(batches), "llm_calls", calls)

	return nil
}

// applyScreening writes the decisions into the pool, accounts the cost and
// warns about the candidates left unscreened.
func (p *Pipeline) applyScreening(st *State, info *DiscoveryInfo, outcomes []screenOutcome) {
	for _, out := range outcomes {
		if !out.attempted {
			continue
		}

		st.screeningCost().add(out.usage)

		for at, d := range out.decisions {
			info.Pool[at].Screen = &d
		}

		if out.err != nil {
			p.log.Warn("discovery screening batch failed: its candidates stay unscreened", "err", out.err)
		}
	}

	unscreened, leftOut := 0, 0

	for i := range info.Pool {
		if info.Pool[i].Screen != nil || info.Pool[i].DOI == "" {
			continue
		}

		unscreened++

		if !info.Pool[i].eligible() {
			leftOut++
		}
	}

	if unscreened > 0 {
		st.Warnings = append(st.Warnings, fmt.Sprintf(
			"discovery screening left %d of %d candidates unscreened (a failed or incomplete call): "+
				"Consensus hits stay in the selection, %d candidates found only by PubMed or review references "+
				"were left out", unscreened, len(info.Pool), leftOut))
	}
}

// screenBatch asks the model about one batch of pool indexes.
func (p *Pipeline) screenBatch(ctx context.Context, question string, pool []Candidate, batch []int) screenOutcome {
	items := make([]screenItem, len(batch))

	for k, at := range batch {
		c := &pool[at]
		items[k] = screenItem{
			ID: k + 1, Title: truncate(c.Title, 300), Year: c.Year, Design: c.Design, Snippet: c.Snippet,
		}
	}

	payload, err := json.Marshal(items)
	if err != nil {
		return screenOutcome{err: fmt.Errorf("encoding the candidates: %w", err)}
	}

	user := "Question: " + truncate(question, 2000) + "\n\nCandidates:\n" + string(payload)

	var ans screenAnswer

	usage, err := AskJSON(ctx, p.clients.LLMPlan, screenSystemPrompt, user, &ans, screenMaxTokens)
	out := screenOutcome{usage: usage, attempted: true}

	if err != nil {
		out.err = err
		return out
	}

	out.decisions = p.screenDecisions(ans, batch)

	return out
}

// screenDecisions maps the model's decisions back to the pool. A decision
// with an unknown id, a repeated one or one without "include" is dropped: the
// candidate stays unscreened. An unreadable design guess of an included
// candidate falls back to other_primary with a warning.
func (p *Pipeline) screenDecisions(ans screenAnswer, batch []int) map[int]ScreenDecision {
	out := make(map[int]ScreenDecision, len(batch))

	for _, d := range ans.Decisions {
		if d.ID < 1 || d.ID > len(batch) || d.Include == nil {
			p.log.Warn("discovery screening: a decision was dropped", "id", d.ID, "has_include", d.Include != nil)
			continue
		}

		at := batch[d.ID-1]
		if _, dup := out[at]; dup {
			continue
		}

		design, err := ParseScreenDesign(d.Design)
		if err != nil {
			design = ScreenOther

			if *d.Include {
				design = ScreenOtherPrimary

				p.log.Warn("discovery screening: unreadable design guess, using other_primary",
					"id", d.ID, "err", err)
			}
		}

		out[at] = ScreenDecision{
			Include: *d.Include, Design: design, Reason: truncate(strings.TrimSpace(d.Reason), screenReasonChars),
		}
	}

	return out
}
