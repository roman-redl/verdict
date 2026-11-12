package pubmed

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.PubMedRecords = (*Client)(nil)

const (
	// doiBatch is how many DOIs one esearch query carries: the query is a GET
	// URL, and a quoted DOI with its field tag is ~60 characters.
	doiBatch = 20
	// pmidsPerDOI leaves room for the several records a DOI can match; the
	// results are matched back by the DOI of each fetched record anyway.
	pmidsPerDOI = 5
	// maxFetchBytes caps one efetch answer: 200 records with their reference
	// lists run to tens of megabytes of XML.
	maxFetchBytes = 64 << 20
)

// Records returns what the full PubMed records say beyond the summary: the
// accession numbers of the data banks (ClinicalTrials.gov NCT numbers,
// ISRCTN, ...) and the publication types, keyed by normalized DOI. It takes
// one esearch per 20 DOIs to find the records and one efetch per 200 records
// to read them. A DOI without a record, or whose record lists neither a data
// bank nor a publication type, is absent; when a request fails, the details
// collected before it come back together with the error.
func (c *Client) Records(ctx context.Context, dois []string) (map[string]model.PubMedDetail, error) {
	out := map[string]model.PubMedDetail{}
	wanted := map[string]bool{}

	for _, d := range dois {
		if d = model.NormalizeDOI(d); d != "" && !strings.ContainsAny(d, `"\`) {
			wanted[d] = true
		}
	}

	pmids, err := c.findRecords(ctx, slices.Sorted(maps.Keys(wanted)))
	if err != nil {
		return out, err
	}

	for start := 0; start < len(pmids); start += summaryBatch {
		records, err := c.fetchDataBanks(ctx, pmids[start:min(start+summaryBatch, len(pmids))])
		if err != nil {
			return out, err
		}

		for _, r := range records {
			if !wanted[r.doi] || (len(r.numbers) == 0 && len(r.pubTypes) == 0) {
				continue
			}

			d := out[r.doi]
			d.Registrations = model.NormalizeRegistrations(append(d.Registrations, r.numbers...))
			d.PubTypes = model.NormalizePublicationTypes(append(d.PubTypes, r.pubTypes...))
			out[r.doi] = d
		}
	}

	return out, nil
}

// findRecords looks the DOIs up by the article identifier field and returns
// the PMIDs of the records found, each once.
func (c *Client) findRecords(ctx context.Context, dois []string) ([]string, error) {
	var pmids []string

	for start := 0; start < len(dois); start += doiBatch {
		batch := dois[start:min(start+doiBatch, len(dois))]

		terms := make([]string, len(batch))
		for i, d := range batch {
			terms[i] = `"` + d + `"[AID]`
		}

		hits, err := c.Search(ctx, strings.Join(terms, " OR "), len(batch)*pmidsPerDOI)
		if err != nil {
			return pmids, err
		}

		pmids = append(pmids, hits.PMIDs...)
	}

	slices.Sort(pmids)

	return slices.Compact(pmids), nil
}

// fetched is one record read from efetch: its DOI, the accession numbers of
// its data banks and its publication types.
type fetched struct {
	doi      string
	numbers  []string
	pubTypes []string
}

// article is the part of a PubmedArticle the details come from.
type article struct {
	DataBanks []struct {
		Numbers []string `xml:"AccessionNumberList>AccessionNumber"`
	} `xml:"MedlineCitation>Article>DataBankList>DataBank"`
	PubTypes []string `xml:"MedlineCitation>Article>PublicationTypeList>PublicationType"`
	IDs      []struct {
		Type  string `xml:"IdType,attr"`
		Value string `xml:",chardata"`
	} `xml:"PubmedData>ArticleIdList>ArticleId"`
}

// fetchDataBanks reads the records of the PMIDs (an XML answer: the JSON
// summaries carry no data banks).
func (c *Client) fetchDataBanks(ctx context.Context, pmids []string) ([]fetched, error) {
	q := c.params()
	q.Set("retmode", "xml")
	q.Set("id", strings.Join(pmids, ","))

	u, err := httpx.URL(c.base, q, "efetch.fcgi")
	if err != nil {
		return nil, err
	}

	body, err := c.http.Get(ctx, u, maxFetchBytes)
	if err != nil {
		return nil, fmt.Errorf("efetch: %w", c.redact(err))
	}

	var set struct {
		Articles []article `xml:"PubmedArticle"`
	}

	if err := xml.NewDecoder(bytes.NewReader(body)).Decode(&set); err != nil {
		return nil, fmt.Errorf("efetch: decoding the records: %w", err)
	}

	out := make([]fetched, 0, len(set.Articles))

	for i := range set.Articles {
		a := &set.Articles[i]
		f := fetched{pubTypes: a.PubTypes}

		for _, id := range a.IDs {
			if id.Type == "doi" {
				f.doi = model.NormalizeDOI(id.Value)
			}
		}

		for _, db := range a.DataBanks {
			f.numbers = append(f.numbers, db.Numbers...)
		}

		if f.doi != "" {
			out = append(out, f)
		}
	}

	return out, nil
}
