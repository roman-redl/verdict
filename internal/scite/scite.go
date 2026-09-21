// Package scite is a client for Scite (https://docs.scite.ai): smart
// citations by DOI. Without a key the free tier works (tight limits);
// with a key — Authorization: Bearer.
package scite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/model"
)

const defaultBase = "https://api.scite.ai"

type Client struct {
	base string
	http *httpx.Client
	key  string
}

func New(key string) *Client { return NewWithBase(defaultBase, key) }

// NewWithBase points the client at another base URL (for tests).
func NewWithBase(base, key string) *Client {
	return &Client{base: base, http: httpx.New("scite", 1), key: key}
}

// HasKey reports whether the paid scope is configured (drill-down endpoints).
func (c *Client) HasKey() bool { return c.key != "" }

type tallyFields struct {
	Total         int `json:"total"`
	Supporting    int `json:"supporting"`
	Contradicting int `json:"contradicting"`
	Mentioning    int `json:"mentioning"`
	Unclassified  int `json:"unclassified"`
}

// Tallies returns citation counts for a batch of DOIs (API limit: 500 per
// request). The request body is a bare JSON array of DOIs (verified live:
// {"dois": [...]} is rejected with 400). Scite's response envelope has
// changed over time, so both a flat map and the {"tallies": ...} /
// {"dois": ...} wrappers are supported.
func (c *Client) Tallies(ctx context.Context, dois []string) (map[string]model.CitationTally, error) {
	if len(dois) == 0 {
		return map[string]model.CitationTally{}, nil
	}
	headers := map[string]string{}
	if c.key != "" {
		headers["Authorization"] = "Bearer " + c.key
	}
	var raw json.RawMessage
	if err := c.http.DoJSON(ctx, http.MethodPost, c.base+"/tallies", dois, &raw, headers); err != nil {
		return nil, err
	}
	return parseTallies(raw)
}

// ContradictingSources returns DOIs of papers that contradict the given one
// (drill-down: "who exactly is against"). The api_partner endpoint may
// require an extended key scope.
func (c *Client) ContradictingSources(ctx context.Context, doi string) ([]string, error) {
	headers := map[string]string{}
	if c.key != "" {
		headers["Authorization"] = "Bearer " + c.key
	}
	var resp struct {
		Citations []struct {
			SourceDOI string `json:"source"`
			Type      string `json:"type"` // supporting | contradicting | mentioning
			Section   string `json:"section"`
		} `json:"citations"`
	}
	if err := c.http.DoJSON(ctx, http.MethodGet,
		c.base+"/api_partner/citations/citing/"+doi, nil, &resp, headers); err != nil {
		return nil, err
	}
	var out []string
	for _, cit := range resp.Citations {
		if cit.Type == "contradicting" || cit.Type == "disagreeing" {
			out = append(out, model.NormalizeDOI(cit.SourceDOI))
		}
	}
	return out, nil
}

func parseTallies(raw json.RawMessage) (map[string]model.CitationTally, error) {
	// Variant 1: a flat map {"10.xxx/yyy": {...}}
	var flat map[string]tallyFields
	if err := json.Unmarshal(raw, &flat); err == nil && isDOIMap(flat) {
		return toTallies(flat), nil
	}
	// Variant 2: an envelope {"tallies": {...}} or {"dois": {...}}
	var envelope struct {
		Tallies map[string]tallyFields `json:"tallies"`
		DOIs    map[string]tallyFields `json:"dois"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("scite: unrecognized response format: %s", snippet(raw))
	}
	merged := envelope.Tallies
	if len(merged) == 0 {
		merged = envelope.DOIs
	}
	if !isDOIMap(merged) {
		return nil, fmt.Errorf("scite: no tallies in the response")
	}
	return toTallies(merged), nil
}

func isDOIMap(m map[string]tallyFields) bool {
	for k := range m {
		return strings.HasPrefix(k, "10.")
	}
	return false
}

func toTallies(m map[string]tallyFields) map[string]model.CitationTally {
	out := make(map[string]model.CitationTally, len(m))
	for doi, t := range m {
		if !strings.HasPrefix(doi, "10.") {
			continue // service keys like "status"
		}
		d := model.NormalizeDOI(doi)
		out[d] = model.CitationTally{
			DOI:           d,
			Total:         t.Total,
			Supporting:    t.Supporting,
			Contradicting: t.Contradicting,
			Mentioning:    t.Mentioning,
			Unclassified:  t.Unclassified,
		}
	}
	return out
}

func snippet(raw json.RawMessage) string {
	if len(raw) > 200 {
		return string(raw[:200]) + "..."
	}
	return string(raw)
}
