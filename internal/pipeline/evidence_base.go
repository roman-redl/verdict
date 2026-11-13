package pipeline

// The evidence base: a short summary, computed in Go from the stage data, of
// the studies the verdict rests on (model.EvidenceBase). It counts what the
// critic judged to answer the question directly, by design group, with the
// blinding of the randomized trials and the sizes of the studies.
//
// One trial counts once. The grouping is the registration-number grouping of
// trials.go (trialGroups) and nothing more: reports that share a registration
// number are one trial, whatever the follow-up. The pool's finer dedup (the same
// results under two registrations, the pool's stratum and measure rules) needs
// the numbers of the effects and is not applied here, so a repeat report
// without a shared registration number is counted as a study of its own. A
// review is never merged into a trial.

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// cochraneDOIPrefix is the DOI prefix of the Cochrane Database of Systematic
// Reviews.
const cochraneDOIPrefix = "10.1002/14651858."

// largestLabelChars caps the name of the largest study in the summary.
const largestLabelChars = 60

// evidenceKind is the design group of a direct study in the summary.
type evidenceKind int

const (
	evidenceOther evidenceKind = iota
	evidenceRandomized
	evidenceObservational
	evidenceReview
)

// evidenceKindOf puts a design into its group. Non-randomized trials go with
// the observational studies (the same GRADE starting level); narrative reviews,
// guidelines, laboratory studies and an unclear design are "other".
func evidenceKindOf(d model.StudyDesign) evidenceKind {
	switch d {
	case model.DesignRCT, model.DesignClusterRCT, model.DesignCrossoverRCT:
		return evidenceRandomized
	case model.DesignNonrandomizedTrial, model.DesignCohort, model.DesignCaseControl,
		model.DesignCrossSectional, model.DesignCaseSeries:
		return evidenceObservational
	case model.DesignSystematicReview, model.DesignMetaAnalysis:
		return evidenceReview
	case model.DesignNarrativeReview, model.DesignGuideline, model.DesignOther, model.DesignUnclear:
		return evidenceOther
	}

	return evidenceOther
}

// evidenceStudy is one direct study: a trial with all its reports, or a review.
type evidenceStudy struct {
	design   model.StudyDesign
	blinding model.Blinding
	size     int    // zero when not stated
	label    string // names the study where it is the largest
	cochrane bool
}

// buildEvidenceBase summarizes the corpus; nil without enriched papers. pe is
// the pooled estimate of the report (nil when nothing was pooled).
func buildEvidenceBase(st *State, pe *model.PooledEstimate) *model.EvidenceBase {
	if len(st.Enriched) == 0 {
		return nil
	}

	crits := critiquesFor(st)
	trials := trialGroups(trialInputs(st, crits))
	eb := &model.EvidenceBase{}

	var (
		studies []evidenceStudy
		order   []int // the trials in corpus order
	)

	members := map[int][]int{} // trial -> its direct reports

	for i := range st.Enriched {
		e, c := &st.Enriched[i], crits[i]

		switch {
		case notAssessed(c):
			eb.NotAssessed++
		case c.Relevance != relevanceDirect, model.NonStudyKind(e.PublicationTypes) != "":
			eb.Indirect++
		case e.Flags.Retracted:
			eb.Retracted++
		case resolveDesign(e, c).IsSecondary():
			studies = append(studies, evidenceStudy{
				design: resolveDesign(e, c), label: paperLabel(e),
				cochrane: strings.HasPrefix(model.NormalizeDOI(e.DOI), cochraneDOIPrefix),
			})
		default:
			if _, seen := members[trials[i]]; !seen {
				order = append(order, trials[i])
			}

			members[trials[i]] = append(members[trials[i]], i)
		}
	}

	for _, root := range order {
		studies = append(studies, foldTrial(st, crits, members[root]))
		eb.RepeatReports += len(members[root]) - 1
	}

	fillEvidenceGroups(eb, studies)

	// Reviewed = Direct + RepeatReports + Retracted + Indirect: every assessed
	// paper is in exactly one of the four
	eb.Reviewed = len(st.Enriched) - eb.NotAssessed
	eb.Summary = evidenceText(eb, pe)

	return eb
}

// notAssessed reports whether the paper has no usable critique: the call
// failed (Error is set; the placeholder reads "unknown" everywhere) or no
// critique was matched to the paper (critiquesFor leaves the zero value, whose
// relevance is empty; an assessed paper always has one). Nothing is known about
// such a paper, so it is neither direct nor indirect.
func notAssessed(c model.Critique) bool {
	return c.Error != "" || c.Relevance == ""
}

