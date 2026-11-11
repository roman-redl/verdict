package pipeline

// Trial identity. A trial is often published several times — the 12-month
// result, the five-year follow-up, the health-technology report — and the
// reports share one set of participants: counting them as separate studies
// inflates the pooled sample, the information size and the direction tally.
// The only key that says two reports are one trial is a shared trial
// registration number: shared authors or sample sizes were measured to merge
// different trials. Papers without a registration number are never merged.

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// trialPaper is a paper as the grouping sees it.
type trialPaper struct {
	ids []string // normalized trial registration numbers
	// primary is false for a review or other secondary research: it repeats the
	// data of the trials it covers and never joins a trial's group.
	primary bool
}

// trialGroups assigns every paper to its trial: two primary papers share a
// trial when they share a registration number, and a chain of such pairs
// (A-B by one number, B-C by another) is one trial. The result holds, per
// paper, the index of the first paper of its trial — the paper's own index
// when it stands alone.
func trialGroups(papers []trialPaper) []int {
	root := make([]int, len(papers))
	for i := range root {
		root[i] = i
	}

	find := func(i int) int {
		for root[i] != i {
			root[i] = root[root[i]]
			i = root[i]
		}

		return i
	}

	owner := map[string]int{} // registration number -> the first paper that names it

	for i, p := range papers {
		if !p.primary {
			continue
		}

		for _, id := range p.ids {
			j, seen := owner[id]
			if !seen {
				owner[id] = i
				continue
			}

			// the smaller index becomes the root, so a group is named by its first paper
			a, b := find(i), find(j)
			root[max(a, b)] = min(a, b)
		}
	}

	for i := range root {
		root[i] = find(i)
	}

	return root
}

// trialInputs lists the papers of the corpus for trialGroups; crits are
// aligned with st.Enriched.
func trialInputs(st *State, crits []model.Critique) []trialPaper {
	out := make([]trialPaper, len(st.Enriched))

	for i := range st.Enriched {
		e := &st.Enriched[i]
		if resolveDesign(e, crits[i]).IsSecondary() {
			continue
		}

		out[i] = trialPaper{ids: e.TrialIDs(), primary: true}
	}

	return out
}

// reportRank orders the reports of one trial: the one to keep is the latest.
type reportRank struct {
	doi  string
	year int // zero when unknown: loses to any known year
	n    int // participants; zero when unknown
}

// keptOver reports whether the report is the one to keep over the other: the
// later publication, then the one with more participants, then the lower DOI
// (so that the choice does not depend on the corpus order).
func (r reportRank) keptOver(o reportRank) bool {
	if r.year != o.year {
		return r.year > o.year
	}

	if r.n != o.n {
		return r.n > o.n
	}

	return r.doi < o.doi
}

// sharedRegistration names the registration number that ties a dropped report
// to the kept one: a number both name, else the dropped report's first (the
// two are then linked through a third report of the trial).
func sharedRegistration(dropped, kept []string) string {
	for _, id := range dropped {
		if slices.Contains(kept, id) {
			return id
		}
	}

	if len(dropped) > 0 {
		return dropped[0]
	}

	return ""
}

// fillTrialRegistrations sets the trial registration numbers of every paper:
// from the abstract (a regular expression per registry), and, for a primary
// study whose abstract names none, from the data banks of its PubMed record.
// A PubMed failure only costs the second source: the numbers from the
// abstracts stay. Reviews are not looked up — they never join a trial.
func (p *Pipeline) fillTrialRegistrations(ctx context.Context, enriched []model.EnrichedPaper) {
	byDOI := map[string][]int{} // normalized DOI -> indexes of the papers to look up

	var dois []string

	for i := range enriched {
		e := &enriched[i]
		e.TrialRegistrations = model.NormalizeRegistrations(
			append(e.TrialRegistrations, model.TrialRegistrations(e.Abstract)...))

		doi := model.NormalizeDOI(e.DOI)
		if len(e.TrialRegistrations) > 0 || doi == "" || p.clients.Trials == nil ||
			resolveDesign(e, model.Critique{}).IsSecondary() {
			continue
		}

		if _, seen := byDOI[doi]; !seen {
			dois = append(dois, doi)
		}

		byDOI[doi] = append(byDOI[doi], i)
	}

	if len(dois) == 0 {
		return
	}

	found, err := p.clients.Trials.Registrations(ctx, dois)
	if err != nil {
		p.log.Warn("pubmed unavailable: trial registrations taken from the abstracts only", "err", err)
	}

	filled := 0

	for doi, numbers := range found {
		ids := model.TrialRegistrations(strings.Join(numbers, " "))

		for _, i := range byDOI[doi] {
			enriched[i].TrialRegistrations = ids

			if len(ids) > 0 {
				filled++
			}
		}
	}

	p.log.Info("trial registrations looked up in pubmed", "papers", len(dois), "filled", filled)
}

// tallyIndexes lists the critiques the direction tally counts: every direct
// study, with the reports of one trial reduced to the latest one among them.
// Without the enriched papers (the stage before the enrichment) there is no
// registration number to group by.
func tallyIndexes(st *State, crits []model.Critique) []int {
	var direct []int

	for i := range crits {
		if crits[i].Relevance == relevanceDirect {
			direct = append(direct, i)
		}
	}

	if len(st.Enriched) != len(crits) {
		return direct
	}

	groups := trialGroups(trialInputs(st, crits))
	rank := func(i int) reportRank {
		return reportRank{
			doi: st.Enriched[i].DOI, year: st.Enriched[i].Year,
			n: cmp.Or(st.Enriched[i].SampleSize, crits[i].SampleSizeReported),
		}
	}

	best := map[int]int{} // trial -> the report to count

	var order []int // the trials in corpus order

	for _, i := range direct {
		cur, seen := best[groups[i]]
		if !seen {
			order = append(order, groups[i])
		}

		if !seen || rank(i).keptOver(rank(cur)) {
			best[groups[i]] = i
		}
	}

	out := make([]int, len(order))
	for k, g := range order {
		out[k] = best[g]
	}

	return out
}
