// Package pdf is the adapter for paper PDFs: an open-access downloader on the
// shared HTTP layer and a pure-Go text extractor. It implements
// pipeline.PDFSource and pipeline.PDFText.
package pdf

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"

	lpdf "github.com/ledongthuc/pdf"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var (
	_ pipeline.PDFSource = (*Downloader)(nil)
	_ pipeline.PDFText   = (*Extractor)(nil)
)

// requestsPerSecond keeps the load on publishers' servers low.
const requestsPerSecond = 2

// Downloader fetches open-access PDFs with the shared retry/rate-limit
// machinery.
type Downloader struct {
	hc *httpx.Client
}

func NewDownloader() *Downloader {
	return &Downloader{hc: httpx.New("pdf", requestsPerSecond)}
}

// Fetch returns the document at url; a response above maxBytes is an error.
func (d *Downloader) Fetch(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	return d.hc.Get(ctx, url, maxBytes)
}

// Extractor pulls plain text out of a PDF.
type Extractor struct {
	log *slog.Logger
}

func NewExtractor(log *slog.Logger) *Extractor { return &Extractor{log: log} }

// Text returns the text of the first pages, cut at maxChars. One unreadable
// page does not sink the document.
func (x *Extractor) Text(b []byte, maxChars int) (string, error) {
	r, err := lpdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return "", fmt.Errorf("opening the PDF: %w", err)
	}

	var sb strings.Builder
	for i := 1; i <= r.NumPage() && sb.Len() < maxChars; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}

		text, err := page.GetPlainText(nil)
		if err != nil {
			x.log.Warn("PDF page skipped: unreadable", "page", i, "err", err)
			continue
		}

		sb.WriteString(text)
	}

	out := sb.String()
	if len(out) > maxChars {
		out = out[:maxChars]
	}

	return strings.TrimSpace(out), nil
}
