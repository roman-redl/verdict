// Package consensus is a client for the Consensus API
// (api.consensus.app/v1): semantic ("by meaning") paper search — the mode B
// discovery engine. Behind CONSENSUS_API_KEY (the Pro tier includes 500
// uses/mo); every search request plausibly consumes one use, so callers walk
// pages deliberately and record the count.
package consensus

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/model"
)

const defaultBase = "https://api.consensus.app/v1"

type Client struct {
	base string
	http *httpx.Client
	key  string
}

func New(key string) *Client { return NewWithBase(defaultBase, key) }

// NewWithBase points the client at another base URL (for tests).
func NewWithBase(base, key string) *Client {
	return &Client{base: base, http: httpx.New("consensus", 1), key: key}
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
	u := c.base + "/search?query=" + url.QueryEscape(p.Query)
	if p.Page != 0 {
		u += "&page=" + strconv.Itoa(p.Page)
	}

	if p.PageSize != 0 {
		u += "&page_size=" + strconv.Itoa(p.PageSize)
	}

	if p.YearMin != 0 {
		u += "&year_min=" + strconv.Itoa(p.YearMin)
	}

	if p.YearMax != 0 {
		u += "&year_max=" + strconv.Itoa(p.YearMax)
	}

	if len(p.StudyTypes) > 0 {
		u += "&study_types=" + url.QueryEscape(strings.Join(p.StudyTypes, ","))
	}

	if p.Human {
		u += "&human=true"
	}

	if p.SampleSizeMin != 0 {
		u += "&sample_size_min=" + strconv.Itoa(p.SampleSizeMin)
	}

	if p.ExcludePreprints {
		u += "&exclude_preprints=true"
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
		Source:       "consensus",
		SJRQuartile:  string(r.SJRQuartile),
		Takeaway:     r.Takeaway,
	}
}
