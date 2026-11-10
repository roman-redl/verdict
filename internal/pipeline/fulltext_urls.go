package pipeline

// The addresses the full-text fetch tries, from the paper's locations. They
// are pure functions of the paper: no network, so the order is testable.

import (
	"regexp"
	"slices"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	httpScheme  = "http://"
	httpsScheme = "https://"
)

// upgradeHTTP turns an http:// address into https://. Publishers redirect
// plain http to https anyway, and in the environment this program runs in the
// local proxy of the session answers plain http with 503 (measured: every
// open-access link of the http kind failed that way), so the address is
// always fetched over https.
func upgradeHTTP(u string) string {
	if len(u) >= len(httpScheme) && strings.EqualFold(u[:len(httpScheme)], httpScheme) {
		return httpsScheme + u[len(httpScheme):]
	}

	return u
}

// hostRank orders the locations: a repository copy is served without a
// challenge page more often than a publisher's site. An unfamiliar host type
// (a hand-edited artifact) ranks with the unknown ones.
func hostRank(h model.HostType) int {
	switch h {
	case model.HostRepository:
		return 0
	case model.HostPublisher:
		return 1
	case model.HostUnknown:
		return 2
	}

	return 2
}

// rankedLocations returns the paper's locations, repository copies first,
// then the publisher's, then the rest, each group in the source's order.
func rankedLocations(e *model.EnrichedPaper) []model.Location {
	out := slices.Clone(e.OALocations)

	slices.SortStableFunc(out, func(a, b model.Location) int { return hostRank(a.Host) - hostRank(b.Host) })

	return out
}

// addressList collects addresses in order, https-upgraded, without empty
// ones and without repeats.
type addressList struct {
	list []string
	seen map[string]bool
}

func (l *addressList) add(u string) {
	u = upgradeHTTP(strings.TrimSpace(u))
	if u == "" || l.seen[u] {
		return
	}

	if l.seen == nil {
		l.seen = map[string]bool{}
	}

	l.seen[u] = true
	l.list = append(l.list, u)
}

// isPMCPage reports whether the address is a PMC article page rather than a
// file: the page is read through the new-format PMC address (readerCandidates)
// or through NCBI itself, and as a "PDF" it would only be an HTML download.
func isPMCPage(u string) bool {
	return model.PMCIDFromURL(u) != "" && !strings.Contains(strings.ToLower(u), "pdf")
}

// pdfCandidates lists the addresses to try as a PDF download: the PDF
// addresses of the locations (repository copies before the publisher's),
// then the open-access link, once each. The open-access link is often a
// landing page, which costs one failed download and nothing more.
func pdfCandidates(e *model.EnrichedPaper) []string {
	var l addressList

	for _, loc := range rankedLocations(e) {
		if !isPMCPage(loc.PDFURL) {
			l.add(loc.PDFURL)
		}
	}

	if !isPMCPage(e.OALink) {
		l.add(e.OALink)
	}

	return l.list
}

// abstractPageRe finds the addresses of pages that carry the abstract only:
// the PubMed record (new and old address forms) and the Europe PMC abstract
// page. A PMC article page is not among them.
var abstractPageRe = regexp.MustCompile(
	`(?i)^https?://(?:pubmed\.ncbi\.nlm\.nih\.gov(?:/|$)|(?:www\.)?ncbi\.nlm\.nih\.gov/pubmed(?:/|$)|` +
		`(?:www\.)?europepmc\.org/abstract(?:/|$))`)

// isAbstractPage reports whether the address is a page with an abstract and no
// article text: reading it costs a reader request and yields nothing the
// critic does not have already.
func isAbstractPage(u string) bool { return abstractPageRe.MatchString(strings.TrimSpace(u)) }

// readerCandidates lists the addresses to hand to the page reader: the PMC
// page when the paper has a PMCID, then the landing pages (repository copies
// before the publisher's; the DOI itself after them, resolved to the
// publisher's page by the caller), then the PDF addresses. The old-format PMC
// addresses are dropped: the reader meets a reCAPTCHA there, and the PMC page
// of the new format stands in for them. So are the abstract-only pages
// (PubMed, Europe PMC abstracts).
func readerCandidates(e *model.EnrichedPaper, pmcid string) []string {
	var l addressList

	l.add(model.PMCPageURL(pmcid))

	for _, loc := range rankedLocations(e) {
		if model.PMCIDFromURL(loc.LandingURL) == "" && !isAbstractPage(loc.LandingURL) {
			l.add(loc.LandingURL)
		}
	}

	if doi := model.NormalizeDOI(e.DOI); doi != "" {
		// an identifier, not a request: the caller takes the DOI back out
		// (doiOfURL) and the Landings port makes the request
		l.add(httpsScheme + "doi.org/" + doi)
	}

	for _, u := range pdfCandidates(e) {
		if !isPMCPage(u) && !isAbstractPage(u) {
			l.add(u)
		}
	}

	return l.list
}

// pmcidFromLinks reads a PMCID off the paper's own addresses: OpenAlex often
// lists the PubMed Central copy, and its address carries the ID, which saves
// a lookup.
func pmcidFromLinks(e *model.EnrichedPaper) string {
	if id := model.PMCIDFromURL(e.OALink); id != "" {
		return id
	}

	for _, loc := range e.OALocations {
		for _, u := range []string{loc.LandingURL, loc.PDFURL} {
			if id := model.PMCIDFromURL(u); id != "" {
				return id
			}
		}
	}

	return ""
}

// doiOfURL returns the DOI when the address is a doi.org one (the reader
// service refuses those, so the caller resolves them first).
func doiOfURL(u string) (string, bool) {
	host := strings.ToLower(hostOf(u))
	if host != "doi.org" && host != "dx.doi.org" {
		return "", false
	}

	doi := model.NormalizeDOI(u)
	if doi == "" || strings.EqualFold(doi, u) {
		return "", false
	}

	return doi, true
}
