package pipeline

import (
	"bytes"
	"context"
	"fmt"
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
		return "", err
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

// fetchFullText supplies the paper's full text. A PDF already sitting in
// the run directory (runs/<run>/fulltext/<doi>.pdf — the user can drop one
// for a paywalled paper or two) ALWAYS wins, regardless of flags; otherwise
// the OA link is downloaded, and only with --fulltext.
func (p *Pipeline) fetchFullText(ctx context.Context, e *model.EnrichedPaper, download bool) string {
	if p.dir == "" {
		return ""
	}
	name := strings.ReplaceAll(model.NormalizeDOI(e.DOI), "/", "_") + ".pdf"
	path := filepath.Join(p.dir, "fulltext", name)

	var body []byte
	if b, err := os.ReadFile(path); err == nil {
		body = b // user-supplied: the paywall workaround for a specific paper
	} else if !download || e.OALink == "" {
		return ""
	} else {
		hc := httpx.New("pdf", 2)
		b, err := hc.Get(ctx, e.OALink, pdfMaxBytes)
		if err != nil {
			e.Errors = append(e.Errors, "fulltext: "+err.Error())
			return ""
		}
		body = b
	}
	if !bytes.HasPrefix(body, []byte("%PDF")) {
		src := path
		if e.OALink != "" {
			src = e.OALink
		}
		e.Errors = append(e.Errors, "fulltext: "+src+" is not a PDF (landing page?)")
		return ""
	}
	if !fileExists(path) { // keep downloaded PDFs on disk for the vision stage
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			e.Errors = append(e.Errors, "fulltext: "+err.Error())
			return ""
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			e.Errors = append(e.Errors, "fulltext: "+err.Error())
			return ""
		}
	}
	text, err := pdfTextExtractor(body, fullTextMaxChars)
	if err != nil || text == "" {
		e.Errors = append(e.Errors, fmt.Sprintf("fulltext: PDF saved but text extraction failed (%v)", err))
		return ""
	}
	return text
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
// weighs the notes as data. A broken or missing file is simply "no notes".
func loadVisionNotes(dir string, st *State) {
	p := filepath.Join(dir, "3b-vision.json")
	if !fileExists(p) {
		return
	}
	var v struct {
		VisionNotes []model.VisionNote `json:"vision_notes"`
	}
	if err := loadJSON(p, &v); err != nil {
		return
	}
	st.VisionNotes = v.VisionNotes
}
