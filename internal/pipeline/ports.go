package pipeline

import (
	"context"

	"github.com/roman-redl/verdict/internal/model"
)

// The ports. Everything with a side effect that the pipeline needs from
// outside — HTTP APIs, PDF download and parsing, the LLM — is an interface
// declared here, on the consumer's side; the adapters (internal/scite,
// openalex, crossref, s2, pubpeer, consensus, pdf, llm) implement it and are
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

// Notices is the update-notice registry (Crossref): retractions, corrections.
type Notices interface {
	Updates(ctx context.Context, doi string) ([]model.Notice, error)
}

// Scholar is the second metadata source (Semantic Scholar).
type Scholar interface {
	// Lookup returns records keyed by normalized DOI; unknown DOIs are absent.
	Lookup(ctx context.Context, dois []string) (map[string]model.ScholarRecord, error)
}

// Commentary is the post-publication commentary source (PubPeer).
type Commentary interface {
	// Note returns a one-line summary of the commentary on a paper, or ""
	// when there is none.
	Note(ctx context.Context, doi string) (string, error)
	// HasKey reports whether the developer key is configured.
	HasKey() bool
}

// SemanticSearch is the semantic discovery engine (Consensus).
type SemanticSearch interface {
	// Page returns one 0-indexed page of results for the query.
	Page(ctx context.Context, query string, page int) (model.SearchPage, error)
	// HasKey reports whether the API key is configured.
	HasKey() bool
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
