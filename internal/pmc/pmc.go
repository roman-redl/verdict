// Package pmc is a client for PubMed Central through NCBI's own services: the
// ID converter (DOI → PMCID) and efetch (the article XML). Europe PMC serves
// only the open-access subset; efetch also returns the body of some author
// manuscripts outside it, and for articles the publisher does not allow to
// download it returns the front matter alone, which this client treats as "no
// text". Free, no key. It implements pipeline.PMCArchive.
package pmc

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/jats"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.PMCArchive = (*Client)(nil)

const (
	defaultIDConvBase = "https://pmc.ncbi.nlm.nih.gov/tools/idconv/api/v1"
	defaultEfetchBase = "https://eutils.ncbi.nlm.nih.gov/entrez/eutils"
	// requestsPerSecond is NCBI's limit without an API key.
	requestsPerSecond = 3
	// maxXMLBytes caps an article response: a long article with big tables is
	// a few megabytes of XML.
	maxXMLBytes = 16 << 20
	// minBodyChars is the least text that counts as an article body: front
	// matter that leaks a stub paragraph must not pass for a full text.
	minBodyChars = 800
	// toolName identifies this program to NCBI (it asks for a tool name and an
	// e-mail on every request).
	toolName = "verdict"
)

type Client struct {
	idconvBase string
	efetchBase string
	email      string
	http       *httpx.Client
}

// New returns a client; the e-mail (optional) is passed to NCBI so that it can
// reach the owner instead of blocking the address.
func New(email string) *Client { return NewWithBases(defaultIDConvBase, defaultEfetchBase, email) }

// NewWithBases points the client at other base URLs (for tests).
func NewWithBases(idconvBase, efetchBase, email string) *Client {
	return &Client{
		idconvBase: idconvBase, efetchBase: efetchBase, email: email,
		http: httpx.New("pmc", requestsPerSecond),
	}
}

// query starts the parameters every NCBI request carries.
func (c *Client) query() url.Values {
	q := url.Values{"tool": {toolName}}
	if c.email != "" {
		q.Set("email", c.email)
	}

	return q
}

// PMCID converts a DOI to its PubMed Central ID; "" when PMC does not hold the
// paper.
func (c *Client) PMCID(ctx context.Context, doi string) (string, error) {
	doi = model.NormalizeDOI(doi)
	if doi == "" {
		return "", nil
	}

	q := c.query()
	q.Set("ids", doi)
	q.Set("format", "json")

	u, err := httpx.URL(c.idconvBase, q, "articles")
	if err != nil {
		return "", err
	}

	var resp struct {
		Records []struct {
			PMCID string `json:"pmcid"`
		} `json:"records"`
	}

	if err := c.http.DoJSON(ctx, http.MethodGet, u, nil, &resp, nil); err != nil {
		return "", err
	}

	// an unknown DOI comes back as a record with an error status and no PMCID
	for _, r := range resp.Records {
		if id := model.NormalizePMCID(r.PMCID); id != "" {
			return id, nil
		}
	}

	return "", nil
}

// FullText fetches the article XML and converts it to plain text (tables,
// results, methods, the rest), capped at maxChars characters (no cap when
// maxChars is not positive). "" means PMC serves no body text for the PMCID.
func (c *Client) FullText(ctx context.Context, pmcid string, maxChars int) (string, error) {
	id := model.NormalizePMCID(pmcid)
	if id == "" {
		return "", fmt.Errorf("%q is not a PMCID", pmcid)
	}

	q := c.query()
	q.Set("db", "pmc")
	q.Set("id", id)
	q.Set("rettype", "xml")

	u, err := httpx.URL(c.efetchBase, q, "efetch.fcgi")
	if err != nil {
		return "", err
	}

	data, err := c.http.Get(ctx, u, maxXMLBytes)
	if err != nil {
		return "", err
	}

	text, err := jats.Text(data)
	if err != nil {
		return "", fmt.Errorf("converting the %s XML: %w", id, err)
	}

	text = strings.TrimSpace(text)
	if len([]rune(text)) < minBodyChars {
		return "", nil
	}

	return jats.Clip(text, maxChars), nil
}
