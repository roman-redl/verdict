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
	// ref names the study in the list: the report that best matches the
	// question, for a trial; pooled is whether the study is in the pooled estimate.
	ref    studyRef
	pooled bool
	// dois are the normalized answer keys (answerKey) of all the reports of the
	// study: the risk of bias answers are matched to the study by any of them.
	// weight is the study's share of the pooled estimate, zero when it is not
	// pooled or there is no estimate with weights.
	dois   []string
	weight float64
	// related is true when the study is in the pool (a single-study estimate
	// too) and the report the pool kept has a related outcome; relatedWeight is
	// that report's weight, so it is part of weight (zero for any other study).
	related       bool
	relatedWeight float64
}

// answerKey is how the synthesis names a report in its per-study answers: the
// normalized DOI, else the normalized title (the input lists a paper without a
// DOI by its title only); empty when the paper has neither.
func answerKey(e *model.EnrichedPaper) string {
	return cmp.Or(model.NormalizeDOI(e.DOI), model.NormalizeDOI(e.Title))
}

// studyRef identifies one report for the list of studies.
type studyRef struct {
	doi, title string
	year       int
	direction  model.StudyDirection
}

// refOf describes a report: its DOI, title (the label where there is none),
// year and the direction of its findings.
func refOf(e *model.EnrichedPaper, c model.Critique) studyRef {
	title := strings.TrimSpace(e.Title)
	if title == "" {
		title = paperLabel(e)
	}

	return studyRef{
		doi: model.NormalizeDOI(e.DOI), title: title, year: e.Year,
		direction: studyDirection(c.ResultDirection),
	}
}

// studyDirection reads the critic's result_direction against the claim.
func studyDirection(raw string) model.StudyDirection {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case resultPositive:
		return model.DirectionSupports
	case resultNull:
		return model.DirectionNull
	case resultNegative:
		return model.DirectionOpposite
	case resultMixed:
		return model.DirectionMixed
	}

	return model.DirectionUnclear
}

// evidenceBaseFor builds the evidence base of the corpus and writes its
// summary in the language of the report.
func evidenceBaseFor(st *State, pe *model.PooledEstimate, lang model.Language) *model.EvidenceBase {
	eb := buildEvidenceBase(st, pe)
	if eb != nil {
		eb.Summary = lexiconFor(lang).evidenceText(eb, pe)
	}

	return eb
}

// buildEvidenceBase summarizes the corpus in numbers and one entry per direct
// study; nil without enriched papers. The wording (Summary) is written by
// evidenceBaseFor. pe is the pooled estimate of the report (nil when nothing
// was pooled): the studies in it are marked.
func buildEvidenceBase(st *State, pe *model.PooledEstimate) *model.EvidenceBase {
	eb, _ := evidenceStudies(st, pe)

	return eb
}

// evidenceStudies is buildEvidenceBase with the studies it counted, which carry
// what the risk-of-bias aggregation needs (every report's DOI, the pool weight).
func evidenceStudies(st *State, pe *model.PooledEstimate) (*model.EvidenceBase, []evidenceStudy) {
	if len(st.Enriched) == 0 {
		return nil, nil
	}

	crits := critiquesFor(st)
	trials := trialGroups(trialInputs(st, crits))
	pooled, weights := pooledKeys(pe), poolWeights(pe)
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
				design: resolveDesign(e, c), label: paperLabel(e), ref: refOf(e, c),
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
		studies = append(studies, foldTrial(st, crits, members[root], pooled, weights))
		eb.RepeatReports += len(members[root]) - 1
	}

	fillEvidenceGroups(eb, studies)

	// Reviewed = Direct + RepeatReports + Retracted + Indirect: every assessed
	// paper is in exactly one of the four
	eb.Reviewed = len(st.Enriched) - eb.NotAssessed

	return eb, studies
}

// poolWeights maps the key (studyKey) of each pooled study to its weight in the
// estimate; nil when there is no estimate or it has no weights (an artifact
// written before they were kept, or a count that does not match the studies).
func poolWeights(pe *model.PooledEstimate) map[string]float64 {
	if pe == nil || len(pe.Weights) == 0 || len(pe.Weights) != len(pe.Studies) {
		return nil
	}

	out := make(map[string]float64, len(pe.Studies))

	for i, k := range pe.Studies {
		if k = strings.TrimSpace(k); k != "" {
			out[k] = pe.Weights[i]
		}
	}

	return out
}

