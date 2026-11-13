package pipeline

// One study, once. Reports of one trial (12 months, 5 years, an HTA report)
// share their participants, and so do a paper and the correction notice that
// repeats its abstract: pooling them as separate studies counts the same people
// twice (Cochrane Handbook 5.2.1, 5.5.10).

import (
	"fmt"
	"math"
	"slices"

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

// dropDuplicates keeps one study of every group that reports the same results
// (sameResults): identical extracted arm data, or the same reported estimate of
// the same number of participants. Two papers with such numbers are one result
// seen twice (a correction notice or a reprint carrying the abstract of the
// paper it corrects, a conference abstract of a trial), and counting them twice
// narrows the interval for nothing. The kept study is a full article rather
// than a conference abstract, then the paper published first, then the lower
// DOI (keeperBefore).
//
// The estimate rule has a tolerance, which is not transitive (A is close to B
// and B to C, but A is not close to C), so the groups are built over the
// studies in the order of that preference: a study joins the first group whose
// keeper it matches, or starts a group of its own and keeps it. The first study
// of a group is therefore its final keeper, every member matches the keeper,
// and the result does not depend on the order of items. The kept and dropped
// studies are returned in the order of items.
func dropDuplicates(items []pooledStudy) (kept []pooledStudy, dropped []leftOut) {
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}

	slices.SortStableFunc(order, func(a, b int) int { return keeperCmp(&items[a], &items[b]) })

	var keepers []int // position in items of the study each group keeps

	keeperOf := make([]int, len(items)) // item -> the position of its group's keeper

	for _, pos := range order {
		g := slices.IndexFunc(keepers, func(k int) bool { return sameResults(&items[k], &items[pos]) })
		if g < 0 {
			keepers = append(keepers, pos)
			keeperOf[pos] = pos

			continue
		}

		keeperOf[pos] = keepers[g]
	}

	for pos := range items {
		keep := keeperOf[pos]
		if keep == pos {
			kept = append(kept, items[pos])
			continue
		}

		text := fmt.Sprintf("%s (%s as %s)", items[pos].doi, leftSameResults, items[keep].doi)
		dropped = append(dropped, leftOut{kind: leftSameResults, text: text})
	}

	return kept, dropped
}

// keeperCmp orders two studies by who is kept among reports of the same
// results: a study that is not a conference abstract first, then publishedBefore.
func keeperCmp(a, b *pooledStudy) int {
	switch {
	case a.abstract != b.abstract:
		if a.abstract {
			return 1
		}

		return -1
	case a.publishedBefore(b):
		return -1
	case b.publishedBefore(a):
		return 1
	}

	return 0
}

// A report of one trial in a journal, a reprint of it and its conference
// abstract round the same estimate differently: the interval bound may differ
// by one unit of its last printed digit (1.28 and 1.27).
const (
	estimateBoundTolerance = 0.01
	floatSlack             = 1e-9 // 1.28 - 1.27 is a hair above 0.01 in floating point
)

// reportedEstimate is a study's reported point estimate with its interval, as
// pooled (a reversed estimate in its intervention-versus-control form).
type reportedEstimate struct{ value, low, high float64 }

// sameResults reports whether two studies give the same results. Two studies
// with arm data do when the arm data are identical. Two with only a reported
// estimate do when the measure and the point value are the same, both interval
// bounds differ by at most estimateBoundTolerance, and the number of
// participants is known and equal: an estimate alone is a weak identity (two
// trials can both find RR 0.8), and the participant count is what separates
// them. A study without a participant count is never merged.
func sameResults(a, b *pooledStudy) bool {
	if a.results != "" || b.results != "" {
		return a.results == b.results
	}

	if a.estimate == nil || b.estimate == nil || a.n <= 0 || a.n != b.n || a.measure != b.measure {
		return false
	}

	return closeTo(a.estimate.value, b.estimate.value, 0) &&
		closeTo(a.estimate.low, b.estimate.low, estimateBoundTolerance) &&
		closeTo(a.estimate.high, b.estimate.high, estimateBoundTolerance)
}

// closeTo reports whether two numbers differ by at most tol (plus the slack of
// floating point).
func closeTo(a, b, tol float64) bool { return math.Abs(a-b) <= tol+floatSlack }

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
