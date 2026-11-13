package pipeline

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// RenderMarkdown renders the human-readable report.md from the final state,
// in the language of the report (English for one written before the field
// existed).
func RenderMarkdown(st *State) string {
	r := st.Report
	lx := lexiconFor(r.Language)

	var b strings.Builder

	b.WriteString("# " + lx.reportTitle + "\n\n")
	fmt.Fprintf(&b, "**%s:** %s\n\n", lx.question, r.Question)
	fmt.Fprintf(&b, "## %s: %s\n\n", lx.verdict, lx.verdictLabel(r.Verdict))
	writeComputedLines(&b, lx, r)

	if r.Consensus != "" || r.ConsensusStatus != "" {
		fmt.Fprintf(&b, "### %s: %s\n\n", lx.consensus, lx.consensusLabel(r.ConsensusStatus))

		if r.Consensus != "" {
			b.WriteString(r.Consensus + "\n\n")
		}
	}

	if t := lx.directionTally(st); t != "" {
		fmt.Fprintf(&b, "**%s:** %s\n\n", lx.directTally, t)
	}

	fmt.Fprintf(&b, "_%s: %s_\n\n", lx.generated, r.GeneratedAt.Format("2006-01-02 15:04 UTC"))
	b.WriteString("### " + lx.rationale + "\n\n" + r.VerdictRationale + "\n\n")
	writeMatrix(&b, lx, r.Matrix)

	sectionList(&b, lx.contradictions, r.Contradictions)
	sectionList(&b, lx.gaps, r.Gaps)
	sectionList(&b, lx.warnings, r.Warnings)
	writeFullTextGaps(&b, lx, st)

	if line := st.Cost.Markdown(); line != "" {
		b.WriteString("\n_" + line + "_\n")
	}

	return b.String()
}

// writeComputedLines writes the lines the pipeline computes under the verdict:
// the effect direction, the pooled estimate, the two ratings, the study counts
// and the evidence base with its list of studies.
func writeComputedLines(b *strings.Builder, lx lexicon, r *model.Report) {
	if r.EffectDirection != "" {
		fmt.Fprintf(b, "**%s:** %s\n\n", lx.effectDirection, lx.effectDirectionLabel(r.EffectDirection))
	}

	if pe := r.PooledEstimate; pe != nil {
		fmt.Fprintf(b, "**%s:** %s\n\n", lx.pooledLabel(pe), lx.pooledText(pe))
	}

	if imp := r.Imprecision; imp != nil {
		fmt.Fprintf(b, "**%s:** %s\n\n", lx.imprecision, lx.imprecisionText(imp))
	}

	if bb := r.BlindingBias; bb != nil {
		fmt.Fprintf(b, "**%s:** %s\n\n", lx.blindingBias, lx.blindingBiasText(bb))
	}

	if n := r.ComparableStudies; n != nil {
		fmt.Fprintf(b, "**%s:** %d\n\n", lx.comparable, *n)
	}

	if eb := r.EvidenceBase; eb != nil && eb.Summary != "" {
		fmt.Fprintf(b, "**%s:** %s\n\n", lx.evidenceBase, eb.Summary)
		b.WriteString(lx.studyList(eb))
	}
}

// writeMatrix writes the evidence matrix as a markdown table.
func writeMatrix(b *strings.Builder, lx lexicon, rows []model.MatrixRow) {
	b.WriteString("### " + lx.matrix + "\n\n")
	b.WriteString(lx.matrixHeader + "\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")

	for i := range rows {
		m := &rows[i]
		cell := shortTitle(m.Title, 55)

		if m.DOI != "" {
			cell = fmt.Sprintf("[%s](https://doi.org/%s)", cell, m.DOI)
		}

		fmt.Fprintf(b, "| %s | %d | %s | %s | %d/%d | %s | %s | %s | %s |\n",
			cell, m.Year, mdCell(orDash(m.Design)), numOrDash(m.SampleSize),
			m.Supporting, m.Contradicting, mdCell(orDash(m.Direction)),
			mdCell(orDash(m.DesignQuality)), mdCell(orDash(m.Relevance)), mdCell(orDash(m.Note)))
	}
}

