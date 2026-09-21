// Package s2 is a client for the Semantic Scholar Graph API
// (api.semanticscholar.org/graph/v1): a second metadata source alongside
// OpenAlex. Free without a key (anonymous rate limits are tight — one
// batched request per run); an optional key raises them. Adds what OpenAlex
// lacks: TLDRs, abstracts for works OpenAlex misses, influential-citation
// counts and a legal open-access PDF link.
package s2

import (
	"context"
	"net/http"

	"github.com/roman-redl/verdict/internal/httpx"
)

const defaultBase = "https://api.semanticscholar.org/graph/v1"

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

type Paper struct {
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

// TLDRText returns the one-sentence summary, "" when absent.
func (p *Paper) TLDRText() string {
	if p == nil || p.TLDR == nil {
		return ""
	}
	return p.TLDR.Text
}

// Batch fetches papers by DOI in one request. Unknown DOIs come back as
// nil in their position — "no data", not an error.
func (c *Client) Batch(ctx context.Context, dois []string) ([]*Paper, error) {
	if len(dois) == 0 {
		return nil, nil
	}
	ids := make([]string, len(dois))
	for i, d := range dois {
		ids[i] = "DOI:" + d
	}
	headers := map[string]string{}
	if c.key != "" {
		headers["x-api-key"] = c.key
	}
	var out []*Paper
	err := c.http.DoJSON(ctx, http.MethodPost,
		c.base+"/paper/batch?fields=externalIds,title,year,abstract,tldr,citationCount,influentialCitationCount,openAccessPdf",
		map[string][]string{"ids": ids}, &out, headers)
	if err != nil {
		return nil, err
	}
	return out, nil
}
