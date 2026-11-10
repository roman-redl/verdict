package model

import (
	"regexp"
	"strings"
)

// pmcidRe is a PubMed Central ID with its prefix: PMC and digits.
var pmcidRe = regexp.MustCompile(`(?i)^pmc(\d+)$`)

// NormalizePMCID canonicalizes a PMCID to "PMC<digits>"; "" when the input is
// not one (a bare number is accepted).
func NormalizePMCID(s string) string {
	s = strings.TrimSpace(s)

	if m := pmcidRe.FindStringSubmatch(s); m != nil {
		return "PMC" + m[1]
	}

	if s != "" && strings.Trim(s, "0123456789") == "" {
		return "PMC" + s
	}

	return ""
}
