package model

import (
	"regexp"
	"slices"
	"strings"
)

// registryPattern recognizes one trial registry's identifiers in free text.
// format turns the match (the whole text, then each capture group) into the
// normalized number: uppercase, no spaces.
type registryPattern struct {
	re     *regexp.Regexp
	format func(m []string) string
}

func prefixed(prefix string) func([]string) string {
	return func(m []string) string { return prefix + m[1] }
}

// registryPatterns cover the registries that primary trials are registered
// in. PROSPERO (CRD...) is absent on purpose: it registers systematic
// reviews, and a review is not a trial.
var registryPatterns = []registryPattern{
	// ClinicalTrials.gov
	{regexp.MustCompile(`(?i)\bNCT[\s-]?(\d{8})\b`), prefixed("NCT")},
	{regexp.MustCompile(`(?i)\bISRCTN[\s-]?(\d{8})\b`), prefixed("ISRCTN")},
	// the Australian New Zealand registry: 14 digits, sometimes a letter after them
	{regexp.MustCompile(`(?i)\bACTRN[\s-]?(\d{14})[A-Z]?\b`), prefixed("ACTRN")},
	// the same number appears with and without the category code (ChiCTR-TRC-13003456 and
	// ChiCTR13003456), so the digits alone identify the trial
	{regexp.MustCompile(`(?i)\bChiCTR(?:-[A-Z]+)*-?(\d{8,10})\b`), prefixed("CHICTR")},
	// EudraCT; the optional last part is the EU CT number's version suffix
	{regexp.MustCompile(`\b((?:19|20)\d{2}-\d{6}-\d{2}(?:-\d{2})?)\b`), func(m []string) string { return m[1] }},
	{regexp.MustCompile(`(?i)\bDRKS[\s-]?(\d{8})\b`), prefixed("DRKS")},
	{
		regexp.MustCompile(`(?i)\bCTRI\s?/\s?(\d{4})\s?/\s?(\d{2,3})\s?/\s?(\d{6})\b`),
		func(m []string) string { return "CTRI/" + m[1] + "/" + m[2] + "/" + m[3] },
	},
	{regexp.MustCompile(`(?i)\bUMIN(?:[\s-]?CTR)?[\s:.-]*(?:ID|No\.?)?[\s:.-]*(\d{9})\b`), prefixed("UMIN")},
	{regexp.MustCompile(`(?i)\bIRCT[\s-]?(\d{8,16}N\d{1,3})\b`), prefixed("IRCT")},
	// the Netherlands registry, in its old (NTR) and new (NL) numbering; "NL" alone is too
	// common a token, so it is matched in capitals only
	{regexp.MustCompile(`\bNTR[\s-]?(\d{3,5})\b`), prefixed("NTR")},
	{regexp.MustCompile(`\bNL[\s-]?(\d{4,5})\b`), prefixed("NL")},
	{regexp.MustCompile(`(?i)\bPACTR[\s-]?(\d{15})\b`), prefixed("PACTR")},
}

// TrialRegistrations lists the trial registration numbers a text names, each
// normalized (uppercase, no spaces) and once, in sorted order.
func TrialRegistrations(text string) []string {
	var out []string

	for _, p := range registryPatterns {
		for _, m := range p.re.FindAllStringSubmatch(text, -1) {
			out = append(out, p.format(m))
		}
	}

	return NormalizeRegistrations(out)
}

// NormalizeRegistrations uppercases and trims numbers, drops empty ones and
// duplicates, and sorts the rest.
func NormalizeRegistrations(ids []string) []string {
	var out []string

	for _, id := range ids {
		id = strings.ToUpper(strings.Join(strings.Fields(id), ""))
		if id != "" {
			out = append(out, id)
		}
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// TrialIDs returns the paper's trial registration numbers: the ones the
// enrichment stored, or — in an artifact written before the field existed —
// the ones its abstract names, so that an old run is read the same way.
func (e *EnrichedPaper) TrialIDs() []string {
	if len(e.TrialRegistrations) > 0 {
		return e.TrialRegistrations
	}

	return TrialRegistrations(e.Abstract)
}
