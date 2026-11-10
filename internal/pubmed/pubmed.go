// Package pubmed is a client for PubMed through NCBI's E-utilities: esearch
// (a Boolean query → PMIDs in relevance order) and esummary (PMIDs → title,
// year, publication types, DOI, PMCID). Free; an optional API key raises the
// limit from 3 to 10 requests a second. It implements pipeline.PubMed.
package pubmed

import (
	"context"
	"encoding/json"
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

var _ pipeline.PubMed = (*Client)(nil)

const (
	defaultBase = "https://eutils.ncbi.nlm.nih.gov/entrez/eutils"
	// requestsPerSecond is NCBI's limit without an API key, withKeyRPS the
	// limit with one.
	requestsPerSecond = 3
	withKeyRPS        = 10
	// summaryBatch is how many PMIDs one esummary request carries: NCBI asks
	// for no more than 200 per request, and 200 PMIDs fit a URL comfortably.
	summaryBatch = 200
	// toolName identifies this program to NCBI (it asks for a tool name and an
	// e-mail on every request).
	toolName = "verdict"
	// redacted replaces the API key in a transport error's URL.
	redacted = "REDACTED"
)

// Client talks to the E-utilities.
type Client struct {
	base  string
	key   string
	email string
	http  *httpx.Client
}

// New returns a client. The API key (optional) raises the rate limit; the
// e-mail (optional) lets NCBI reach the owner instead of blocking the address.
func New(apiKey, email string) *Client { return NewWithBase(defaultBase, apiKey, email) }

// NewWithBase points the client at another base URL (for tests).
func NewWithBase(base, apiKey, email string) *Client {
	qps := requestsPerSecond
	if apiKey != "" {
		qps = withKeyRPS
	}

	return &Client{base: base, key: apiKey, email: email, http: httpx.New("pubmed", qps)}
}

// params starts the parameters every E-utilities request carries.
func (c *Client) params() url.Values {
	q := url.Values{"tool": {toolName}, "retmode": {"json"}, "db": {"pubmed"}}
	if c.email != "" {
		q.Set("email", c.email)
	}

	if c.key != "" {
		q.Set("api_key", c.key)
	}

	return q
}

// get requests an E-utility and decodes the JSON answer. The API key sits in
// the URL, so a transport error, which quotes the URL, is stripped of it.
func (c *Client) get(ctx context.Context, util string, q url.Values, out any) error {
	u, err := httpx.URL(c.base, q, util)
	if err != nil {
		return err
	}

	if err := c.http.DoJSON(ctx, http.MethodGet, u, nil, out, nil); err != nil {
		return c.redact(err)
	}

	return nil
}

// redactedError is an error whose text has the API key cut out; the original
// stays reachable for errors.Is and errors.As.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// redact removes the API key from the text of an error: a transport error
// quotes the request URL, which carries it.
func (c *Client) redact(err error) error {
	if c.key == "" {
		return err
	}

	//nolint:forbidigo // the text of an external library's error is stripped of the secret here, once
	msg := err.Error()
	if !strings.Contains(msg, c.key) {
		return err
	}

	return &redactedError{msg: strings.ReplaceAll(msg, c.key, redacted), err: err}
}

// Search runs the query and returns the PMIDs of the best limit matches in
// relevance order, and the total number of matches.
func (c *Client) Search(ctx context.Context, query string, limit int) (model.PubMedHits, error) {
	if strings.TrimSpace(query) == "" {
		return model.PubMedHits{}, errors.New("empty PubMed query")
	}

	if limit < 1 {
		return model.PubMedHits{}, fmt.Errorf("the result limit must be positive, got %d", limit)
	}

	q := c.params()
	q.Set("term", query)
	q.Set("retmax", strconv.Itoa(limit))
	q.Set("sort", "relevance")

	var resp struct {
		Result struct {
			Count  string   `json:"count"`
			IDList []string `json:"idlist"`
			// E-utilities answer a failed search with HTTP 200 and this field.
			Error string `json:"ERROR"`
		} `json:"esearchresult"`
	}

	if err := c.get(ctx, "esearch.fcgi", q, &resp); err != nil {
		return model.PubMedHits{}, fmt.Errorf("esearch: %w", err)
	}

	if resp.Result.Error != "" {
		return model.PubMedHits{}, fmt.Errorf("esearch: PubMed rejected the query: %s", resp.Result.Error)
	}

	total, err := strconv.Atoi(resp.Result.Count)
	if err != nil {
		return model.PubMedHits{}, fmt.Errorf("esearch: reading the hit count %q: %w", resp.Result.Count, err)
	}

	return model.PubMedHits{PMIDs: resp.Result.IDList, Total: total}, nil
}

// Summaries returns the records of the PMIDs in the order given, fetched in
// batches. A PMID PubMed does not return is absent.
func (c *Client) Summaries(ctx context.Context, pmids []string) ([]model.PubMedRecord, error) {
	var out []model.PubMedRecord

	for start := 0; start < len(pmids); start += summaryBatch {
		batch := pmids[start:min(start+summaryBatch, len(pmids))]

		records, err := c.summaryBatch(ctx, batch)
		if err != nil {
			return nil, err
		}

		out = append(out, records...)
	}

	return out, nil
}

// summary is one esummary document.
type summary struct {
	UID      string   `json:"uid"`
	Title    string   `json:"title"`
	PubDate  string   `json:"pubdate"`
	SortDate string   `json:"sortpubdate"`
	PubType  []string `json:"pubtype"`
	Articles []struct {
		IDType string `json:"idtype"`
		Value  string `json:"value"`
	} `json:"articleids"`
	// Error is set on a PMID that has no document.
	Error string `json:"error"`
}

func (c *Client) summaryBatch(ctx context.Context, pmids []string) ([]model.PubMedRecord, error) {
	q := c.params()
	q.Set("id", strings.Join(pmids, ","))

	var resp struct {
		// the result holds "uids" (the order) and one document per PMID
		Result map[string]json.RawMessage `json:"result"`
		Error  string                     `json:"error"`
	}

	if err := c.get(ctx, "esummary.fcgi", q, &resp); err != nil {
		return nil, fmt.Errorf("esummary: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("esummary: PubMed rejected the request: %s", resp.Error)
	}

	var uids []string
	if raw, ok := resp.Result["uids"]; ok {
		if err := json.Unmarshal(raw, &uids); err != nil {
			return nil, fmt.Errorf("esummary: decoding the PMID list: %w", err)
		}
	}

	records := make([]model.PubMedRecord, 0, len(uids))

	for _, uid := range uids {
		var s summary
		if err := json.Unmarshal(resp.Result[uid], &s); err != nil {
			return nil, fmt.Errorf("esummary: decoding the record %s: %w", uid, err)
		}

		if s.Error != "" {
			continue // no document for this PMID
		}

		records = append(records, s.record(uid))
	}

	return records, nil
}

// record converts a document into the domain record.
func (s *summary) record(uid string) model.PubMedRecord {
	r := model.PubMedRecord{
		PMID:     uid,
		Title:    strings.TrimSuffix(strings.TrimSpace(s.Title), "."),
		Year:     year(s.PubDate, s.SortDate),
		PubTypes: s.PubType,
	}

	for _, a := range s.Articles {
		switch a.IDType {
		case "doi":
			r.DOI = model.NormalizeDOI(a.Value)
		case "pmc":
			r.PMCID = model.NormalizePMCID(a.Value)
		}
	}

	return r
}

// year reads the publication year off the first date that starts with one
// ("2018 Jan", "1996 Sep 25", "2018/01/01 00:00"); zero when none does.
func year(dates ...string) int {
	for _, d := range dates {
		if len(d) < 4 {
			continue
		}

		if y, err := strconv.Atoi(d[:4]); err == nil && y > 0 {
			return y
		}
	}

	return 0
}
