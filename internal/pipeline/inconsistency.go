package pipeline

// The input of the inconsistency judgment. GRADE judges inconsistency across
// studies: one study cannot be inconsistent, and neither can studies that
// report other outcomes (Core GRADE 3: "If only one eligible study exists,
// Core GRADE users will not rate down for inconsistency"). Whether a rating is
// due stays with the synthesis model; the code only counts the studies it can
// be judged across.

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
// same). The design stratum, the measure and the time point do not matter and
// neither does whether the study entered the pooled estimate: a second trial
// with numbers on the outcome that was kept out of the pool (another measure,
// a non-randomized design) still makes inconsistency judgeable.
func comparableStudies(st *State) int {
	crits := critiquesFor(st)
	trials := trialGroups(trialInputs(st, crits))
	seen := map[int]struct{}{}

	for i := range st.Enriched {
		e, c := &st.Enriched[i], crits[i]

		if c.Effect == nil || c.Relevance != relevanceDirect || e.Flags.Retracted || !isStudyReport(e, c) {
			continue
		}

		if !c.Effect.OutcomeMatch.IsSame() {
			continue
		}

		if _, err := deriveEffect(withRestoredSDs(c.Effect)); err != nil {
			continue
		}

		seen[trials[i]] = struct{}{}
	}

	return len(seen)
}
