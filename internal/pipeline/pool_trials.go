package pipeline

// One study, once. Reports of one trial (12 months, 5 years, an HTA report)
// share their participants, and so do a paper and the correction notice that
// repeats its abstract: pooling them as separate studies counts the same people
// twice (Cochrane Handbook 5.2.1, 5.5.10).

import (
	"fmt"

	"github.com/roman-redl/verdict/internal/model"
)

// chooseReports keeps one report per trial, the one that carries the question's
// outcome at its time point (reportRank), and names the others as repeat
// reports, each with the registration number that ties it to the kept one. It
// runs before the measure is chosen: the report is picked for what it measures
// and when, not for the scale it happens to use. A study that is not known to
// repeat another (trial 0) stays.
func chooseReports(items []pooledStudy) (kept []pooledStudy, dropped []leftOut) {
	best := map[int]int{} // trial -> position in items of the report to keep

	for pos := range items {
		it := &items[pos]
		if it.trial == 0 {
			continue
		}

		if cur, seen := best[it.trial]; !seen || it.rank.keptOver(items[cur].rank) {
			best[it.trial] = pos
		}
	}

	for pos := range items {
		it := items[pos]

		if keep, ok := best[it.trial]; ok && keep != pos {
			winner := &items[keep]
			text := fmt.Sprintf("%s (same trial as %s, %s)", it.doi, winner.doi, sharedRegistration(it.regs, winner.regs))
			dropped = append(dropped, leftOut{kind: leftRepeatReport, text: text})

			continue
		}

		kept = append(kept, it)
	}

	return kept, dropped
}

// dropDuplicates keeps one study of every group whose extracted arm data are
// identical — both arms' events and totals, or both arms' mean, SD and n. Two
// papers with exactly the same numbers report the same results (a correction
// notice or a reprint carrying the abstract of the paper it corrects), and
// counting them twice narrows the interval for nothing. The paper published
// first is kept, then the lower DOI. Only exact equality counts.
func dropDuplicates(items []pooledStudy) (kept []pooledStudy, dropped []leftOut) {
	first := map[string]int{} // results -> position in items of the study to keep

	for pos := range items {
		key := items[pos].results
		if key == "" {
			continue
		}

		if cur, seen := first[key]; !seen || items[pos].publishedBefore(&items[cur]) {
			first[key] = pos
		}
	}

	for pos := range items {
		it := items[pos]

		if keep, ok := first[it.results]; it.results != "" && ok && keep != pos {
			text := fmt.Sprintf("%s (%s as %s)", it.doi, leftSameResults, items[keep].doi)
			dropped = append(dropped, leftOut{kind: leftSameResults, text: text})

			continue
		}

		kept = append(kept, it)
	}

	return kept, dropped
}

// publishedBefore orders two studies by publication: the earlier year (a known
// year before an unknown one), then the lower DOI.
func (ps *pooledStudy) publishedBefore(o *pooledStudy) bool {
	if ps.year != o.year && ps.year != 0 && o.year != 0 {
		return ps.year < o.year
	}

	if ps.year != o.year {
		return ps.year != 0
	}

	return ps.doi < o.doi
}

// resultsKey is the extracted arm data of an effect as one comparable text:
// the counts of both arms, else the means, SDs and sizes of both; empty when the
// effect carries neither (a reported estimate has no arm data to compare).
func resultsKey(ef *model.Effect) string {
	if ef.Intervention == nil || ef.Control == nil {
		return ""
	}

	if a, b, ok, err := countArms(ef.Intervention, ef.Control); err == nil && ok {
		return fmt.Sprintf("counts %v/%v %v/%v", a.events, a.total, b.events, b.total)
	}

	if a, b, ok := meanArms(ef.Intervention, ef.Control); ok {
		return fmt.Sprintf("means %v,%v,%v %v,%v,%v", a.Mean, a.SD, a.N, b.Mean, b.SD, b.N)
	}

	return ""
}
