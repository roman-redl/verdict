package pipeline

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// RenderMarkdown renders the human-readable report.md from the final state.
func RenderMarkdown(st *State) string {
	r := st.Report

	var b strings.Builder

	b.WriteString("# Evidence Report\n\n")
	fmt.Fprintf(&b, "**Question:** %s\n\n", r.Question)
	fmt.Fprintf(&b, "## Verdict: %s\n\n", verdictLabel(r.Verdict))

	if r.EffectDirection != "" {
		fmt.Fprintf(&b, "**Effect direction:** %s\n\n", effectDirectionLabel(r.EffectDirection))
	}

	if pe := r.PooledEstimate; pe != nil {
		fmt.Fprintf(&b, "**%s:** %s\n\n", pooledLabel(pe), pooledText(pe))
	}

	if imp := r.Imprecision; imp != nil {
		fmt.Fprintf(&b, "**Imprecision (computed):** %s\n\n", imprecisionText(imp))
	}

	if r.Consensus != "" || r.ConsensusStatus != "" {
		fmt.Fprintf(&b, "### Scientific consensus: %s\n\n", consensusLabel(r.ConsensusStatus))

		if r.Consensus != "" {
			b.WriteString(r.Consensus + "\n\n")
		}
	}

	if t := directionTally(st.Critiques); t != "" {
		fmt.Fprintf(&b, "**Direction of direct studies:** %s\n\n", t)
	}

	fmt.Fprintf(&b, "_Generated: %s_\n\n", r.GeneratedAt.Format("2006-01-02 15:04 UTC"))
	b.WriteString("### Rationale\n\n" + r.VerdictRationale + "\n\n")

	b.WriteString("### Evidence matrix\n\n")
	b.WriteString("| Study | Year | Design | N | Sup/Contra | Direction | Quality | Relevance | Note |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")

	for i := range r.Matrix {
		m := &r.Matrix[i]
		cell := shortTitle(m.Title, 55)

		if m.DOI != "" {
			cell = fmt.Sprintf("[%s](https://doi.org/%s)", cell, m.DOI)
		}

		fmt.Fprintf(&b, "| %s | %d | %s | %s | %d/%d | %s | %s | %s | %s |\n",
			cell, m.Year, mdCell(orDash(m.Design)), numOrDash(m.SampleSize),
			m.Supporting, m.Contradicting, mdCell(orDash(m.Direction)),
			mdCell(orDash(m.DesignQuality)), mdCell(orDash(m.Relevance)), mdCell(orDash(m.Note)))
	}

	sectionList(&b, "Contradictions", r.Contradictions)
	sectionList(&b, "Gaps and limitations", r.Gaps)
	sectionList(&b, "Data warnings", r.Warnings)

	if line := st.Cost.Markdown(); line != "" {
		b.WriteString("\n_" + line + "_\n")
	}

	return b.String()
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

	if t := directionTally(st.Critiques); t != "" {
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

// writeVerdictDigest writes the verdict block of the digest.
func writeVerdictDigest(b *strings.Builder, r *model.Report) {
	fmt.Fprintf(b, "Verdict: %s.%s Consensus: %s.\n",
		verdictLabel(r.Verdict), effectDirectionSentence(r.EffectDirection), consensusLabel(r.ConsensusStatus))

	if line := pooledLine(r.PooledEstimate); line != "" {
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
		orDash(e.DOI), flag, e.Title, e.Year, orDash(e.StudyDesign), n, sup, con, total, quality, relevance, direction)

	if concerns != "" {
		fmt.Fprintf(b, "; concerns: %s", concerns)
	}

	if len(e.ContradictingDOIs) > 0 {
		fmt.Fprintf(b, "; contradicted by: %s", strings.Join(e.ContradictingDOIs, ", "))
	}

	b.WriteString("\n")

	if e.Abstract != "" {
		fmt.Fprintf(b, "  Abstract: %s\n", truncate(e.Abstract, digestAbstractChars))
	}
}

func verdictLabel(v string) string {
	switch strings.ToLower(v) {
	case "strong":
		return "STRONG — well-established evidence"
	case "moderate":
		return "MODERATE — limited but consistent evidence"
	case "weak":
		return "WEAK — sparse or low-quality evidence"
	case "insufficient":
		return "INSUFFICIENT — not enough evidence to judge"
	}

	return strings.ToUpper(v)
}

func consensusLabel(s string) string {
	switch strings.ToLower(s) {
	case "broad_agreement":
		return "broad agreement"
	case "contested":
		return "contested"
	case "emerging":
		return "emerging"
	case "no_consensus":
		return "no consensus"
	case "insufficient_data":
		return "insufficient data for consensus"
	}

	return orDash(s)
}

// effectDirectionLabel is the report's wording of the effect direction; a
// value outside the vocabulary (a hand-edited artifact) is shown as it is.
func effectDirectionLabel(d model.EffectDirection) string {
	switch d {
	case model.EffectSupports:
		return "supports the claim"
	case model.EffectNoEffect:
		return "no meaningful effect"
	case model.EffectOpposite:
		return "opposite effect"
	case model.EffectUnclear:
		return "cannot be stated"
	}

	return orDash(string(d))
}

// effectDirectionSentence is the digest's sentence for the effect direction,
// with a leading space; empty for a report written before the field existed.
func effectDirectionSentence(d model.EffectDirection) string {
	if d == "" {
		return ""
	}

	return " Effect direction: " + effectDirectionLabel(d) + "."
}

// pooledLabel names what pooledText shows.
func pooledLabel(pe *model.PooledEstimate) string {
	if pe.Method == model.PoolSingleStudy {
		return "Effect of the one direct study with numbers (not pooled)"
	}

	return "Pooled effect of the direct studies (random effects, DerSimonian-Laird)"
}

// pooledText renders the pooled numbers; Go formats them, the LLM never
// retypes them.
func pooledText(pe *model.PooledEstimate) string {
	// one precision for the three numbers: two decimals, three when any of
	// them is small, so that a small effect does not read as zero
	prec := 2

	for _, v := range []float64{pe.Estimate, pe.CILow, pe.CIHigh} {
		if a := math.Abs(v); a != 0 && a < 0.1 {
			prec = 3
		}
	}

	est := fmt.Sprintf("%s %.*f", pe.Measure, prec, pe.Estimate)
	if pe.Unit != "" {
		est += " " + pe.Unit
	}

	s := fmt.Sprintf("%s (95%% CI %.*f to %.*f), %d %s", est, prec, pe.CILow, prec, pe.CIHigh,
		pe.K, plural(pe.K, "study", "studies"))

	if pe.Participants > 0 {
		s += fmt.Sprintf(", %d participants", pe.Participants)
	}

	if pe.I2 != nil {
		s += fmt.Sprintf(", I² = %.0f%%", *pe.I2)
	}

	if pe.Outcome != "" {
		s += "; outcome: " + pe.Outcome
	}

	return s
}

// imprecisionText renders the computed rating: the levels, then the reason
// (a sentence with the numbers).
func imprecisionText(imp *model.Imprecision) string {
	s := "not rated down"
	if imp.Levels > 0 {
		s = fmt.Sprintf("rated down %d %s", imp.Levels, plural(imp.Levels, "level", "levels"))
	}

	return s + " — " + imp.Reason
}

// pooledLine is the one-line digest entry; empty without a pooled estimate.
func pooledLine(pe *model.PooledEstimate) string {
	if pe == nil {
		return ""
	}

	return pooledLabel(pe) + ": " + pooledText(pe) + "."
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
	s = strings.NewReplacer("|", "/", "\n", " ").Replace(s)
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
// Computed deterministically from critique fields, not by the LLM.
func directionTally(critiques []model.Critique) string {
	var pos, null, neg, mixed, unclear int

	direct := false

	for i := range critiques {
		if critiques[i].Relevance != relevanceDirect {
			continue
		}

		direct = true

		switch strings.ToLower(critiques[i].ResultDirection) {
		case "positive":
			pos++
		case "null":
			null++
		case "negative":
			neg++
		case "mixed":
			mixed++
		default:
			unclear++
		}
	}

	if !direct {
		return ""
	}

	var parts []string

	for _, p := range []struct {
		n    int
		name string
	}{
		{pos, "positive"},
		{null, "null"},
		{neg, "negative"},
		{mixed, "mixed"},
		{unclear, valueUnclear},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.name))
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
