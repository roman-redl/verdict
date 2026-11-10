// Package jina is a client for Jina Reader (r.jina.ai), a third-party service
// that fetches a web page or a PDF with a real browser and returns it as
// markdown. It is the last resort of the full-text fetch: publishers that
// answer a plain download with a landing page or a Cloudflare challenge often
// let the reader through. The service sees the URLs it is asked to read
// (addresses of scientific papers, never personal data). It implements
// pipeline.PageReader.
//
// A reader returns whatever the target answered — including a challenge page
// with HTTP 200 — so the client judges the content, not the status: see
// check.go.
package jina

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.PageReader = (*Client)(nil)

const (
	defaultBase = "https://r.jina.ai"
	// anonPerMinute and keyedPerMinute are the service's limits without an API
	// key and with a free one.
	anonPerMinute  = 20
	keyedPerMinute = 500
)

type Client struct {
	base string
	key  string // secret: only ever sent as the Authorization header, never logged or put in an error
	http *httpx.Client
}

// New returns a client; without a key the anonymous limit (20 requests a
// minute) applies, with a free key the higher one (500).
func New(key string) *Client {
	perMinute := anonPerMinute
	if key != "" {
		perMinute = keyedPerMinute
	}

	return NewWithBase(defaultBase, key, perMinute)
}

// NewWithBase points the client at another base URL with another request rate
// (for tests).
func NewWithBase(base, key string, perMinute int) *Client {
	return &Client{base: base, key: key, http: httpx.NewPerMinute("jina", perMinute)}
}

// PageText reads the page at pageURL and returns its text as markdown, with
// the links reduced to their text and the images dropped. It fails with a
// *RejectedError when the page is not a usable article (see Check).
func (c *Client) PageText(ctx context.Context, pageURL string) (string, error) {
	var resp struct {
		Data struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"data"`
	}

	// the URL goes in the body: as a path segment its "//" would be collapsed
	body := map[string]string{"url": pageURL}

	if err := c.http.DoJSON(ctx, http.MethodPost, c.base, body, &resp, c.headers()); err != nil {
		return "", err
	}

	text := tidy(resp.Data.Content)

	if err := Check(resp.Data.Title, text); err != nil {
		return "", err
	}

	return text, nil
}

// headers are the request headers; the key goes out only when there is one.
func (c *Client) headers() map[string]string {
	if c.key == "" {
		return nil
	}

	return map[string]string{"Authorization": "Bearer " + c.key}
}

var (
	// markdownImage is an image, which carries no evidence and a long URL.
	markdownImage = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	// markdownLink is a link; its text stays, the address (often a tracking
	// URL a hundred characters long) goes.
	markdownLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	// blankRuns is what the removals leave: runs of blank lines.
	blankRuns = regexp.MustCompile(`\n{3,}`)
)

// tidy strips the markup that costs the critic's window and says nothing: the
// images, the link addresses, the blank runs. Tables (pipe rows) are kept.
func tidy(s string) string {
	s = markdownImage.ReplaceAllString(s, "")
	s = markdownLink.ReplaceAllString(s, "$1")
	s = blankRuns.ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s)
}
