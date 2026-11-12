package model

import (
	"slices"
	"strings"
)

// nonStudyTypes maps a publication type, as OpenAlex (type, type_crossref) or
// PubMed (PublicationType) spells it in lower case, to the kind of paper that
// is not a study report: it carries no results of its own, or repeats those
// of another paper.
var nonStudyTypes = map[string]string{
	"erratum":                   kindErratum,
	"published erratum":         kindErratum,
	"retraction":                kindRetraction,
	"retraction of publication": kindRetraction,
	"expression of concern":     "expression of concern",
	"comment":                   "comment",
	"editorial":                 "editorial",
	"letter":                    "letter",
	"news":                      kindNews,
	"newspaper article":         kindNews,
	"paratext":                  "front or back matter",
	"peer-review":               "peer review report",
}

// The kinds that more than one publication type maps to.
const (
	kindErratum    = "erratum or correction"
	kindRetraction = "retraction notice"
	kindNews       = "news"
)

// studyTypePrefixes are the publication types that name a primary study: a
// paper typed both as a trial and, say, as a letter (a research letter) is a
// study report.
var studyTypePrefixes = []string{
	"randomized controlled trial", "controlled clinical trial", "clinical trial",
	"pragmatic clinical trial", "equivalence trial", "observational study",
}

// NormalizePublicationTypes lowercases and trims the types, drops empty ones
// and duplicates, and sorts the rest.
func NormalizePublicationTypes(types []string) []string {
	var out []string

	for _, t := range types {
		if t = strings.ToLower(strings.Join(strings.Fields(t), " ")); t != "" {
			out = append(out, t)
		}
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// MergePublicationTypes is the normalized union of publication types from
// several sources.
func MergePublicationTypes(lists ...[]string) []string {
	var all []string

	for _, l := range lists {
		all = append(all, l...)
	}

	return NormalizePublicationTypes(all)
}

// NonStudyKind names the kind of paper that is not a study report (an erratum
// or correction, a comment, an editorial, a letter, news, a retraction notice,
// an expression of concern) when the publication types say it is one; empty
// when they say nothing of the kind. A type that names a primary study wins
// over the others. Such a paper is never pooled, never counted in the direction
// tally and never takes a corpus slot.
func NonStudyKind(types []string) string {
	kind := ""

	for _, t := range NormalizePublicationTypes(types) {
		for _, p := range studyTypePrefixes {
			if strings.HasPrefix(t, p) {
				return ""
			}
		}

		if k, ok := nonStudyTypes[t]; ok && kind == "" {
			kind = k
		}
	}

	return kind
}
