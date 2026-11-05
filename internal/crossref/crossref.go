// Package crossref is a client for the Crossref REST API (api.crossref.org):
// update notices by DOI. The Retraction Watch Database lives inside Crossref,
// so a work's updated-by list is the authoritative retraction signal — it
// typically leads OpenAlex's is_retracted. Free, no key; a mailto is polite.
// It implements pipeline.Notices.
package crossref

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.Notices = (*Client)(nil)

const (
	defaultBase       = "https://api.crossref.org"
	requestsPerSecond = 2
)

type Client struct {
	base  string
	http  *httpx.Client
	email string
}

func New(email string) *Client { return NewWithBase(defaultBase, email) }

// NewWithBase points the client at another base URL (for tests).
func NewWithBase(base, email string) *Client {
	return &Client{base: base, http: httpx.New("crossref", requestsPerSecond), email: email}
}

// update is one notice linked to a work (verified live on the Wakefield DOI):
// {"DOI": "...", "type": "retraction" | "correction", "label": "Retraction",
//
//	"source": "retraction-watch", ...}.
type update struct {
	DOI   string `json:"DOI"`
	Type  string `json:"type"`
	Label string `json:"label"`
}

// Updates returns the notices linked to the work. No updates = a clean
// record as far as Crossref knows; an unknown DOI (404) is the same.
func (c *Client) Updates(ctx context.Context, doi string) ([]model.Notice, error) {
	q := url.Values{}
	if c.email != "" {
		q.Set("mailto", c.email)
	}

	u, err := httpx.URL(c.base, q, "works", doi)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Message struct {
			UpdatedBy []update `json:"updated-by"`
		} `json:"message"`
	}

	if err := c.http.DoJSON(ctx, http.MethodGet, u, nil, &resp, nil); err != nil {
		if se, ok := errors.AsType[*httpx.StatusError](err); ok && se.Status == http.StatusNotFound {
			return nil, nil // method contract: an unknown DOI is "no data", not an error
		}

		return nil, err
	}

	out := make([]model.Notice, len(resp.Message.UpdatedBy))
	for i, up := range resp.Message.UpdatedBy {
		out[i] = model.Notice{DOI: up.DOI, Type: up.Type, Label: up.Label}
	}

	return out, nil
}