// RenderCSV renders the matrix in a machine-readable form (for the
// ecosystem / downstream processing).
func RenderCSV(r *model.Report) (string, error) {
	records := make([][]string, 0, len(r.Matrix)+1)
	records = append(records, []string{
		"doi", "title", "year", "design", "sample_size",
		"supporting", "contradicting", "total_citations", "direction", "design_quality", "relevance", "note",
	})

	for i := range r.Matrix {
		m := &r.Matrix[i]
		records = append(records, []string{
			m.DOI, m.Title, strconv.Itoa(m.Year), m.Design, strconv.Itoa(m.SampleSize),
			strconv.Itoa(m.Supporting), strconv.Itoa(m.Contradicting),
			strconv.Itoa(m.TotalCitations), m.Direction, m.DesignQuality, m.Relevance, m.Note,
		})
	}

	var buf bytes.Buffer
	if err := csv.NewWriter(&buf).WriteAll(records); err != nil {
		return "", fmt.Errorf("encoding the matrix as CSV: %w", err)
	}

	return buf.String(), nil
}

// digestAbstractChars caps an abstract in the digest.
const digestAbstractChars = 900

// RenderContext is a compact digest of a run: everything an analyst needs to
// answer questions about it. Written to context.md and used as the base for
// `ask` and interactive agents.
func RenderContext(st *State) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Question: %s\n\n", st.Question)

	if st.Report != nil {
		writeVerdictDigest(&b, st.Report)
	}

	if t := english().directionTally(st); t != "" {
		fmt.Fprintf(&b, "Direction of direct studies: %s\n\n", t)
	}

	switch {
	case len(st.Enriched) > 0:
		fmt.Fprintf(&b, "## Papers (%d)\n", len(st.Enriched))

		crits := critiquesFor(st)
		for i := range st.Enriched {
			writePaperDigest(&b, &st.Enriched[i], crits[i])
		}
	case len(st.Papers) > 0:
		fmt.Fprintf(&b, "## Papers (%d)\n", len(st.Papers))

		for i := range st.Papers {
			p := &st.Papers[i]
			fmt.Fprintf(&b, "- %s — %s (%d)\n", orDash(p.DOI), p.Title, p.Year)
		}
	}

	if len(st.Warnings) > 0 {
		fmt.Fprintf(&b, "\nData warnings: %s\n", strings.Join(st.Warnings, " | "))
	}

	return b.String()
}

// writeVerdictDigest writes the verdict block of the digest. The digest is
// English whatever the report's language: it feeds the model, not the reader.
func writeVerdictDigest(b *strings.Builder, r *model.Report) {
	lx := english()

	fmt.Fprintf(b, "Verdict: %s.%s Consensus: %s.\n",
		lx.verdictLabel(r.Verdict), lx.effectDirectionSentence(r.EffectDirection), lx.consensusLabel(r.ConsensusStatus))

	if line := lx.pooledLine(r.PooledEstimate); line != "" {
		b.WriteString(line + "\n")
	}

	if r.Consensus != "" {
		fmt.Fprintf(b, "Field position: %s\n", r.Consensus)
	}

	if r.VerdictRationale != "" {
		fmt.Fprintf(b, "Rationale: %s\n", r.VerdictRationale)
	}

	if len(r.Contradictions) > 0 {
		fmt.Fprintf(b, "Contradictions: %s\n", strings.Join(r.Contradictions, " | "))
	}

	if len(r.Gaps) > 0 {
		fmt.Fprintf(b, "Gaps: %s\n", strings.Join(r.Gaps, " | "))
	}

	b.WriteString("\n")
}

