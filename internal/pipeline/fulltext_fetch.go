package pipeline

// The download walk of fetchFullText: one paper through the full-text
// sources, in order, stopping at the first usable text. Each step either
// yields the text, or records why it did not: a skip (nothing to try) or a
// failure (something went wrong). Both go into one compact trace that is
// logged as a single line per study, so that an audit of the run can say why
// a paper ended up on its abstract; the failures also become the paper's
// recorded error when no source delivered.

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	// maxReaderTries caps the pages the reader service is asked about per
	// paper: it is rate limited (20 requests a minute without a key) and
	// slow (a few seconds a page).
	maxReaderTries = 4
	// maxStepErrChars cuts an error in the trace: an HTTP error carries a
	// snippet of the response body, which is a page of HTML for a challenge.
	maxStepErrChars = 110
)

// notPDFReason is what a download that is not a PDF is reported as.
const notPDFReason = "not a PDF (landing page?)"

var errNotPDF = errors.New(notPDFReason)

// logDOI is the log attribute that names the paper.
const logDOI = "doi"

// fullTextRun is the walk of one paper through the sources.
type fullTextRun struct {
	p     *Pipeline
	e     *model.EnrichedPaper
	path  string // where the PDF is kept for the vision stage
	pmcid string // learned along the way: the OA links, Europe PMC, the ID converter
	// steps is the trace: every source's outcome, skips and failures alike;
	// failures is the part of it that went wrong.
	steps    []string
	failures []string
}

// downloadFullText runs the download sources for a paper with no PDF of the
// user's in the run directory, logs the outcome and records the failures.
func (p *Pipeline) downloadFullText(ctx context.Context, path string, e *model.EnrichedPaper) (text, source string) {
	r := &fullTextRun{p: p, e: e, path: path, pmcid: pmcidFromLinks(e)}

	text, source = r.run(ctx)
	r.log(text, source)

	if text == "" && len(r.failures) > 0 {
		p.sourceFailed(e, "fulltext", errors.New(strings.Join(r.failures, "; ")))
	}

	return text, source
}

// run tries the sources in order.
func (r *fullTextRun) run(ctx context.Context) (text, source string) {
	for _, step := range []func(context.Context) (string, string){r.europePMC, r.pmcArchive, r.pdfs, r.reader} {
		if ctx.Err() != nil {
			r.skip("run", "interrupted")
			return "", ""
		}

		if text, source = step(ctx); text != "" {
			return text, source
		}
	}

	return "", ""
}

func (r *fullTextRun) skip(step, why string) { r.steps = append(r.steps, step+": "+why) }

func (r *fullTextRun) fail(step string, err error) {
	msg := step + ": " + compactErr(err)

	r.steps = append(r.steps, msg)
	r.failures = append(r.failures, msg)
}

// log writes the one line per study: what was obtained and from where, or
// that nothing was, and in both cases why the sources that did not deliver
// did not.
func (r *fullTextRun) log(text, source string) {
	args := []any{logDOI, r.e.DOI}

	if text != "" {
		args = append(args, "source", source, "chars", len([]rune(text)))
	}

	if len(r.steps) > 0 {
		args = append(args, "steps", strings.Join(r.steps, "; "))
	}

	if text == "" {
		r.p.log.Info("full text not obtained", args...)
		return
	}

	r.p.log.Info("full text obtained", args...)
}

// europePMC is the first source: the JATS XML of an open-access PMCID.
func (r *fullTextRun) europePMC(ctx context.Context) (text, source string) {
	const step = "europepmc"

	switch {
	case r.p.clients.FullTexts == nil:
		r.skip(step, "not configured")
		return "", ""
	case model.NormalizeDOI(r.e.DOI) == "":
		r.skip(step, "no DOI")
		return "", ""
	}

	res, err := r.p.clients.FullTexts.FullText(ctx, r.e.DOI, fullTextMaxChars)
	r.pmcid = cmp.Or(model.NormalizePMCID(res.PMCID), r.pmcid)

	switch {
	case err != nil:
		r.fail(step, err)
		return "", ""
	case res.Text == "":
		r.skip(step, cmp.Or(res.Reason, "no text"))
		return "", ""
	}

	r.keepOAPDF(ctx)

	return res.Text, fullTextSourceEuropePMC
}

// pmcArchive is the second source: the PubMed Central XML through NCBI, for
// the papers Europe PMC does not serve. The PMCID is the one already learned,
// else the ID converter's answer for the DOI.
func (r *fullTextRun) pmcArchive(ctx context.Context) (text, source string) {
	const step = "pmc"

	pmc := r.p.clients.PMC
	if pmc == nil {
		r.skip(step, "not configured")
		return "", ""
	}

	if r.pmcid == "" && !r.lookUpPMCID(ctx, step) {
		return "", ""
	}

	text, err := pmc.FullText(ctx, r.pmcid, fullTextMaxChars)

	switch {
	case err != nil:
		r.fail(step+" "+r.pmcid, err)
		return "", ""
	case text == "":
		r.skip(step, r.pmcid+" has no body text")
		return "", ""
	}

	r.keepOAPDF(ctx)

	return text, fullTextSourcePMC
}

