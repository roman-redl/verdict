package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	pdf "github.com/ledongthuc/pdf"

	"github.com/roman-redl/verdict/internal/httpx"
	"github.com/roman-redl/verdict/internal/model"
)

// fullText caps: the download is bounded, the stored excerpt tighter (stage
// artifacts stay readable), the critic sees the most it can usefully read.
const (
	pdfMaxBytes      = 20 << 20 // 20 MB
	fullTextMaxChars = 20000
)

// pdfTextExtractor is the PDF → text hook; tests stub it.
var pdfTextExtractor = pdfText

// pdfText pulls plain text out of a PDF via the pure-Go parser.
func pdfText(b []byte, maxChars int) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(b), int64(len(b)))
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
			continue // one unreadable page does not sink the document
		}

		sb.WriteString(text)
	}

	out := sb.String()
	if len(out) > maxChars {
		out = out[:maxChars]
	}

	return strings.TrimSpace(out), nil
}

// fullTextPath is where a paper's PDF lives inside the run directory:
// <dir>/fulltext/<doi with '/' as '_'>.pdf. The user can drop a file there
// for a paywalled paper; downloaded PDFs are kept there for the vision stage.
func fullTextPath(dir, doi string) string {
	name := strings.ReplaceAll(model.NormalizeDOI(doi), "/", "_") + ".pdf"
	return filepath.Join(dir, "fulltext", name)
}

// fetchFullText supplies the paper's full text. A PDF already sitting in
// the run directory ALWAYS wins, regardless of flags; otherwise the OA link
// is downloaded, and only where the caller allows it (full text mode at
// enrichment, the hybrid deepening pass for pivotal studies).
func (p *Pipeline) fetchFullText(ctx context.Context, dir string, e *model.EnrichedPaper, download bool) string {
	if dir == "" {
		return ""
	}

	path := fullTextPath(dir, e.DOI)

	body, ok := p.loadPDF(ctx, path, e, download)
	if !ok {
		return ""
	}

	if !bytes.HasPrefix(body, []byte("%PDF")) {
		src := path
		if e.OALink != "" {
			src = e.OALink
		}

		p.sourceFailed(e, "fulltext", fmt.Errorf("%s is not a PDF (landing page?)", src))

		return ""
	}

	if err := keepPDF(path, body); err != nil {
		p.sourceFailed(e, "fulltext", err)
		return ""
	}

	text, err := pdfTextExtractor(body, fullTextMaxChars)

	switch {
	case err != nil:
		p.sourceFailed(e, "fulltext", fmt.Errorf("PDF saved but text extraction failed: %w", err))
		return ""
	case text == "":
		p.sourceFailed(e, "fulltext", errors.New("PDF saved but holds no extractable text"))
		return ""
	}

	return text
}

// loadPDF returns the PDF bytes: the user's file when one is in place, else
// the open-access download when the caller allows it. ok is false when there
// is nothing to read (or the download failed, which is recorded).
func (p *Pipeline) loadPDF(ctx context.Context, path string, e *model.EnrichedPaper, download bool) ([]byte, bool) {
	//nolint:gosec // the path is built from the run directory and a normalized DOI (a file the user placed)
	b, err := os.ReadFile(path)
	if err == nil {
		return b, true // user-supplied: the paywall workaround for a specific paper
	}

	if !errors.Is(err, fs.ErrNotExist) {
		p.sourceFailed(e, "fulltext", err)
		return nil, false
	}

	if !download || e.OALink == "" {
		return nil, false
	}

	b, err = httpx.New("pdf", 2).Get(ctx, e.OALink, pdfMaxBytes)
	if err != nil {
		p.sourceFailed(e, "fulltext", err)
		return nil, false
	}

	return b, true
}

// keepPDF stores a downloaded PDF for the vision stage; a file already in
// place (the user's) is left alone.
func keepPDF(path string, body []byte) error {
	if fileExists(path) {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating the fulltext directory: %w", err)
	}

	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("saving the PDF: %w", err)
	}

	return nil
}

// pickupFullTexts loads PDFs sitting in the run's fulltext/ directory into
// papers that have no full text yet. This is the entry point the resume path
// needs: stage 2 (where the pickup used to live exclusively) is skipped when
// its artifact is on disk, so a PDF dropped afterwards (the addpdf recipe)
// enters the pipeline here, at critique time — the critique cache key
// includes the full text, so exactly those studies re-critique. Only ever
// fills empty slots; no network.
func (p *Pipeline) pickupFullTexts(ctx context.Context, dir string, st *State) {
	if dir == "" {
		return
	}

	for i := range st.Enriched {
		e := &st.Enriched[i]
		if e.FullText != "" || model.NormalizeDOI(e.DOI) == "" {
			continue
		}

		if txt := p.fetchFullText(ctx, dir, e, false); txt != "" {
			e.FullText = txt
		}
	}
}

// PDFDOIs extracts the DOIs mentioned in a PDF (the leading pages carry
// the paper's own DOI) — the identity of a user-supplied file.
func PDFDOIs(b []byte) []string {
	text, err := pdfTextExtractor(b, 20000)
	if err != nil {
		return nil
	}

	return ExtractDOIs(text)
}

// loadVisionNotes picks up the optional agent-written chart critique
// (3b-vision.json): the agent renders the run's PDF pages and inspects them
// via the vision MCP (GLM text models must not read images), then synthesis
// weighs the notes as data. A missing file is simply "no notes"; a broken
// one is logged and also yields no notes — it must not kill the run.
func (p *Pipeline) loadVisionNotes(dir string, st *State) {
	path := filepath.Join(dir, "3b-vision.json")
	if !fileExists(path) {
		return
	}

	var v struct {
		VisionNotes []model.VisionNote `json:"vision_notes"`
	}

	if err := loadJSON(path, &v); err != nil {
		p.log.Warn("vision notes ignored: unreadable artifact", "path", path, "err", err)
		return
	}

	st.VisionNotes = v.VisionNotes
}
