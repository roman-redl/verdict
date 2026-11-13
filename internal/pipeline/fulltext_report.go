package pipeline

// The report section for the studies whose full text was not obtained: the
// reader gets what is needed to supply the PDF by hand.

import (
	"fmt"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// fullTextGaps lists the direct studies that have no full text, one line per
// study: title, DOI, the best page to download it from, and the file name the
// PDF must have in fulltext/. Nothing in the lean mode (no full text was
// asked for) and in artifacts written before the mode was recorded.
func fullTextGaps(st *State) []string {
	if st.TextMode != TextHybrid && st.TextMode != TextFull {
		return nil
	}

	var lines []string

	crits := critiquesFor(st)

	for i := range st.Enriched {
		e := &st.Enriched[i]
		if e.FullText != "" || crits[i].Relevance != relevanceDirect || model.NormalizeDOI(e.DOI) == "" {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s — DOI %s — %s — %#q",
			shortTitle(e.Title, 90), model.NormalizeDOI(e.DOI), bestLandingURL(e), fullTextFileName(e.DOI)))
	}

	return lines
}

// bestLandingURL is the page a person should open to get the paper: the
// landing page of a repository copy, then the publisher's, then the
// open-access link, then the DOI itself.
func bestLandingURL(e *model.EnrichedPaper) string {
	var l addressList

	for _, loc := range rankedLocations(e) {
		l.add(loc.LandingURL)
	}

	l.add(e.OALink)

	if doi := model.NormalizeDOI(e.DOI); doi != "" {
		l.add(httpsScheme + "doi.org/" + doi)
	}

	return l.list[0] // the DOI is there: fullTextGaps skips a paper without one
}

// writeFullTextGaps appends the section to the report, with the lexicon's
// title and the instruction that tells the reader how to supply a missing full
// text; nothing when there are no gaps.
func writeFullTextGaps(b *strings.Builder, lx lexicon, st *State) {
	lines := fullTextGaps(st)
	if len(lines) == 0 {
		return
	}

	b.WriteString("\n### " + lx.fullTexts + "\n\n" + lx.fullTextsIntro + "\n\n")

	for _, l := range lines {
		b.WriteString("- " + l + "\n")
	}

	b.WriteString("\n")
}
