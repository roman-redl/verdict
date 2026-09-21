// Package crossref is a client for the Crossref REST API (api.crossref.org):
// update notices by DOI. The Retraction Watch Database lives inside Crossref,
// so a work's updated-by list is the authoritative retraction signal — it
// typically leads OpenAlex's is_retracted. Free, no key; a mailto is polite.
package crossref

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/roman-redl/verdict/internal/httpx"
)

const defaultBase = "https://api.crossref.org"

type Client struct {
	base  string
	http  *httpx.Client
	email string
}

func New(email string) *Client { return NewWithBase(defaultBase, email) }

// NewWithBase points the client at another base URL (for tests).
func NewWithBase(base, email string) *Client {
	return &Client{base: base, http: httpx.New("crossref", 2), email: email}
}

// Update is one notice linked to a work (verified live on the Wakefield DOI):
// {"DOI": "...", "type": "retraction" | "correction", "label": "Retraction",
//
//	"source": "retraction-watch", ...}.
type Update struct {
	DOI   string `json:"DOI"`
	Type  string `json:"type"`
	Label string `json:"label"`
}

// IsRetraction reports whether the update is a retraction notice.
func (u Update) IsRetraction() bool { return u.Type == "retraction" }

// Updates returns the notices linked to the work. No updates = a clean
// record as far as Crossref knows.
func (c *Client) Updates(ctx context.Context, doi string) ([]Update, error) {
	u := c.base + "/works/" + url.PathEscape(doi)
	if c.email != "" {
		u += "?mailto=" + url.QueryEscape(c.email)
	}
	var resp struct {
		Message struct {
			UpdatedBy []Update `json:"updated-by"`
		} `json:"message"`
	}
	if err := c.http.DoJSON(ctx, http.MethodGet, u, nil, &resp, nil); err != nil {
		var se *httpx.StatusError
		if errors.As(err, &se) && se.Status == http.StatusNotFound {
			return nil, nil // unknown DOI is "no data", not an error
		}
		return nil, err
	}
	return resp.Message.UpdatedBy, nil
}
