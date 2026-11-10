package europepmc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/jats"
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
// An empty Text with a Reason means Europe PMC has no open-access full text
// for the DOI: not indexed, no PMCID, not in the open-access subset, or the
// full text is not served. The PMCID comes back whenever the DOI has one, so
// that PubMed Central can be asked directly.
func (c *Client) FullText(ctx context.Context, doi string, maxChars int) (model.FullTextLookup, error) {
	records, err := c.search(ctx, []string{doi})
	if err != nil {
		return model.FullTextLookup{}, err
	}

	pmcid := openAccessPMCID(records, doi)
	if pmcid == "" {
		return noOpenAccess(records, doi), nil
	}

	u, err := httpx.URL(c.base, nil, pmcid, "fullTextXML")
	if err != nil {
		return model.FullTextLookup{}, err
	}

	data, err := c.http.Get(ctx, u, maxXMLBytes)
	if err != nil {
		if se, ok := errors.AsType[*httpx.StatusError](err); ok && se.Status == http.StatusNotFound {
			return model.FullTextLookup{PMCID: pmcid, Reason: pmcid + " full text is not served (404)"}, nil
		}

		return model.FullTextLookup{PMCID: pmcid}, err
	}

	text, err := jats.Text(data)
	if err != nil {
		return model.FullTextLookup{PMCID: pmcid}, fmt.Errorf("converting the %s full text: %w", pmcid, err)
	}

	if text == "" {
		return model.FullTextLookup{PMCID: pmcid, Reason: pmcid + " XML has no body"}, nil
	}

	return model.FullTextLookup{Text: jats.Clip(text, maxChars), PMCID: pmcid}, nil
}

// noOpenAccess explains an answer without an open-access PMCID, and passes on
// the PMCID of a record outside the open-access subset when there is one.
func noOpenAccess(records []record, doi string) model.FullTextLookup {
	want := model.NormalizeDOI(doi)
	known, best, bestRank := false, "", 0

	for _, r := range records {
		if model.NormalizeDOI(r.DOI) != want {
			continue
		}

		known = true

		if rank := sourceRank(r.Source); r.PMCID != "" && (best == "" || rank < bestRank) {
			best, bestRank = r.PMCID, rank
		}
	}

	switch {
	case !known:
		return model.FullTextLookup{Reason: "DOI not indexed"}
	case best == "":
		return model.FullTextLookup{Reason: "no PMCID"}
	}

	return model.FullTextLookup{PMCID: best, Reason: best + " is not in the open-access subset"}
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