// foldTrial folds the reports of one trial into a study. The kept report is
// the one that best matches the question (reportRank, as the pool and the
// direction tally keep it); a field it does not state (an unclear design, no
// blinding, no size) is taken from the other reports in corpus order.
func foldTrial(st *State, crits []model.Critique, reports []int) evidenceStudy {
	kept := reports[0]
	rank := func(i int) reportRank { return rankOf(st.Enriched[i].DOI, st.Enriched[i].Year, crits[i].Effect) }

	for _, i := range reports[1:] {
		if rank(i).keptOver(rank(kept)) {
			kept = i
		}
	}

	ordered := []int{kept}

	for _, i := range reports {
		if i != kept {
			ordered = append(ordered, i)
		}
	}

	s := evidenceStudy{design: model.DesignUnclear}
	sized, designed := false, false
	s.blinding = foldBlinding(crits, ordered)

	for _, i := range ordered {
		e, c := &st.Enriched[i], crits[i]

		if d := resolveDesign(e, c); !designed && d.IsKnown() {
			s.design, designed = d, true
		}

		if n := paperSize(e, c); !sized && n > 0 {
			s.size, s.label, sized = n, paperLabel(e), true
		}
	}

	return s
}

// foldBlinding is the blinding of a trial from its reports in the order given:
// the first value a report states; else "unclear" when a critic read a report
// and found none stated; else empty (no report has the field: not assessed).
func foldBlinding(crits []model.Critique, reports []int) model.Blinding {
	var out model.Blinding

	for _, i := range reports {
		b := crits[i].Blinding

		switch {
		case stated(b):
			return b
		case b == model.BlindingUnclear:
			out = b
		}
	}

	return out
}

// stated reports whether the critic gave a blinding the text states.
func stated(b model.Blinding) bool {
	return b == model.BlindingDouble || b == model.BlindingSingle || b == model.BlindingOpen
}

// paperSize is the participant number of a study as the pool reads it for a
// study without arm data: the critic's own count first, then the metadata's
// (the matrix N).
func paperSize(e *model.EnrichedPaper, c model.Critique) int {
	return cmp.Or(c.SampleSizeReported, e.SampleSize)
}

// paperLabel names a study in a sentence: its short title, else the first
// author with the year, else the DOI.
func paperLabel(e *model.EnrichedPaper) string {
	if t := strings.TrimSpace(e.Title); t != "" {
		return shortTitle(t, largestLabelChars)
	}

	if len(e.Authors) > 0 && e.Year > 0 {
		return strings.TrimSpace(e.Authors[0]) + " " + strconv.Itoa(e.Year)
	}

	return e.DOI
}

// fillEvidenceGroups sorts the studies into the groups of the summary.
func fillEvidenceGroups(eb *model.EvidenceBase, studies []evidenceStudy) {
	byKind := map[evidenceKind][]evidenceStudy{}

	for _, s := range studies {
		k := evidenceKindOf(s.design)
		byKind[k] = append(byKind[k], s)
	}

	eb.Randomized = evidenceGroup(byKind[evidenceRandomized], true)
	eb.Observational = evidenceGroup(byKind[evidenceObservational], false)
	eb.Reviews = reviewGroup(byKind[evidenceReview])
	eb.Other = len(byKind[evidenceOther])
	eb.Direct = len(studies)
}

// reviewGroup counts the reviews and the Cochrane ones among them; their sizes
// are not summed, because a review recounts the participants of its trials.
func reviewGroup(reviews []evidenceStudy) *model.EvidenceGroup {
	if len(reviews) == 0 {
		return nil
	}

	g := &model.EvidenceGroup{Studies: len(reviews)}

	for _, r := range reviews {
		if r.cochrane {
			g.Cochrane++
		}
	}

	return g
}

// evidenceGroup describes the studies of a primary-design group: the blinding
// (randomized trials only) and the sizes.
func evidenceGroup(studies []evidenceStudy, withBlinding bool) *model.EvidenceGroup {
	if len(studies) == 0 {
		return nil
	}

	g := &model.EvidenceGroup{Studies: len(studies)}

	var sizes []int

	for _, s := range studies {
		if withBlinding {
			countBlinding(g, s.blinding)
		}

		if s.size <= 0 {
			g.SizeNotStated++
			continue
		}

		sizes = append(sizes, s.size)
		g.Participants += s.size

		if s.size > g.Largest {
			g.Largest, g.LargestStudy = s.size, s.label
		}
	}

	g.MedianSize = median(sizes)

	return g
}

// countBlinding adds one study to the blinding counters of the group.
func countBlinding(g *model.EvidenceGroup, b model.Blinding) {
	switch b {
	case model.BlindingDouble:
		g.DoubleBlind++
	case model.BlindingSingle:
		g.SingleBlind++
	case model.BlindingOpen:
		g.Open++
	case model.BlindingUnclear:
		g.BlindingNotStated++
	default: // empty: a critique from before the field existed says nothing
		g.BlindingNotAssessed++
	}
}

// median of the values; the mean of the two middle ones, rounded half up, for
// an even count; zero for none.
func median(values []int) int {
	if len(values) == 0 {
		return 0
	}

	s := slices.Sorted(slices.Values(values))
	mid := len(s) / 2

	if len(s)%2 == 1 {
		return s[mid]
	}

	return (s[mid-1] + s[mid] + 1) / 2
}
