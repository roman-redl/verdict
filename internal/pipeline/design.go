package pipeline

// The study design a stage works with. Three sources know it, in order of
// trust: the critic (it read the paper), the discovery tag (Consensus's own
// classification; absent in --dois and OpenAlex runs), and the title. The
// design matters for pooling: a review and the trials it includes carry the
// same data, so pooling both counts it twice.

import (
	"log/slog"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// resolveDesign returns the best known design of a study, or DesignUnclear
// when no source tells. A critic's "unclear" or an artifact without the field
// falls through to the discovery tag, then to the title.
//
// The critic's value is trusted as stored: it is normalized when the critique
// is written, so an unparseable one can come only from a hand-edited artifact
// and falls through like a missing one.
func resolveDesign(e *model.EnrichedPaper, c model.Critique) model.StudyDesign {
	if d, err := model.ParseStudyDesign(string(c.StudyDesign)); err == nil && d.IsKnown() {
		return d
	}

	if d := designFromTag(e.StudyDesign); d.IsKnown() {
		return d
	}

	return designFromTitle(e.Title)
}

// displayDesign is the text of the Design column and of the synthesis input:
// the discovery tag when there is one (the reader sees the source's own words),
// else the resolved design; empty when nothing is known.
func displayDesign(e *model.EnrichedPaper, c model.Critique) string {
	if e.StudyDesign != "" {
		return e.StudyDesign
	}

	if d := resolveDesign(e, c); d.IsKnown() {
		return string(d)
	}

	return ""
}

// consensusTags maps Consensus's study_type values (seen in real runs: rct,
// meta-analysis, systematic review, non-randomized experimental study, cohort
// study, cross-sectional study, literature review, case report, field study,
// theoretical/modeling/simulation study, bench experiment, other) onto the
// design set. "other" is Consensus's catch-all and says nothing, so it is
// absent here: the title may still show a review.
var consensusTags = map[string]model.StudyDesign{
	"rct":                               model.DesignRCT,
	"randomized controlled trial":       model.DesignRCT,
	"meta-analysis":                     model.DesignMetaAnalysis,
	"systematic review":                 model.DesignSystematicReview,
	"literature review":                 model.DesignNarrativeReview,
	"non-randomized experimental study": model.DesignNonrandomizedTrial,
	"cohort study":                      model.DesignCohort,
	"case-control study":                model.DesignCaseControl,
	"cross-sectional study":             model.DesignCrossSectional,
	"case report":                       model.DesignCaseSeries,
	"case series":                       model.DesignCaseSeries,
	"field study":                       model.DesignOther,
	"theoretical, modeling, or simulation study": model.DesignOther,
	"bench experiment":                           model.DesignOther,
}

// designFromTag maps a discovery tag; an empty or unrecognized one is unclear.
func designFromTag(tag string) model.StudyDesign {
	if d, ok := consensusTags[strings.ToLower(strings.TrimSpace(tag))]; ok {
		return d
	}

	return model.DesignUnclear
}

// Title words of secondary research. Only the title is read, so the list is
// conservative: a phrase that names the paper's own type, not a topic.
var (
	metaAnalysisWords = []string{"meta-analys", "meta analys", "metaanalys", "pooled analysis"}
	reviewWords       = []string{"systematic review", "umbrella review", "cochrane review"}
)

// designFromTitle recognizes a review by its title; a title without those
// words is unclear (it says nothing about a primary study's design).
func designFromTitle(title string) model.StudyDesign {
	t := strings.ToLower(title)

	for _, w := range metaAnalysisWords {
		if strings.Contains(t, w) {
			return model.DesignMetaAnalysis
		}
	}

	for _, w := range reviewWords {
		if strings.Contains(t, w) {
			return model.DesignSystematicReview
		}
	}

	return model.DesignUnclear
}

// normalizeDesign makes the critic's design a valid enum value. A missing one
// is "unclear" (the model left the field out); an unknown one is "unclear"
// with a warning, because the pipeline then falls back to the other sources
// instead of acting on a word it does not know.
func normalizeDesign(raw model.StudyDesign, doi string, log *slog.Logger) model.StudyDesign {
	if strings.TrimSpace(string(raw)) == "" {
		return model.DesignUnclear
	}

	d, err := model.ParseStudyDesign(string(raw))
	if err != nil {
		log.Warn("critic returned an unknown study design: using unclear", "doi", doi, "err", err)
		return model.DesignUnclear
	}

	return d
}
