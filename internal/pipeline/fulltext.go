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

	"github.com/roman-redl/verdict/internal/model"
)

// fullText caps: the download is bounded, the stored excerpt tighter (stage
// artifacts stay readable), the critic window tighter still: a critique batch
// carries several studies, so the window sets the cost of a call. The
// outcomes a review needs sit in tables and results, which a window too small
// cuts off (the Europe PMC text puts the tables first for the same reason).
const (
	pdfMaxBytes = 20 << 20 // 20 MB
	// fullTextMaxChars is how much of a full text is stored (the artifacts
	// and the PDF text extractor use it).
	fullTextMaxChars = 60000
	// critiqueFullTextChars is how much of the stored text the critic reads.
	critiqueFullTextChars = 30000
	// pdfDOIChars is how much of a PDF is scanned for its own DOI.
	pdfDOIChars = 20000
)

// The values of EnrichedPaper.FullTextSource.
const (
	fullTextSourceEuropePMC = "europepmc"
	fullTextSourcePDF       = "pdf"
)

// pdfMagic starts every PDF file; an open-access link that answers with a
// landing page does not.
var pdfMagic = []byte("%PDF")

// fullTextPath is where a paper's PDF lives inside the run directory:
// <dir>/fulltext/<doi with '/' as '_'>.pdf. The user can drop a file there
// for a paywalled paper; downloaded PDFs are kept there for the vision stage.
func fullTextPath(dir, doi string) string {
	name := strings.ReplaceAll(model.NormalizeDOI(doi), "/", "_") + ".pdf"
	return filepath.Join(dir, "fulltext", name)
}

// fetchFullText supplies the paper's full text and names its source
// ("europepmc" or "pdf"; both empty when there is none). A PDF already
// sitting in the run directory ALWAYS wins, regardless of flags. Otherwise,
// only where the caller allows downloads (full text mode at enrichment, the
// hybrid deepening pass for pivotal studies): the Europe PMC XML first — no
// landing pages, tables kept as text — then the OA link as a PDF. When the XML
// text is used, the OA PDF is still saved (best effort, for the chart check)
// but its text is not extracted. It does not touch e.FullText: the caller
// assigns the field.
func (p *Pipeline) fetchFullText(
	ctx context.Context, dir string, e *model.EnrichedPaper, download bool,
) (text, source string) {
	if dir == "" {
		return "", ""
	}

	path := fullTextPath(dir, e.DOI)

	//nolint:gosec // the path is built from the run directory and a normalized DOI (a file the user placed)
	body, err := os.ReadFile(path)

	switch {
	case err == nil:
		// user-supplied: the paywall workaround for a specific paper
		return p.textOfPDF(path, body, e)
	case !errors.Is(err, fs.ErrNotExist):
		p.sourceFailed(e, "fulltext", err)
		return "", ""
	case !download:
		return "", ""
	}

	if txt := p.europePMCText(ctx, e); txt != "" {
		// the XML is the text the critic reads; the PDF is only kept for the
		// chart check, so its text is never extracted and its failure is not
		// a paper error
		p.keepOAPDF(ctx, path, e)

		return txt, fullTextSourceEuropePMC
	}

	if e.OALink == "" || p.clients.PDFs == nil {
		return "", ""
	}

	body, err = p.clients.PDFs.Fetch(ctx, e.OALink, pdfMaxBytes)
	if err != nil {
		p.sourceFailed(e, "fulltext", err)
		return "", ""
	}

	return p.textOfPDF(path, body, e)
}

// europePMCText asks Europe PMC for the open-access full text; "" when there
// is none or the request failed — the failure is not fatal, the caller falls
// back to the PDF.
func (p *Pipeline) europePMCText(ctx context.Context, e *model.EnrichedPaper) string {
	if p.clients.FullTexts == nil || model.NormalizeDOI(e.DOI) == "" {
		return ""
	}

	txt, err := p.clients.FullTexts.FullText(ctx, e.DOI, fullTextMaxChars)
	if err != nil {
		p.log.Warn("europe pmc full text unavailable: falling back to the PDF", "doi", e.DOI, "err", err)
		return ""
	}

	return txt
}

