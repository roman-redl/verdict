// Package s2 is a client for the Semantic Scholar Graph API
// (api.semanticscholar.org/graph/v1): a second metadata source alongside
// OpenAlex. Free without a key (anonymous rate limits are tight — one
// batched request per run); an optional key raises them. Adds what OpenAlex
// lacks: TLDRs, abstracts for works OpenAlex misses, influential-citation
// counts and a legal open-access PDF link. It implements pipeline.Scholar.
package s2

import (
	"context"
	"net/http"
	"net/url"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.Scholar = (*Client)(nil)

const defaultBase = "https://api.semanticscholar.org/graph/v1"

// fields are the paper fields the pipeline consumes from S2.
const fields = "externalIds,title,year,abstract,tldr,citationCount,influentialCitationCount,openAccessPdf"

type Client struct {
	base string
	http *httpx.Client
	key  string
}

func New(key string) *Client { return NewWithBase(defaultBase, key) }

// NewWithBase points the client at another base URL (for tests).
func NewWithBase(base, key string) *Client {
	return &Client{base: base, http: httpx.New("s2", 1), key: key}
}

type paper struct {
	ExternalIDs struct {
		DOI string `json:"DOI"`
	} `json:"externalIds"`
	Title    string `json:"title"`
	Year     int    `json:"year"`
	Abstract string `json:"abstract"`
	TLDR     *struct {
		Text string `json:"text"`
	} `json:"tldr"`
	CitationCount        int `json:"citationCount"`
	InfluentialCitations int `json:"influentialCitationCount"`
	OpenAccessPDF        *struct {
		URL string `json:"url"`
	} `json:"openAccessPdf"`
}

// record converts an API paper into the domain record.
func (p *paper) record() model.ScholarRecord {
	r := model.ScholarRecord{Abstract: p.Abstract, InfluentialCitations: p.InfluentialCitations}

	if p.TLDR != nil {
		r.TLDR = p.TLDR.Text
	}

	if p.OpenAccessPDF != nil {
		r.OAPDF = p.OpenAccessPDF.URL
	}

	return r
}

// Lookup fetches records by DOI in one batched request, keyed by normalized
// DOI. Unknown DOIs are absent — "no data", not an error.
func (c *Client) Lookup(ctx context.Context, dois []string) (map[string]model.ScholarRecord, error) {
	if len(dois) == 0 {
		return map[string]model.ScholarRecord{}, nil
	}

	papers, err := c.batch(ctx, dois)
	if err != nil {
		return nil, err
	}

	out := make(map[string]model.ScholarRecord, len(papers))

	for i, d := range dois {
		if i >= len(papers) {
			break
		}

		if p := papers[i]; p != nil {
			out[model.NormalizeDOI(d)] = p.record()
		}
	}

	return out, nil
}

// batch asks for the papers; the answer is index-aligned with dois, with a
// null for each unknown one.
func (c *Client) batch(ctx context.Context, dois []string) ([]*paper, error) {
	ids := make([]string, len(dois))
	for i, d := range dois {
		ids[i] = "DOI:" + d
	}

	headers := map[string]string{}
	if c.key != "" {
		headers["x-api-key"] = c.key
	}

	u, err := httpx.URL(c.base, url.Values{"fields": {fields}}, "paper", "batch")
	if err != nil {
		return nil, err
	}

	var out []*paper

	err = c.http.DoJSON(ctx, http.MethodPost, u, map[string][]string{"ids": ids}, &out, headers)
	if err != nil {
		return nil, err
	}

	return out, nil
}
