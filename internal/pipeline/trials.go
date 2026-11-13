package pipeline

// Trial identity. A trial is often published several times — the 12-month
// result, the five-year follow-up, the health-technology report — and the
// reports share one set of participants: counting them as separate studies
// inflates the pooled sample, the information size and the direction tally.
// The only key that says two reports are one trial is a shared trial
// registration number: shared authors or sample sizes were measured to merge
// different trials. Papers without a registration number are never merged.

import (
	"slices"

	"github.com/roman-redl/verdict/internal/model"
)

// trialPaper is a paper as the grouping sees it.
type trialPaper struct {
	ids []string // normalized trial registration numbers
	// primary is false for a review or other secondary research, and for a
	// paper that is not a study report (an erratum, a comment): it repeats the
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

// isStudyReport reports whether the paper may count as a study of its own: it
// is not a review (secondary research) and its publication types do not say
// it is an erratum, a comment, a letter or another paper without results of
// its own. The design is the critic's, else the discovery tag, else the title
// (resolveDesign); a design that cannot be told counts as a study.
func isStudyReport(e *model.EnrichedPaper, c model.Critique) bool {
	return !resolveDesign(e, c).IsSecondary() && model.NonStudyKind(e.PublicationTypes) == ""
}

// trialInputs lists the papers of the corpus for trialGroups; crits are
// aligned with st.Enriched.
func trialInputs(st *State, crits []model.Critique) []trialPaper {
	out := make([]trialPaper, len(st.Enriched))

	for i := range st.Enriched {
		e := &st.Enriched[i]
		if !isStudyReport(e, crits[i]) {
			continue
		}

		out[i] = trialPaper{ids: e.TrialIDs(), primary: true}
	}

	return out
}

// reportRank orders the reports of one trial: the one to keep is the report
// that carries the question's outcome at the question's time frame.
type reportRank struct {
	doi  string
	year int // zero when unknown: loses to any known year
	// timepoint is how well the report's follow-up matches the question's time
	// frame: 2 yes, 1 unclear or unknown, 0 no.
	timepoint int
	// outcome is how well its outcome matches the question's: 2 the same (or
	// unknown), 1 related, 0 different.
	outcome int
	weeks   float64 // follow-up in weeks; zero when unknown: loses to any stated one
}

// rankOf builds the rank of a report from its extracted effect; a report
// without one ranks as an unknown outcome at an unknown time point.
func rankOf(doi string, year int, ef *model.Effect) reportRank {
	r := reportRank{doi: doi, year: year, timepoint: 1, outcome: 2}
	if ef == nil {
		return r
	}

	switch ef.TimepointMatch {
	case model.TimepointYes:
		r.timepoint = 2
	case model.TimepointNo:
		r.timepoint = 0
	case model.TimepointUnclear, "":
		r.timepoint = 1
	}

	switch ef.OutcomeMatch {
	case model.OutcomeSame, "":
		r.outcome = 2
	case model.OutcomeRelated:
		r.outcome = 1
	case model.OutcomeDifferent:
		r.outcome = 0
	}

	if ef.TimepointWeeks != nil {
		r.weeks = *ef.TimepointWeeks
	}

	return r
}

// keptOver reports whether the report is the one to keep over the other: the
// one whose time point matches the question (yes over unclear), then the one
// whose outcome does, then the longer follow-up, then the later publication,
// then the lower DOI (so that the choice does not depend on the corpus order).
func (r reportRank) keptOver(o reportRank) bool {
	switch {
	case r.timepoint != o.timepoint:
		return r.timepoint > o.timepoint
	case r.outcome != o.outcome:
		return r.outcome > o.outcome
	case r.weeks != o.weeks:
		return r.weeks > o.weeks
	case r.year != o.year:
		return r.year > o.year
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

// tallyIndexes lists the critiques the direction tally counts: every direct
// primary study, with the reports of one trial reduced to the one report of the
// trial that ranks first (reportRank). A review or meta-analysis, a paper that
// is not a study report and a retracted paper (as in the pool and the evidence
// base) are never counted. Without the enriched papers
// (the stage before the enrichment) there is no registration number to group
// by, and only the critic's own design tells a review.
func tallyIndexes(st *State, crits []model.Critique) []int {
	enriched := len(st.Enriched) == len(crits)

	countable := func(i int) bool {
		if enriched {
			return !st.Enriched[i].Flags.Retracted && isStudyReport(&st.Enriched[i], crits[i])
		}

		return !crits[i].StudyDesign.IsSecondary()
	}

	var direct []int

	for i := range crits {
		if crits[i].Relevance == relevanceDirect && countable(i) {
			direct = append(direct, i)
		}
	}

	if !enriched {
		return direct
	}

	groups := trialGroups(trialInputs(st, crits))
	rank := func(i int) reportRank { return rankOf(st.Enriched[i].DOI, st.Enriched[i].Year, crits[i].Effect) }

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