// writePaperDigest writes one study: identity, design, citation tallies and
// the critique, with its abstract on the next line.
func writePaperDigest(b *strings.Builder, e *model.EnrichedPaper, c model.Critique) {
	sup, con, total := 0, 0, 0

	if e.Tally != nil {
		sup, con, total = e.Tally.Supporting, e.Tally.Contradicting, e.Tally.Total
	}

	quality, relevance, direction, n, concerns := "—", "—", "—", e.SampleSize, ""
	if hasCritique(c) {
		quality = orDash(c.DesignQuality)
		relevance = orDash(c.Relevance)
		direction = orDash(c.ResultDirection)

		if c.SampleSizeReported > 0 {
			n = c.SampleSizeReported
		}

		concerns = strings.Join(c.Concerns, "; ")
	}

	flag := ""
	if e.Flags.Retracted {
		flag = " [RETRACTED]"
	}

	if e.FullText != "" {
		flag += " [fulltext]"
	}

	fmt.Fprintf(b, "- %s%s — %s (%d); design: %s; N=%d; "+
		"Scite: %d support / %d contradict (of %d citations); "+
		"quality: %s; relevance: %s; direction: %s",
		orDash(e.DOI), flag, e.Title, e.Year, orDash(displayDesign(e, c)), n, sup, con, total, quality, relevance, direction)

	if concerns != "" {
		fmt.Fprintf(b, "; concerns: %s", concerns)
	}

	if len(e.ContradictingDOIs) > 0 {
		fmt.Fprintf(b, "; contradicted by: %s", strings.Join(e.ContradictingDOIs, ", "))
	}

	if ids := e.TrialIDs(); len(ids) > 0 {
		fmt.Fprintf(b, "; trial registration: %s", strings.Join(ids, ", "))
	}

	b.WriteString("\n")

	if e.Abstract != "" {
		fmt.Fprintf(b, "  Abstract: %s\n", truncate(e.Abstract, digestAbstractChars))
	}
}

// verdictLabel is the heading wording of the verdict level; a value outside the
// vocabulary is shown in capitals as it is.
func (l lexicon) verdictLabel(v string) string {
	if name, ok := l.verdictNames[strings.ToLower(v)]; ok {
		return name
	}

	return strings.ToUpper(v)
}

func (l lexicon) consensusLabel(s string) string {
	if name, ok := l.consensusNames[strings.ToLower(s)]; ok {
		return name
	}

	return orDash(s)
}

// effectDirectionLabel is the report's wording of the effect direction; a
// value outside the vocabulary (a hand-edited artifact) is shown as it is.
func (l lexicon) effectDirectionLabel(d model.EffectDirection) string {
	if name, ok := l.effectNames[d]; ok {
		return name
	}

	return orDash(string(d))
}

// effectDirectionSentence is the digest's sentence for the effect direction,
// with a leading space; empty for a report written before the field existed.
func (l lexicon) effectDirectionSentence(d model.EffectDirection) string {
	if d == "" {
		return ""
	}

	return " " + l.effectDirection + ": " + l.effectDirectionLabel(d) + "."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}

func sectionList(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}

	fmt.Fprintf(b, "\n### %s\n\n", title)

	for _, it := range items {
		fmt.Fprintf(b, "- %s\n", it)
	}

	b.WriteString("\n")
}

func shortTitle(s string, n int) string {
	s = strings.NewReplacer("|", "/", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	r := []rune(s)

	if len(r) <= n {
		return s
	}

	return string(r[:n-1]) + "…"
}

// mdCell makes a string safe for a markdown table cell (pipes and newlines
// would break the row).
func mdCell(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}

	return s
}

// directionTally summarizes where the direct studies' own findings point,
// e.g. "7 positive / 3 null / 2 negative" (mixed/unclear shown when present).
// Computed deterministically from critique fields, not by the LLM. Primary
// studies only: reviews and papers that are not study reports (errata,
// comments) are not counted, and a trial reported several times counts once,
// with the direction of the report that best matches the question's outcome
// and time frame (tallyIndexes). The words are the lexicon's.
func (l lexicon) directionTally(st *State) string {
	var pos, null, neg, mixed, unclear int

	crits := st.Critiques
	if len(st.Enriched) > 0 {
		crits = critiquesFor(st)
	}

	for _, i := range tallyIndexes(st, crits) {
		switch strings.ToLower(crits[i].ResultDirection) {
		case resultPositive:
			pos++
		case resultNull:
			null++
		case resultNegative:
			neg++
		case resultMixed:
			mixed++
		default:
			unclear++
		}
	}

	var parts []string

	for _, p := range []struct {
		n    int
		word string
	}{
		{pos, resultPositive},
		{null, resultNull},
		{neg, resultNegative},
		{mixed, resultMixed},
		{unclear, valueUnclear},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf(l.item, p.n, l.tallyNames[p.word]))
		}
	}

	return strings.Join(parts, " / ")
}

func numOrDash(n int) string {
	if n <= 0 {
		return "—"
	}

	return fmt.Sprintf("%d", n)
}
