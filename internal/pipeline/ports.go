package pipeline

import (
	"context"
	"errors"

	"github.com/roman-redl/verdict/internal/model"
)

// The ports. Everything with a side effect that the pipeline needs from
// outside — HTTP APIs, PDF download and parsing, the LLM — is an interface
// declared here, on the consumer's side; the adapters (internal/scite,
// openalex, crossref, s2, europepmc, pubmed, pubpeer, consensus, pdf, llm) implement it and are
// wired in cmd/internal/app. The pipeline tests run on handwritten fakes.
//
// Where a method returns (nil, nil), it means "the source knows nothing about
// this" — data, not an error.

// Citations is the smart-citation source (Scite).
type Citations interface {
	// Tallies returns citation counts keyed by normalized DOI; DOIs the
	// source does not know are absent.
	Tallies(ctx context.Context, dois []string) (map[string]model.CitationTally, error)
	// ContradictingSources lists the DOIs of papers that cite the given one
	// as refuted (a paid scope).
	ContradictingSources(ctx context.Context, doi string) ([]string, error)
	// HasKey reports whether the paid scope is configured.
	HasKey() bool
}

// Works is the bibliographic metadata source (OpenAlex).
type Works interface {
	ByDOI(ctx context.Context, doi string) (*model.Work, error)
	// Search is a keyword search; retracted works are left out.
	Search(ctx context.Context, query string, limit int) ([]model.Paper, error)
	// CitesWork returns works citing the given work ID, most cited first.
	CitesWork(ctx context.Context, workID string, limit int) ([]model.Work, error)
	ByIDs(ctx context.Context, ids []string) ([]model.Work, error)
}

// TitleFinder resolves a paper title to a work (the DOI-resolution helper of
// the CLI); (nil, nil) means nothing matched.
type TitleFinder interface {
	SearchTitle(ctx context.Context, title string) (*model.Work, error)
}

// Notices is the update-notice registry (Crossref): retractions, corrections.
type Notices interface {
	Updates(ctx context.Context, doi string) ([]model.Notice, error)
}

// Scholar is the second metadata source (Semantic Scholar).
type Scholar interface {
	// Lookup returns records keyed by normalized DOI; unknown DOIs are absent.
	Lookup(ctx context.Context, dois []string) (map[string]model.ScholarRecord, error)
}

// Abstracts is the fallback abstract source (Europe PMC): OpenAlex withholds
// the abstracts of publisher-restricted works.
type Abstracts interface {
	// Abstracts returns plain-text abstracts keyed by normalized DOI; DOIs
	// without one are absent. When a request fails, the map holds what was
	// collected before the failure, together with the error.
	Abstracts(ctx context.Context, dois []string) (map[string]string, error)
}

// FullTexts is the open-access full-text source (Europe PMC JATS XML). It is
// tried before the PDF download: the XML needs no landing-page guessing and
// keeps the tables as text.
type FullTexts interface {
	// FullText returns the plain text of the paper's open-access full text,
	// at most maxChars characters (results and tables come first, so the cut
	// falls on the least informative part). ("", nil) means the source has no
	// open-access full text for the DOI.
	FullText(ctx context.Context, doi string, maxChars int) (string, error)
}

// Commentary is the post-publication commentary source (PubPeer).
type Commentary interface {
	// Note returns a one-line summary of the commentary on a paper, or ""
	// when there is none.
	Note(ctx context.Context, doi string) (string, error)
	// HasKey reports whether the developer key is configured.
	HasKey() bool
}

// ErrPaginationDenied is what SemanticSearch.Page wraps when the plan does not
// allow pages after the first (Consensus answers 403 to them on the free
// plan). It is a limit of the plan, not a failure of the run: the walk keeps
// what the first pages returned.
var ErrPaginationDenied = errors.New("the plan does not allow pages after the first")

// SemanticSearch is the semantic discovery engine (Consensus).
type SemanticSearch interface {
	// Page returns one 0-indexed page of results for the query. The page size
	// is the adapter's own setting; the size actually applied comes back in
	// the page. A page after the first that the plan refuses fails with
	// ErrPaginationDenied.
	Page(ctx context.Context, query string, page int) (model.SearchPage, error)
	// HasKey reports whether the API key is configured.
	HasKey() bool
}

// PubMed is the PubMed literature index (NCBI E-utilities): a Boolean search
// over MEDLINE and the records behind its hits.
type PubMed interface {
	// Search runs a PubMed query and returns at most limit PMIDs in relevance
	// order, with the total number of matching records.
	Search(ctx context.Context, query string, limit int) (model.PubMedHits, error)
	// Summaries returns the records of the PMIDs, in the order of the PMIDs;
	// a PMID PubMed does not know is absent.
	Summaries(ctx context.Context, pmids []string) ([]model.PubMedRecord, error)
}

// PDFSource downloads open-access PDFs.
type PDFSource interface {
	// Fetch returns the document at url; a response above maxBytes is an error.
	Fetch(ctx context.Context, url string, maxBytes int64) ([]byte, error)
}

// PDFText extracts the plain text of a PDF.
type PDFText interface {
	Text(pdf []byte, maxChars int) (string, error)
}
