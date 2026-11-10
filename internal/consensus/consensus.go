// Package consensus is a client for the Consensus API
// (api.consensus.app/v1): semantic ("by meaning") paper search — the mode B
// discovery engine. Behind CONSENSUS_API_KEY (the Pro tier includes 500
// uses/mo); a search spends one use per 100 papers it returns, rounded up, so
// callers walk pages deliberately and record the count.
package consensus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.SemanticSearch = (*Client)(nil)

const defaultBase = "https://api.consensus.app/v1"

type Client struct {
	base string
	http *httpx.Client
	key  string
	// pageSize is the page_size every search asks for; 0 leaves it to the
	// server's default.
	pageSize int
}

// New builds a client; pageSize is the page_size of every search (the plan
// may cap it: the size actually applied comes back in each page).
func New(key string, pageSize int) *Client { return NewWithBase(defaultBase, key, pageSize) }

// NewWithBase points the client at another base URL (for tests).
func NewWithBase(base, key string, pageSize int) *Client {
	return &Client{base: base, http: httpx.New("consensus", 1), key: key, pageSize: pageSize}
}

// HasKey reports whether the API key is configured (mode B available).
func (c *Client) HasKey() bool { return c.key != "" }

// SearchParams is a query plus the filter subset the pipeline uses. Zero
// values are omitted from the request; extend field by field.
type SearchParams struct {
	Query    string // required
	Page     int    // 0-indexed; (page+1)*page_size <= 1000 is enforced server-side
	PageSize int    // 0 = server default (20); the plan may cap it lower —
	// the APPLIED value comes back in the response
	YearMin, YearMax int
	StudyTypes       []string // enum: "rct", "meta-analysis", "systematic review", ...
	Human            bool
	SampleSizeMin    int
	ExcludePreprints bool
}

// QueryResult is one paper as the search returns it (the subset the pipeline
// maps; full_text_chunks deliberately skipped — follow-up).
type QueryResult struct {
	Title         string   `json:"title"`
	Authors       []string `json:"authors"`
	DOI           string   `json:"doi"`
	Abstract      string   `json:"abstract"`
	JournalName   string   `json:"journal_name"`
	PublishYear   int      `json:"publish_year"`
	URL           string   `json:"url"` // a consensus.app paper link — NOT an OA PDF (do not map to OALink)
	CitationCount int      `json:"citation_count"`
	StudyType     string   `json:"study_type"`
	SampleSize    int      `json:"sample_size"`
	IsPreprint    bool     `json:"is_preprint"`
	SJRQuartile   Quartile `json:"sjr_best_quartile"`
	Takeaway      string   `json:"takeaway"` // AI one-liner for humans; never critic/judge input
}

// Quartile accepts the journal quartile as "Q1"/"Q2"... or as a bare number
// (the API returns one form, the CSV export the other).
type Quartile string

// UnmarshalJSON normalizes both wire forms to "Q<n>" ("" when absent).
func (q *Quartile) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*q = ""
		return nil
	}

	if s[0] >= '0' && s[0] <= '9' {
		*q = Quartile("Q" + s)
		return nil
	}

	*q = Quartile(s)

	return nil
}

// Page is one response plus the envelope facts a page-walker needs.
type Page struct {
	Results  []QueryResult
	PageSize int // as applied by the server (plan cap echo) — trust this, not the request
	IsEnd    bool
	NextPage int // 0 when the ranking ended (is_end)
}

// SearchPage fetches one page of semantic search results. page_size in the
// response is the value the plan actually applied — walk with it (and
// is_end/next_page), never with the requested size.
func (c *Client) SearchPage(ctx context.Context, p SearchParams) (*Page, error) {
	q := url.Values{"query": {p.Query}}
	setInt := func(key string, v int) {
		if v != 0 {
			q.Set(key, strconv.Itoa(v))
		}
	}

	setInt("page", p.Page)
	setInt("page_size", p.PageSize)
	setInt("year_min", p.YearMin)
	setInt("year_max", p.YearMax)
	setInt("sample_size_min", p.SampleSizeMin)

	if len(p.StudyTypes) > 0 {
		q.Set("study_types", strings.Join(p.StudyTypes, ","))
	}

	if p.Human {
		q.Set("human", "true")
	}

	if p.ExcludePreprints {
		q.Set("exclude_preprints", "true")
	}

	u, err := httpx.URL(c.base, q, "search")
	if err != nil {
		return nil, err
	}

	var resp struct {
		Results  []QueryResult `json:"results"`
		PageSize int           `json:"page_size"`
		IsEnd    bool          `json:"is_end"`
		NextPage int           `json:"next_page"`
	}

	if err := c.http.DoJSON(ctx, http.MethodGet, u, nil, &resp,
		map[string]string{"x-api-key": c.key}); err != nil {
		return nil, err // *httpx.StatusError propagates: 401/403/422 fail loudly
	}

	return &Page{
		Results: resp.Results, PageSize: resp.PageSize,
		IsEnd: resp.IsEnd, NextPage: resp.NextPage,
	}, nil
}

// Page implements pipeline.SemanticSearch: one 0-indexed page of results for
// the query, mapped to corpus papers. A 403 on a page after the first is the
// Free plan refusing pagination: it comes back as pipeline.ErrPaginationDenied
// (a 403 on the first page is a bad key or an exhausted quota and stays an
// error as is).
func (c *Client) Page(ctx context.Context, query string, page int) (model.SearchPage, error) {
	pg, err := c.SearchPage(ctx, SearchParams{Query: query, Page: page, PageSize: c.pageSize})
	if err != nil {
		if se, ok := errors.AsType[*httpx.StatusError](err); ok && page > 0 && se.Status == http.StatusForbidden {
			return model.SearchPage{}, fmt.Errorf("page %d: %w: %w", page, pipeline.ErrPaginationDenied, err)
		}

		return model.SearchPage{}, err
	}

	papers := make([]model.Paper, len(pg.Results))
	for i := range pg.Results {
		papers[i] = pg.Results[i].ToPaper()
	}

	return model.SearchPage{
		Papers: papers, IsEnd: pg.IsEnd, PageSize: pg.PageSize, PageSizeAsked: c.pageSize,
	}, nil
}

// ToPaper converts a result into the domain model. OALink stays empty on
// purpose: r.URL is not an open-access PDF, and enrichment backfills a real
// OA link from OpenAlex (source "consensus" triggers the ByDOI lookup there).
func (r *QueryResult) ToPaper() model.Paper {
	return model.Paper{
		DOI:          model.NormalizeDOI(r.DOI),
		Title:        r.Title,
		Year:         r.PublishYear,
		Authors:      r.Authors,
		Venue:        r.JournalName,
		Abstract:     r.Abstract,
		StudyDesign:  r.StudyType,
		SampleSize:   r.SampleSize,
		CitedByCount: r.CitationCount,
		Source:       model.SourceConsensus,
		SJRQuartile:  string(r.SJRQuartile),
		Takeaway:     r.Takeaway,
	}
}
