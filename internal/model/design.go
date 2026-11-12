package model

import (
	"fmt"
	"strings"
)

// StudyDesign is the design of one paper as the critic reads it from the
// text. The set is short on purpose: it separates what later steps need to
// tell apart — secondary research (reviews, which must not be pooled with the
// trials they include) from primary studies, and randomized from observational
// ones (the GRADE starting level).
type StudyDesign string

const (
	// DesignRCT is a parallel-group randomized controlled trial.
	DesignRCT StudyDesign = "rct"
	// DesignClusterRCT randomizes groups (clinics, schools), not individuals.
	DesignClusterRCT StudyDesign = "cluster_rct"
	// DesignCrossoverRCT is a randomized crossover trial.
	DesignCrossoverRCT StudyDesign = "crossover_rct"
	// DesignNonrandomizedTrial is an intervention study without randomization.
	DesignNonrandomizedTrial StudyDesign = "nonrandomized_trial"
	// DesignCohort is a prospective or retrospective cohort study.
	DesignCohort StudyDesign = "cohort"
	// DesignCaseControl is a case-control study.
	DesignCaseControl StudyDesign = "case_control"
	// DesignCrossSectional is a cross-sectional study or survey.
	DesignCrossSectional StudyDesign = "cross_sectional"
	// DesignCaseSeries is a case report or a case series.
	DesignCaseSeries StudyDesign = "case_series"
	// DesignSystematicReview is a systematic review without a pooled estimate.
	DesignSystematicReview StudyDesign = "systematic_review"
	// DesignMetaAnalysis is a meta-analysis or a pooled analysis of studies.
	DesignMetaAnalysis StudyDesign = "meta_analysis"
	// DesignNarrativeReview is a non-systematic (narrative) review.
	DesignNarrativeReview StudyDesign = "narrative_review"
	// DesignGuideline is a clinical guideline or consensus statement.
	DesignGuideline StudyDesign = "guideline"
	// DesignOther is a design outside the list (animal, in-vitro, modelling).
	DesignOther StudyDesign = "other"
	// DesignUnclear means the design cannot be told from the text.
	DesignUnclear StudyDesign = "unclear"
)

// StudyDesigns lists every design in the order the critic's prompt names them.
func StudyDesigns() []StudyDesign {
	return []StudyDesign{
		DesignRCT, DesignClusterRCT, DesignCrossoverRCT, DesignNonrandomizedTrial,
		DesignCohort, DesignCaseControl, DesignCrossSectional, DesignCaseSeries,
		DesignSystematicReview, DesignMetaAnalysis, DesignNarrativeReview, DesignGuideline,
		DesignOther, DesignUnclear,
	}
}

// ParseStudyDesign parses a design as the critic writes it, ignoring case and
// reading spaces and hyphens as underscores ("Meta-analysis" is meta_analysis).
// The empty string is an error too: the caller decides what a missing value
// means (an artifact written before the field existed, or an omitted field).
func ParseStudyDesign(s string) (StudyDesign, error) {
	norm := strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(s)))
	d := StudyDesign(norm)

	switch d {
	case DesignRCT, DesignClusterRCT, DesignCrossoverRCT, DesignNonrandomizedTrial,
		DesignCohort, DesignCaseControl, DesignCrossSectional, DesignCaseSeries,
		DesignSystematicReview, DesignMetaAnalysis, DesignNarrativeReview, DesignGuideline,
		DesignOther, DesignUnclear:
		return d, nil
	}

	return "", fmt.Errorf("unknown study design %q (want one of %v)", s, StudyDesigns())
}

// IsKnown reports whether the design says something: it is neither empty nor
// "unclear".
func (d StudyDesign) IsKnown() bool { return d != "" && d != DesignUnclear }

// IsRandomized reports whether the design randomizes participants or groups:
// parallel, cluster and crossover trials. Every other design, including an
// unknown one, is not.
func (d StudyDesign) IsRandomized() bool {
	return d == DesignRCT || d == DesignClusterRCT || d == DesignCrossoverRCT
}

// IsSecondary reports whether the design is secondary research: a review or a
// guideline that summarizes other studies. Its numbers are not independent of
// the primary studies it covers.
func (d StudyDesign) IsSecondary() bool {
	switch d {
	case DesignSystematicReview, DesignMetaAnalysis, DesignNarrativeReview, DesignGuideline:
		return true
	case DesignRCT, DesignClusterRCT, DesignCrossoverRCT, DesignNonrandomizedTrial,
		DesignCohort, DesignCaseControl, DesignCrossSectional, DesignCaseSeries,
		DesignOther, DesignUnclear:
		return false
	}

	return false
}
