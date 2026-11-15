package pipeline

// The input of the inconsistency judgment. GRADE judges inconsistency across
// studies: one study cannot be inconsistent, and neither can studies that
// report other outcomes (Core GRADE 3: "If only one eligible study exists,
// Core GRADE users will not rate down for inconsistency"). Whether a rating is
// due stays with the synthesis model; the code only counts the studies it can
// be judged across.

import (
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// inconsistencyBasis is the synthesis input "inconsistency_basis".
type inconsistencyBasis struct {
	// ComparableStudies is the number of distinct trials or studies that
	// report usable numbers on the question's outcome (see comparableStudies).
	ComparableStudies int `json:"comparable_studies"`
}

// comparableStudies counts the direct primary studies that inconsistency can
// be judged across: one per trial (reports that share a registration number
// count once, as in the pool), not retracted, not a review and not a paper
// without results of its own, whose extracted effect is usable (arm counts or
// means the code can turn into an estimate, or a reported estimate with its
// interval) and whose outcome is the question's (a missing label reads as the
// same) or, only when the study is in the pooled estimate, related to it. The
// design stratum, the measure and the time point do not matter, and neither does
// whether a study of the question's own outcome entered the pooled estimate: a
// second trial with numbers on the outcome that was kept out of the pool
// (another measure, a non-randomized design) still makes inconsistency
// judgeable. A related study left out of the pool is no evidence on the
// outcome and does not count.
func comparableStudies(st *State) int {
	crits := critiquesFor(st)
	trials := trialGroups(trialInputs(st, crits))
	pooled := poolMembers(st.Pooled)
	seen := map[int]struct{}{}

	for i := range st.Enriched {
		e, c := &st.Enriched[i], crits[i]

		if c.Effect == nil || c.Relevance != relevanceDirect || e.Flags.Retracted || !isStudyReport(e, c) {
			continue
		}

		if c.Effect.OutcomeMatch.IsDifferent() {
			continue
		}

		if c.Effect.OutcomeMatch.IsRelated() && !pooled[studyKey(e)] {
			continue
		}

		if _, err := deriveEffect(withRestoredSDs(c.Effect)); err != nil {
			continue
		}

		seen[trials[i]] = struct{}{}
	}

	return len(seen)
}

// poolMembers is the set of the keys (studyKey) of every study in the pooled
// estimate, a single study that is the whole estimate included (unlike
// pooledKeys); empty when nothing is pooled.
func poolMembers(pe *model.PooledEstimate) map[string]bool {
	set := map[string]bool{}

	if pe == nil {
		return set
	}

	for _, k := range pe.Studies {
		// the normalized form too: an estimate saved before the keys were
		// normalized names a DOI as the paper wrote it
		for _, key := range []string{strings.TrimSpace(k), model.NormalizeDOI(k)} {
			if key != "" {
				set[key] = true
			}
		}
	}

	return set
}