// lookUpPMCID asks the ID converter for the DOI's PMCID; false when there is
// none (the reason is in the trace).
func (r *fullTextRun) lookUpPMCID(ctx context.Context, step string) bool {
	doi := model.NormalizeDOI(r.e.DOI)
	if doi == "" {
		r.skip(step, "no DOI")
		return false
	}

	id, err := r.p.clients.PMC.PMCID(ctx, doi)

	switch {
	case err != nil:
		r.fail(step+" id converter", err)
		return false
	case id == "":
		r.skip(step, "no PMCID")
		return false
	}

	r.pmcid = model.NormalizePMCID(id)

	return r.pmcid != ""
}

// keepOAPDF downloads the first candidate PDF next to a paper whose text came
// from an XML source, so the chart check still finds it in fulltext/. The
// text is already there, so a failure is only noted in the trace.
func (r *fullTextRun) keepOAPDF(ctx context.Context) {
	urls := pdfCandidates(r.e)
	if len(urls) == 0 || r.p.clients.PDFs == nil {
		return
	}

	body, err := r.p.clients.PDFs.Fetch(ctx, urls[0], pdfMaxBytes)

	switch {
	case err != nil:
		r.skip("pdf copy "+hostOf(urls[0]), "not saved: "+compactErr(err))
	case !bytes.HasPrefix(body, pdfMagic):
		r.skip("pdf copy "+hostOf(urls[0]), "not saved: "+notPDFReason)
	default:
		if err := keepPDF(r.path, body); err != nil {
			r.skip("pdf copy", "not saved: "+compactErr(err))
		}
	}
}

// pdfs is the third source: the PDF from the candidate locations (see
// pdfCandidates), each tried once. A PDF that downloads but yields no text
// ends the step: the file is kept in fulltext/ and would shadow any later one.
func (r *fullTextRun) pdfs(ctx context.Context) (text, source string) {
	const step = "pdf"

	if r.p.clients.PDFs == nil {
		r.skip(step, "not configured")
		return "", ""
	}

	urls := pdfCandidates(r.e)
	if len(urls) == 0 {
		r.skip(step, "no PDF address")
		return "", ""
	}

	for _, u := range urls {
		label := step + " " + hostOf(u)

		body, err := r.p.clients.PDFs.Fetch(ctx, u, pdfMaxBytes)

		switch {
		case err != nil:
			r.fail(label, err)
			continue
		case !bytes.HasPrefix(body, pdfMagic):
			r.fail(label, errNotPDF)
			continue
		}

		text, err := r.p.pdfText(r.path, body)
		if err != nil {
			r.fail(label, err)
			return "", ""
		}

		return text, fullTextSourcePDF
	}

	return "", ""
}

// reader is the last source: the Jina Reader on the PMC page, then the
// landing pages, then the PDF addresses. Its answer has already passed the
// content check (a challenge page, an abstract page and an error page are
// rejected by the reader client).
func (r *fullTextRun) reader(ctx context.Context) (text, source string) {
	const step = "jina"

	if r.p.clients.Reader == nil {
		r.skip(step, "not configured")
		return "", ""
	}

	candidates := readerCandidates(r.e, r.pmcid)
	if len(candidates) == 0 {
		r.skip(step, "no page address")
		return "", ""
	}

	tried := map[string]bool{}

	for _, u := range candidates {
		if len(tried) >= maxReaderTries {
			r.skip(step, "try limit reached")
			break
		}

		page, ok := r.pageAddress(ctx, u)
		if !ok || tried[page] {
			continue
		}

		// a DOI may resolve to an abstract page: nothing to read there
		if isAbstractPage(page) {
			r.skip(step+" "+hostOf(page), "an abstract page, no article text")
			continue
		}

		tried[page] = true

		text, err := r.p.clients.Reader.PageText(ctx, page)
		if err != nil {
			r.fail(step+" "+hostOf(page), err)
			continue
		}

		return clipRunes(text, fullTextMaxChars), fullTextSourceJinaPrefix + hostOf(page)
	}

	return "", ""
}

// pageAddress turns a candidate into the address the reader is asked about: a
// doi.org address is resolved to the page the publisher registered (the reader
// service refuses doi.org), any other is used as it is.
func (r *fullTextRun) pageAddress(ctx context.Context, u string) (string, bool) {
	doi, isDOI := doiOfURL(u)
	if !isDOI {
		return u, true
	}

	label := "doi " + doi

	if r.p.clients.Landings == nil {
		r.skip(label, "no resolver configured")
		return "", false
	}

	landing, err := r.p.clients.Landings.LandingURL(ctx, doi)

	switch {
	case err != nil:
		r.fail(label, err)
		return "", false
	case landing == "":
		r.skip(label, "not registered at doi.org")
		return "", false
	}

	return upgradeHTTP(landing), true
}

// hostOf is the host of an address, for the trace and the source name; the
// address itself when it does not parse.
func hostOf(u string) string {
	if parsed, err := url.Parse(u); err == nil && parsed.Host != "" {
		return parsed.Host
	}

	return u
}

// compactErr renders an error on one short line for the trace.
func compactErr(err error) string {
	s := strings.Join(strings.Fields(persistedErr(err)), " ")
	if r := []rune(s); len(r) > maxStepErrChars {
		return string(r[:maxStepErrChars]) + "…"
	}

	return s
}

// clipRunes cuts s at maxChars characters, never splitting one.
func clipRunes(s string, maxChars int) string {
	if r := []rune(s); len(r) > maxChars {
		return string(r[:maxChars])
	}

	return s
}
