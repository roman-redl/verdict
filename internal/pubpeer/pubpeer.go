// Package pubpeer is a client for the PubPeer API v3 (POST
// pubpeer.com/v3/publications): post-publication peer commentary by DOI.
// The API requires a developer key (issued on request at pubpeer.com), so
// the check runs only when PUBPEER_DEVKEY is set. A commentary thread is a
// SOFT signal — it is not a retraction and says nothing on its own about
// validity; it lands in RedFlags.Notes for the judge to weigh.
package pubpeer

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/roman-redl/verdict/internal/httpx"
)

const defaultBase = "https://pubpeer.com"

type Client struct {
	base   string
	http   *httpx.Client
	devkey string
}

func New(devkey string) *Client { return NewWithBase(defaultBase, devkey) }

// NewWithBase points the client at another base URL (for tests).
func NewWithBase(base, devkey string) *Client {
	return &Client{base: base, http: httpx.New("pubpeer", 1), devkey: devkey}
}

// HasKey reports whether the developer key is configured.
func (c *Client) HasKey() bool { return c.devkey != "" }

// Thread is one publication's feedback record. The shape is confirmed by
// the official Zotero plugin source (github.com/pubpeerfoundation/
// pubpeer_zotero_plugin, content/pubpeer.ts): the API returns counts and
// links, never comment bodies.
type Thread struct {
	ID            string `json:"id"` // the DOI
	Title         string `json:"title"`
	TotalComments int    `json:"total_comments"`
	URL           string `json:"url"`
	LastCommented *struct {
		Timezone string `json:"timezone"`
	} `json:"last_commented_at"`
}

type response struct {
	Feedbacks []Thread `json:"feedbacks"`
}

// Publications returns the PubPeer feedback records for the DOI (per-DOI
// requests keep the thread→paper mapping unambiguous). Empty = no
// commentary known.
func (c *Client) Publications(ctx context.Context, doi string) ([]Thread, error) {
	var resp response
	err := c.http.DoJSON(ctx, http.MethodPost, c.base+"/v3/publications",
		map[string]string{"dois": doi, "devkey": c.devkey}, &resp, nil)
	if err != nil {
		return nil, err
	}
	return resp.Feedbacks, nil
}

// Note renders the soft signal for RedFlags.Notes.
func Note(threads []Thread) string {
	if len(threads) == 0 {
		return ""
	}
	n := 0
	for _, t := range threads {
		n += t.TotalComments
	}
	return "PubPeer commentary (" + strconv.Itoa(n) + " comments): " +
		strings.Join(mapURLs(threads), ", ")
}

func mapURLs(threads []Thread) []string {
	urls := make([]string, 0, len(threads))
	for _, t := range threads {
		urls = append(urls, t.URL)
	}
	return urls
}
