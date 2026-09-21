// Package openalex is a client for OpenAlex (api.openalex.org): retraction
// flags, open-access links, abstracts, citation counts. No key required;
// a mailto enables the "polite pool".
package openalex

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/model"
)

const defaultBase = "https://api.openalex.org"

type Client struct {
	base  string
	http  *httpx.Client
	email string
}

func New(email string) *Client { return NewWithBase(defaultBase, email) }

// NewWithBase points the client at another base URL (for tests).
func NewWithBase(base, email string) *Client {
	return &Client{base: base, http: httpx.New("openalex", 8), email: email}
}

type Work struct {
	ID              string `json:"id"`
	DOI             string `json:"doi"` // arrives URL-shaped: https://doi.org/10.x/...
	DisplayName     string `json:"display_name"`
	PublicationYear int    `json:"publication_year"`
	IsRetracted     bool   `json:"is_retracted"`
	CitedByCount    int    `json:"cited_by_count"`
	OpenAccess      struct {
		IsOA  bool   `json:"is_oa"`
		OAURL string `json:"oa_url"`
	} `json:"open_access"`
	Authorships []struct {
		Author struct {
			DisplayName string `json:"display_name"`
		} `json:"author"`
	} `json:"authorships"`
	PrimaryLocation *struct {
		Source *struct {
			DisplayName string `json:"display_name"`
		} `json:"source"`
		PDFURL string `json:"pdf_url"`
	} `json:"primary_location"`
	// ReferencedWorks is the work's reference list as OpenAlex work IDs
	// (W...) — the backward half of citation chaining.
	ReferencedWorks       []string         `json:"referenced_works"`
	AbstractInvertedIndex map[string][]int `json:"abstract_inverted_index"`
}

// ByDOI returns (nil, nil) when OpenAlex does not know the DOI (404) —
// that is "no data", not an error.
func (c *Client) ByDOI(ctx context.Context, doi string) (*Work, error) {
	u := c.base + "/works/doi:" + doi
	if c.email != "" {
		u += "?mailto=" + c.email
	}
	var w Work
	err := c.http.DoJSON(ctx, http.MethodGet, u, nil, &w, nil)
	var se *httpx.StatusError
	if errors.As(err, &se) && se.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// Search is a free relevance search over title/abstract/fulltext. A drop-in
// The free discovery fallback: weaker semantics than a semantic engine,
// zero cost. Retractions are filtered out client-side.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]model.Paper, error) {
	if limit < 1 {
		limit = 10
	}
	// OpenAlex treats * and ? as wildcards and rejects them in stemmed search;
	// natural-language questions legitimately end with "?" — strip both.
	query = strings.NewReplacer("*", "", "?", "").Replace(query)
	u := c.base + "/works?search=" + url.QueryEscape(query) + "&per-page=" + strconv.Itoa(limit)
	if c.email != "" {
		u += "&mailto=" + url.QueryEscape(c.email)
	}
	var resp struct {
		Results []Work `json:"results"`
	}
	if err := c.http.DoJSON(ctx, http.MethodGet, u, nil, &resp, nil); err != nil {
		return nil, err
	}
	papers := make([]model.Paper, 0, len(resp.Results))
	for i := range resp.Results {
		if resp.Results[i].IsRetracted {
			continue
		}
		papers = append(papers, resp.Results[i].ToPaper(""))
	}
	return papers, nil
}

// SearchTitle finds a work by title (filter=title.search) — DOI resolution
// for pastes that carry titles only. Returns (nil, nil) when nothing matches.
func (c *Client) SearchTitle(ctx context.Context, title string) (*Work, error) {
	// The filter value must be double-quoted when it may contain commas:
	// OpenAlex rejects a %2C-encoded comma with HTTP 400 even though its
	// error text suggests percent-encoding (verified live 2026-09-08) —
	// only the quoted form works.
	u := c.base + "/works?filter=title.search:" + url.QueryEscape(`"`+title+`"`) + "&per-page=1"
	if c.email != "" {
		u += "&mailto=" + url.QueryEscape(c.email)
	}
	var resp struct {
		Results []Work `json:"results"`
	}
	if err := c.http.DoJSON(ctx, http.MethodGet, u, nil, &resp, nil); err != nil {
		return nil, err
	}
	if len(resp.Results) == 0 {
		return nil, nil
	}
	return &resp.Results[0], nil
}

// CitesWork returns works citing the given OpenAlex work ID (W...) — the
// forward half of citation chaining (newer evidence building on a seed) —
// most-cited first.
func (c *Client) CitesWork(ctx context.Context, openalexID string, limit int) ([]Work, error) {
	if limit < 1 {
		limit = 25
	}
	u := c.base + "/works?filter=cites:" + url.QueryEscape(openalexID) +
		"&sort=cited_by_count:desc&per-page=" + strconv.Itoa(limit)
	if c.email != "" {
		u += "&mailto=" + url.QueryEscape(c.email)
	}
	var resp struct {
		Results []Work `json:"results"`
	}
	if err := c.http.DoJSON(ctx, http.MethodGet, u, nil, &resp, nil); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// ByIDs resolves OpenAlex work IDs (W...) to works, 50 per request.
func (c *Client) ByIDs(ctx context.Context, ids []string) ([]Work, error) {
	var out []Work
	for i := 0; i < len(ids); i += 50 {
		end := min(i+50, len(ids))
		u := c.base + "/works?filter=ids.openalex:" +
			url.QueryEscape(strings.Join(ids[i:end], "|")) + "&per-page=50"
		if c.email != "" {
			u += "&mailto=" + url.QueryEscape(c.email)
		}
		var resp struct {
			Results []Work `json:"results"`
		}
		if err := c.http.DoJSON(ctx, http.MethodGet, u, nil, &resp, nil); err != nil {
			return nil, err
		}
		out = append(out, resp.Results...)
	}
	return out, nil
}

// Abstract reconstructs the text from abstract_inverted_index
// (OpenAlex stores abstracts as word → positions).
func (w *Work) Abstract() string {
	if len(w.AbstractInvertedIndex) == 0 {
		return ""
	}
	var maxPos int
	for _, idxs := range w.AbstractInvertedIndex {
		for _, i := range idxs {
			if i > maxPos {
				maxPos = i
			}
		}
	}
	words := make([]string, maxPos+1)
	for word, idxs := range w.AbstractInvertedIndex {
		for _, i := range idxs {
			words[i] = word
		}
	}
	return strings.Join(words, " ")
}

// ToPaper converts a work into the domain model. The DOI is taken from the
// work itself; the parameter is a fallback for DOI-based lookups (the ID is
// not present for all works).
func (w *Work) ToPaper(doi string) model.Paper {
	d := w.DOI
	if d == "" {
		d = doi
	}
	var authors []string
	for _, a := range w.Authorships {
		if a.Author.DisplayName != "" {
			authors = append(authors, a.Author.DisplayName)
		}
	}
	venue := ""
	if w.PrimaryLocation != nil && w.PrimaryLocation.Source != nil {
		venue = w.PrimaryLocation.Source.DisplayName
	}
	oaLink := w.OpenAccess.OAURL
	if oaLink == "" && w.PrimaryLocation != nil {
		oaLink = w.PrimaryLocation.PDFURL
	}
	return model.Paper{
		DOI:          model.NormalizeDOI(d),
		Title:        w.DisplayName,
		Year:         w.PublicationYear,
		Authors:      authors,
		Venue:        venue,
		Abstract:     w.Abstract(),
		OALink:       oaLink,
		CitedByCount: w.CitedByCount,
		Retracted:    w.IsRetracted,
		Source:       "openalex",
	}
}
