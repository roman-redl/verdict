package europepmc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.FullTexts = (*Client)(nil)

const (
	// maxXMLBytes caps a full-text response: a long article with big tables
	// is a few megabytes of JATS.
	maxXMLBytes = 16 << 20
	// flagYes is the value of Europe PMC's Y/N flags.
	flagYes = "Y"
)

// FullText returns the plain text of the paper's open-access full text,
// capped at maxChars characters (no cap when maxChars is not positive), in
// the order the converter gives it: tables, results, methods, the rest.
// ("", nil) means Europe PMC has no open-access full text for the DOI: no
// PMCID, not in the open-access subset, or the full text is not served.
func (c *Client) FullText(ctx context.Context, doi string, maxChars int) (string, error) {
	records, err := c.search(ctx, []string{doi})
	if err != nil {
		return "", err
	}

	pmcid := openAccessPMCID(records, doi)
	if pmcid == "" {
		return "", nil
	}

	u, err := httpx.URL(c.base, nil, pmcid, "fullTextXML")
	if err != nil {
		return "", err
	}

	data, err := c.http.Get(ctx, u, maxXMLBytes)
	if err != nil {
		if se, ok := errors.AsType[*httpx.StatusError](err); ok && se.Status == http.StatusNotFound {
			return "", nil
		}

		return "", err
	}

	text, err := fullTextFromXML(data)
	if err != nil {
		return "", fmt.Errorf("converting the %s full text: %w", pmcid, err)
	}

	return clip(text, maxChars), nil
}

// openAccessPMCID picks the PMCID of the DOI's open-access record: the
// journal record (MED) first, then the PMC one; "" when there is none.
// Europe PMC serves the XML only for the open-access subset of PMC, hence
// the isOpenAccess flag, not the mere presence of a PMCID.
func openAccessPMCID(records []record, doi string) string {
	want := model.NormalizeDOI(doi)
	best, bestRank := "", 0

	for _, r := range records {
		if model.NormalizeDOI(r.DOI) != want || r.PMCID == "" || r.IsOpenAccess != flagYes {
			continue
		}

		if rank := sourceRank(r.Source); best == "" || rank < bestRank {
			best, bestRank = r.PMCID, rank
		}
	}

	return best
}

// clip cuts the text at maxChars characters (runes), so a multi-byte
// character is never split.
func clip(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}

	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}

	return string(runes[:maxChars])
}
