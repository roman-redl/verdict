// Package openalex is a client for OpenAlex (api.openalex.org): retraction
// flags, open-access links, abstracts, citation counts. No key required;
// a mailto enables the "polite pool". It implements pipeline.Works.
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
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.Works = (*Client)(nil)

const defaultBase = "https://api.openalex.org"

// Query parameter names shared by several endpoints.
const (
	paramFilter  = "filter"
	paramPerPage = "per-page"
)

const (
	// idsPerRequest is how many work IDs one ByIDs request resolves.
	idsPerRequest = 50
	// requestsPerSecond is the polite-pool rate.
	requestsPerSecond = 8
)

type Client struct {
	base  string
	http  *httpx.Client
	email string
}

func New(email string) *Client { return NewWithBase(defaultBase, email) }

// NewWithBase points the client at another base URL (for tests).
func NewWithBase(base, email string) *Client {
	return &Client{base: base, http: httpx.New("openalex", requestsPerSecond), email: email}
}

// work is the API's representation of a work.
type work struct {
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
func (c *Client) ByDOI(ctx context.Context, doi string) (*model.Work, error) {
	u, err := c.url(nil, "works", "doi:"+doi)
	if err != nil {
		return nil, err
	}

	var w work

	err = c.http.DoJSON(ctx, http.MethodGet, u, nil, &w, nil)

	if se, ok := errors.AsType[*httpx.StatusError](err); ok && se.Status == http.StatusNotFound {
		return nil, nil //nolint:nilnil // method contract: "no data" is not an error, the caller checks for nil
	}

	if err != nil {
		return nil, err
	}

	m := w.toModel()

	return &m, nil
}

// url builds an API URL; the polite-pool mailto is added to every request.
func (c *Client) url(q url.Values, segments ...string) (string, error) {
	if q == nil {
		q = url.Values{}
	}

	if c.email != "" {
		q.Set("mailto", c.email)
	}

	return httpx.URL(c.base, q, segments...)
}

// list fetches a /works listing.
func (c *Client) list(ctx context.Context, q url.Values) ([]work, error) {
	u, err := c.url(q, "works")
	if err != nil {
		return nil, err
	}

	var resp struct {
		Results []work `json:"results"`
	}

	if err := c.http.DoJSON(ctx, http.MethodGet, u, nil, &resp, nil); err != nil {
		return nil, err
	}

	return resp.Results, nil
}

// Search is a free relevance search over title/abstract/fulltext: the free
// discovery fallback with weaker semantics than a semantic engine and zero
// cost. Retractions are filtered out client-side.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]model.Paper, error) {
	if limit < 1 {
		limit = 10
	}
	// OpenAlex treats * and ? as wildcards and rejects them in stemmed search;
	// natural-language questions legitimately end with "?" — strip both.
	query = strings.NewReplacer("*", "", "?", "").Replace(query)

	works, err := c.list(ctx, url.Values{"search": {query}, paramPerPage: {strconv.Itoa(limit)}})
	if err != nil {
		return nil, err
	}

	papers := make([]model.Paper, 0, len(works))

	for i := range works {
		if works[i].IsRetracted {
			continue
		}

		m := works[i].toModel()
		papers = append(papers, m.ToPaper(""))
	}

	return papers, nil
}

// SearchTitle finds a work by title (filter=title.search) — DOI resolution
// for pastes that carry titles only. Returns (nil, nil) when nothing matches.
func (c *Client) SearchTitle(ctx context.Context, title string) (*model.Work, error) {
	// The filter value must be double-quoted when it may contain commas:
	// OpenAlex rejects a %2C-encoded comma with HTTP 400 even though its
	// error text suggests percent-encoding (verified live 2026-09-08) —
	// only the quoted form works.
	works, err := c.list(ctx, url.Values{paramFilter: {`title.search:"` + title + `"`}, paramPerPage: {"1"}})
	if err != nil {
		return nil, err
	}

	if len(works) == 0 {
		return nil, nil //nolint:nilnil // method contract: "no data" is not an error, the caller checks for nil
	}

	m := works[0].toModel()

	return &m, nil
}

// CitesWork returns works citing the given OpenAlex work ID (W...) — the
// forward half of citation chaining (newer evidence building on a seed) —
// most-cited first.
func (c *Client) CitesWork(ctx context.Context, workID string, limit int) ([]model.Work, error) {
	if limit < 1 {
		limit = 25
	}

	works, err := c.list(ctx, url.Values{
		paramFilter:  {"cites:" + workID},
		"sort":       {"cited_by_count:desc"},
		paramPerPage: {strconv.Itoa(limit)},
	})
	if err != nil {
		return nil, err
	}

	return toModels(works), nil
}

// ByIDs resolves OpenAlex work IDs (W...) to works, 50 per request.
func (c *Client) ByIDs(ctx context.Context, ids []string) ([]model.Work, error) {
	var out []model.Work

	for i := 0; i < len(ids); i += idsPerRequest {
		end := min(i+idsPerRequest, len(ids))

		works, err := c.list(ctx, url.Values{
			paramFilter:  {"ids.openalex:" + strings.Join(ids[i:end], "|")},
			paramPerPage: {strconv.Itoa(idsPerRequest)},
		})
		if err != nil {
			return nil, err
		}

		out = append(out, toModels(works)...)
	}

	return out, nil
}

// abstract reconstructs the text from abstract_inverted_index
// (OpenAlex stores abstracts as word → positions).
func (w *work) abstract() string {
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

func toModels(works []work) []model.Work {
	out := make([]model.Work, len(works))
	for i := range works {
		out[i] = works[i].toModel()
	}

	return out
}

// toModel converts the API representation into the domain record.
func (w *work) toModel() model.Work {
	var authors []string

	for _, a := range w.Authorships {
		if a.Author.DisplayName != "" {
			authors = append(authors, a.Author.DisplayName)
		}
	}

	m := model.Work{
		ID:           w.ID,
		DOI:          w.DOI,
		Title:        w.DisplayName,
		Year:         w.PublicationYear,
		Authors:      authors,
		Abstract:     w.abstract(),
		OAURL:        w.OpenAccess.OAURL,
		CitedByCount: w.CitedByCount,
		Retracted:    w.IsRetracted,
		References:   w.ReferencedWorks,
	}

	if loc := w.PrimaryLocation; loc != nil {
		m.PDFURL = loc.PDFURL

		if loc.Source != nil {
			m.Venue = loc.Source.DisplayName
		}
	}

	return m
}