// pooledKeys is the set of the keys (studyKey) of the studies in the pooled
// estimate; empty when nothing is pooled. A single study is not pooled with
// anything, so it is not marked either.
func pooledKeys(pe *model.PooledEstimate) map[string]bool {
	set := map[string]bool{}

	if pe == nil || pe.Method == model.PoolSingleStudy {
		return set
	}

	// the normalized form too: an estimate saved before the keys were
	// normalized names a DOI as the paper wrote it
	for _, k := range pe.Studies {
		for _, key := range []string{strings.TrimSpace(k), model.NormalizeDOI(k)} {
			if key != "" {
				set[key] = true
			}
		}
	}

	return set
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
// blinding, no size) is taken from the other reports in corpus order. The pool
// keeps one report per trial, so the study's weight is that report's weight.
func foldTrial(st *State, crits []model.Critique, reports []int, pooled map[string]bool,
	weights map[string]float64,
) evidenceStudy {
	kept := reports[0]
	rank := func(i int) reportRank { return rankOf(studyKey(&st.Enriched[i]), st.Enriched[i].Year, crits[i].Effect) }

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

	s := evidenceStudy{design: model.DesignUnclear, ref: refOf(&st.Enriched[kept], crits[kept])}
	sized, designed := false, false
	s.blinding = foldBlinding(crits, ordered)

	for _, i := range ordered {
		e, c := &st.Enriched[i], crits[i]

		// the study is pooled when any of its reports is: the pool keeps one
		// report per trial, which need not be the one named here
		key := studyKey(e)
		s.pooled = s.pooled || (key != "" && pooled[key])
		s.weight += weights[key]

		marked, w := relatedReport(c, key, pooled, weights)
		s.related, s.relatedWeight = s.related || marked, s.relatedWeight+w

		if key := answerKey(e); key != "" {
			s.dois = append(s.dois, key)
		}

		if d := resolveDesign(e, c); !designed && d.IsKnown() {
			s.design, designed = d, true
		}

		if n := paperSize(e, c); !sized && n > 0 {
			s.size, s.label, sized = n, paperLabel(e), true
		}
	}

	return s
}

// relatedReport tells whether the report with the key has a related outcome and
// is in the pool: marked, and then its weight in the pool (the weight of
// related outcomes); a report that is not marked has no such weight. The
// weights name every study of the estimate, a single study included (weight 1),
// which pooledKeys does not: that study counts and is marked as well, since it
// is the whole estimate.
func relatedReport(
	c model.Critique, key string, pooled map[string]bool, weights map[string]float64,
) (marked bool, weight float64) {
	if key == "" || c.Effect == nil || !c.Effect.OutcomeMatch.IsRelated() {
		return false, 0
	}

	weight, weighted := weights[key]
	if !pooled[key] && !weighted {
		return false, 0
	}

	return true, weight
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

	for i := range studies {
		k := evidenceKindOf(studies[i].design)
		byKind[k] = append(byKind[k], studies[i])
	}

	eb.Randomized = evidenceGroup(byKind[evidenceRandomized], true)
	eb.Observational = evidenceGroup(byKind[evidenceObservational], false)
	eb.Reviews = reviewGroup(byKind[evidenceReview])
	eb.Other = len(byKind[evidenceOther])
	eb.Direct = len(studies)

	for i := range studies {
		eb.Studies = append(eb.Studies, studies[i].record())
	}
}

// record is the study as the artifact keeps it.
func (s evidenceStudy) record() model.EvidenceStudy {
	return model.EvidenceStudy{
		DOI: s.ref.doi, Title: s.ref.title, Year: s.ref.year, Design: s.design, Blinding: s.blinding,
		Cochrane: s.cochrane, Participants: s.size, Direction: s.ref.direction, Pooled: s.pooled,
		OutcomeRelated: s.related,
	}
}

// reviewGroup counts the reviews and the Cochrane ones among them; their sizes
// are not summed, because a review recounts the participants of its trials.
func reviewGroup(reviews []evidenceStudy) *model.EvidenceGroup {
	if len(reviews) == 0 {
		return nil
	}

	g := &model.EvidenceGroup{Studies: len(reviews)}

	for i := range reviews {
		if reviews[i].cochrane {
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

	for i := range studies {
		s := &studies[i]

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
