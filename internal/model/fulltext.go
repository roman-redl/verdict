package model

import (
	"regexp"
	"strings"
)

// HostType says what kind of host serves a location of a work. A repository
// (PubMed Central, a university archive, a preprint server) usually serves
// the file without a challenge page; a publisher's site often does not.
type HostType string

const (
	HostPublisher  HostType = "publisher"
	HostRepository HostType = "repository"
	// HostUnknown is a location the source gave no usable host type for.
	HostUnknown HostType = "unknown"
)

// ParseHostType maps the host type of a metadata source (OpenAlex's source
// type: journal, repository, conference, ebook platform, book series,
// metadata, other) onto the two kinds the full-text fetch cares about.
// Anything else is HostUnknown: an unfamiliar value is no reason to drop the
// location.
func ParseHostType(sourceType string) HostType {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "repository":
		return HostRepository
	case "journal", "conference", "ebook platform", "book series":
		return HostPublisher
	}

	return HostUnknown
}

// Location is one place a work can be read, as the metadata source lists it:
// every location is kept, because the first one OpenAlex names is often the
// publisher's paywall or challenge page while a repository copy sits next to
// it.
type Location struct {
	PDFURL     string   `json:"pdf_url,omitempty"`
	LandingURL string   `json:"landing_url,omitempty"`
	Host       HostType `json:"host,omitempty"`
	OA         bool     `json:"oa,omitempty"` // the source marks this copy as open access
}

// FullTextLookup is what a full-text source answers for one paper.
type FullTextLookup struct {
	// Text is the plain text; empty when the source serves none.
	Text string
	// PMCID is the paper's PubMed Central ID when the source knows it, even
	// when it serves no text: another service can use it.
	PMCID string
	// Reason says why Text is empty (data, not an error); empty when there is
	// text.
	Reason string
}

// pmcidRe is a PubMed Central ID with its prefix: PMC and digits.
var pmcidRe = regexp.MustCompile(`(?i)^pmc(\d+)$`)

// pmcURLRe finds the PMC number in the two address forms of a PMC article:
// the old www.ncbi.nlm.nih.gov/pmc/articles/<id> (the number with or without
// the prefix) and pmc.ncbi.nlm.nih.gov/articles/PMC<id>.
var pmcURLRe = regexp.MustCompile(
	`(?i)^https?://(?:www\.ncbi\.nlm\.nih\.gov/pmc|pmc\.ncbi\.nlm\.nih\.gov)/articles/(?:pmc)?(\d+)`)

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

// PMCIDFromURL reads the PMCID out of the address of a PMC article; "" for
// any other URL.
func PMCIDFromURL(u string) string {
	if m := pmcURLRe.FindStringSubmatch(strings.TrimSpace(u)); m != nil {
		return "PMC" + m[1]
	}

	return ""
}

// pmcPageBase is the current address of PMC article pages (the old
// www.ncbi.nlm.nih.gov/pmc one answers a reCAPTCHA to a reader service).
const pmcPageBase = "https://pmc.ncbi.nlm.nih.gov"

// PMCPageURL is the address of a PMC article page; "" when pmcid is not a
// PMCID.
func PMCPageURL(pmcid string) string {
	id := NormalizePMCID(pmcid)
	if id == "" {
		return ""
	}

	// the id is "PMC" and digits (checked above), the base a constant: nothing
	// to escape, so a plain join is exact
	return pmcPageBase + "/articles/" + id + "/"
}