// keepOAPDF downloads the open-access PDF next to a paper whose text came from
// Europe PMC, so the chart check still finds it in fulltext/. The text is
// already there, so every failure is only logged.
func (p *Pipeline) keepOAPDF(ctx context.Context, path string, e *model.EnrichedPaper) {
	if e.OALink == "" || p.clients.PDFs == nil {
		return
	}

	body, err := p.clients.PDFs.Fetch(ctx, e.OALink, pdfMaxBytes)
	if err != nil {
		p.log.Warn("PDF not saved: download failed", "doi", e.DOI, "url", e.OALink, "err", err)
		return
	}

	if !bytes.HasPrefix(body, pdfMagic) {
		p.log.Warn("PDF not saved: the link is not a PDF (landing page?)", "doi", e.DOI, "url", e.OALink)
		return
	}

	if err := keepPDF(path, body); err != nil {
		p.log.Warn("PDF not saved", "doi", e.DOI, "err", err)
	}
}

// textOfPDF checks the document is a PDF, keeps it for the vision stage and
// extracts its text. Every failure is recorded on the paper and yields no
// text.
func (p *Pipeline) textOfPDF(path string, body []byte, e *model.EnrichedPaper) (text, source string) {
	if !bytes.HasPrefix(body, pdfMagic) {
		src := path
		if e.OALink != "" {
			src = e.OALink
		}

		p.sourceFailed(e, "fulltext", fmt.Errorf("%s is not a PDF (landing page?)", src))

		return "", ""
	}

	if err := keepPDF(path, body); err != nil {
		p.sourceFailed(e, "fulltext", err)
		return "", ""
	}

	if p.clients.PDFText == nil {
		p.sourceFailed(e, "fulltext", errors.New("no PDF text extractor is configured"))
		return "", ""
	}

	text, err := p.clients.PDFText.Text(body, fullTextMaxChars)

	switch {
	case err != nil:
		p.sourceFailed(e, "fulltext", fmt.Errorf("PDF saved but text extraction failed: %w", err))
		return "", ""
	case text == "":
		p.sourceFailed(e, "fulltext", errors.New("PDF saved but holds no extractable text"))
		return "", ""
	}

	return text, fullTextSourcePDF
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

	//nolint:gosec // the path is built from the run directory and a normalized DOI ('/' replaced by '_')
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

		if txt, src := p.fetchFullText(ctx, dir, e, false); txt != "" {
			e.FullText, e.FullTextSource = txt, src
		}
	}
}

// AddedPDF is a user-supplied PDF placed into a run.
type AddedPDF struct {
	DOI  string // the first DOI found inside the document
	Path string // where the document landed
}

// AddPDF drops a paper PDF into the run's fulltext/ slot: the DOI is read
// from the PDF itself, so no manual renaming. The next run over that
// directory (with --resume) reads the full text instead of the abstract.
func AddPDF(x PDFText, runDir string, data []byte) (AddedPDF, error) {
	if !bytes.HasPrefix(data, pdfMagic) {
		return AddedPDF{}, &UserError{Msg: "not a PDF"}
	}

	dois, err := PDFDOIs(x, data)
	if err != nil {
		return AddedPDF{}, err
	}

	if len(dois) == 0 {
		return AddedPDF{}, &UserError{Msg: fmt.Sprintf(
			"no DOI found inside the text — place it manually as %s", fullTextPath(runDir, "<doi>"))}
	}

	path := fullTextPath(runDir, dois[0])
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return AddedPDF{}, fmt.Errorf("creating the fulltext directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return AddedPDF{}, fmt.Errorf("saving the PDF: %w", err)
	}

	return AddedPDF{DOI: dois[0], Path: path}, nil
}

// PDFDOIs extracts the DOIs mentioned in a PDF (the leading pages carry
// the paper's own DOI) — the identity of a user-supplied file.
func PDFDOIs(x PDFText, b []byte) ([]string, error) {
	text, err := x.Text(b, pdfDOIChars)
	if err != nil {
		return nil, fmt.Errorf("reading the PDF text: %w", err)
	}

	return ExtractDOIs(text), nil
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
