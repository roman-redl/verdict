// Package doi resolves a DOI to the URL its publisher registered for it,
// through the doi.org handle API (a JSON answer: no redirect to follow and no
// request to the publisher's site, which may sit behind a challenge page). It
// implements pipeline.Landings.
package doi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.Landings = (*Resolver)(nil)

const (
	defaultBase       = "https://doi.org"
	requestsPerSecond = 5
	// handleTypeURL is the handle value that holds the landing page address.
	handleTypeURL = "URL"
)

type Resolver struct {
	base string
	http *httpx.Client
}

func New() *Resolver { return NewWithBase(defaultBase) }

// NewWithBase points the resolver at another base URL (for tests).
func NewWithBase(base string) *Resolver {
	return &Resolver{base: base, http: httpx.New("doi", requestsPerSecond)}
}

// LandingURL returns the URL registered for the DOI; "" when doi.org does not
// know it (a 404) or holds no URL for it.
func (r *Resolver) LandingURL(ctx context.Context, doi string) (string, error) {
	doi = model.NormalizeDOI(doi)
	if doi == "" {
		return "", nil
	}

	u, err := httpx.URL(r.base, nil, "api", "handles", doi)
	if err != nil {
		return "", err
	}

	var resp struct {
		Values []struct {
			Type string `json:"type"`
			Data struct {
				Value json.RawMessage `json:"value"` // a string for the URL value, an object for the others
			} `json:"data"`
		} `json:"values"`
	}

	err = r.http.DoJSON(ctx, http.MethodGet, u, nil, &resp, nil)
	if se, ok := errors.AsType[*httpx.StatusError](err); ok && se.Status == http.StatusNotFound {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	for _, v := range resp.Values {
		if v.Type != handleTypeURL {
			continue
		}

		var landing string
		if err := json.Unmarshal(v.Data.Value, &landing); err != nil {
			return "", fmt.Errorf("reading the URL registered for %s: %w", doi, err)
		}

		return landing, nil
	}

	return "", nil
}
