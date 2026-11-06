// Package europepmc is a client for the Europe PMC REST API
// (www.ebi.ac.uk/europepmc/webservices/rest): abstracts by DOI and the
// open-access full texts (JATS XML, converted to plain text). OpenAlex
// withholds the abstracts of publisher-restricted works (every Elsevier DOI,
// for one); Europe PMC indexes them for the biomedical literature. Free, no
// key. It implements pipeline.Abstracts and pipeline.FullTexts.
package europepmc

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.Abstracts = (*Client)(nil)

const (
	defaultBase       = "https://www.ebi.ac.uk/europepmc/webservices/rest"
	requestsPerSecond = 5
	// batchSize is how many DOIs one search carries: the query is a GET URL,
	// and a DOI is ~30 characters.
	batchSize = 20
	// pageSize leaves room for several records per DOI (the journal record
	// plus a preprint, say).
	pageSize = 100
)

// Record sources, in the order of preference: MED is the curated PubMed
// record, PMC the full-text archive, everything else (PPR preprints, AGR, ...)
// comes last.
const (
	sourceMED = "MED"
	sourcePMC = "PMC"
)

type Client struct {
	base string
	http *httpx.Client
}

func New() *Client { return NewWithBase(defaultBase) }

// NewWithBase points the client at another base URL (for tests).
func NewWithBase(base string) *Client {
	return &Client{base: base, http: httpx.New("europepmc", requestsPerSecond)}
}

// record is one search hit (resultType=core): only the fields the pipeline
// reads. abstractText carries HTML markup; isOpenAccess is "Y" or "N".
type record struct {
	DOI          string `json:"doi"`
	Source       string `json:"source"`
	AbstractText string `json:"abstractText"`
	PMCID        string `json:"pmcid"`
	IsOpenAccess string `json:"isOpenAccess"`
}

// Abstracts returns plain-text abstracts keyed by normalized DOI. DOIs
// Europe PMC does not know, or knows without an abstract, are absent. The
// DOIs go out in batches; when one fails, the abstracts collected before it
// are returned together with the error.
func (c *Client) Abstracts(ctx context.Context, dois []string) (map[string]string, error) {
	out := map[string]string{}

	for start := 0; start < len(dois); start += batchSize {
		batch := dois[start:min(start+batchSize, len(dois))]

		records, err := c.search(ctx, batch)
		if err != nil {
			return out, err
		}

		maps.Copy(out, pick(records))
	}

	return out, nil
}

// search runs one query for a batch of DOIs.
func (c *Client) search(ctx context.Context, dois []string) ([]record, error) {
	query := buildQuery(dois)
	if query == "" {
		return nil, nil
	}

	q := url.Values{
		"query":      {query},
		"resultType": {"core"},
		"format":     {"json"},
		"pageSize":   {strconv.Itoa(pageSize)},
	}

	u, err := httpx.URL(c.base, q, "search")
	if err != nil {
		return nil, err
	}

	var resp struct {
		ResultList struct {
			Result []record `json:"result"`
		} `json:"resultList"`
	}

	if err := c.http.DoJSON(ctx, http.MethodGet, u, nil, &resp, nil); err != nil {
		return nil, err
	}

	return resp.ResultList.Result, nil
}

// buildQuery joins the DOIs into one field query: DOI:"a" OR DOI:"b". A DOI
// with a quote or backslash cannot be put in the phrase safely and is left
// out (none occurs in practice).
func buildQuery(dois []string) string {
	terms := make([]string, 0, len(dois))

	for _, d := range dois {
		d = model.NormalizeDOI(d)
		if d == "" || strings.ContainsAny(d, `"\`) {
			continue
		}

		terms = append(terms, `DOI:"`+d+`"`)
	}

	return strings.Join(terms, " OR ")
}

// pick chooses one abstract per DOI. A DOI can come back as several records
// (the journal article and its preprint); the one with an abstract wins,
// among those the most trusted source, then the first in the response.
func pick(records []record) map[string]string {
	out := map[string]string{}
	rank := map[string]int{}

	for _, r := range records {
		doi := model.NormalizeDOI(r.DOI)
		text := plainText(r.AbstractText)

		if doi == "" || text == "" {
			continue
		}

		if prev, seen := rank[doi]; seen && prev <= sourceRank(r.Source) {
			continue
		}

		out[doi] = text
		rank[doi] = sourceRank(r.Source)
	}

	return out
}

func sourceRank(source string) int {
	switch source {
	case sourceMED:
		return 0
	case sourcePMC:
		return 1
	default:
		return 2
	}
}
