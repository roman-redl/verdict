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

// The values of EnrichedPaper.FullTextSource; a page read by the Jina Reader
// is "jina:<host of the page>".
const (
	fullTextSourceEuropePMC  = "europepmc"
	fullTextSourcePMC        = "pmc"
	fullTextSourcePDF        = "pdf"
	fullTextSourceJinaPrefix = "jina:"
)

// pdfMagic starts every PDF file; an open-access link that answers with a
// landing page does not.
var pdfMagic = []byte("%PDF")

// fullTextPath is where a paper's PDF lives inside the run directory:
// <dir>/fulltext/<doi with '/' as '_'>.pdf. The user can drop a file there
// for a paywalled paper; downloaded PDFs are kept there for the vision stage.
func fullTextPath(dir, doi string) string {
	return filepath.Join(dir, "fulltext", fullTextFileName(doi))
}

// fullTextFileName is the name a paper's PDF must have inside fulltext/:
// the normalized DOI with '/' as '_'.
func fullTextFileName(doi string) string {
	return strings.ReplaceAll(model.NormalizeDOI(doi), "/", "_") + ".pdf"
}

// fetchFullText supplies the paper's full text and names its source
// ("europepmc", "pmc", "pdf" or "jina:<host>"; both empty when there is
// none). A PDF already sitting in the run directory ALWAYS wins, regardless
// of flags. Otherwise, only where the caller allows downloads (full text mode
// at enrichment, the hybrid deepening pass for pivotal studies), the sources
// are tried in this order, stopping at the first usable text:
//
//  1. the Europe PMC JATS XML (open-access PMCID needed; tables kept as text);
//  2. the PubMed Central XML through NCBI (some author manuscripts that
//     Europe PMC does not serve);
//  3. the PDF, from the candidate locations: repository copies, then the
//     publisher's PDF, then the open-access link, each tried once;
//  4. the Jina Reader on the PMC page, then the landing pages, then the PDF
//     addresses (the text must pass its content check).
//
// When the XML text is used, the OA PDF is still saved (best effort, for the
// chart check) but its text is not extracted. One Info line records the
// outcome and why each skipped source was skipped. It does not touch
// e.FullText: the caller assigns the field.
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

	return p.downloadFullText(ctx, path, e)
}

// pdfText extracts the text of a downloaded or placed PDF, after keeping it
// for the vision stage. An error says what went wrong; no text and no error
// is not possible.
func (p *Pipeline) pdfText(path string, body []byte) (string, error) {
	if err := keepPDF(path, body); err != nil {
		return "", err
	}

	if p.clients.PDFText == nil {
		return "", errors.New("no PDF text extractor is configured")
	}

	text, err := p.clients.PDFText.Text(body, fullTextMaxChars)

	switch {
	case err != nil:
		return "", fmt.Errorf("PDF saved but text extraction failed: %w", err)
	case text == "":
		return "", errors.New("PDF saved but holds no extractable text")
	}

	return text, nil
}

// textOfPDF reads a PDF the user placed in fulltext/ (or an earlier run
// saved there): it must be a PDF, and its text is extracted. Every failure is
// recorded on the paper and yields no text.
func (p *Pipeline) textOfPDF(path string, body []byte, e *model.EnrichedPaper) (text, source string) {
	if !bytes.HasPrefix(body, pdfMagic) {
		p.sourceFailed(e, "fulltext", fmt.Errorf("%s is not a PDF (landing page?)", path))
		return "", ""
	}

	text, err := p.pdfText(path, body)
	if err != nil {
		p.sourceFailed(e, "fulltext", err)
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
