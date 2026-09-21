package pipeline

import (
	"bytes"
	"encoding/csv"
	"fmt"
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
	for _, m := range r.Matrix {
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
	return b.String()
}

// RenderCSV renders the matrix in a machine-readable form (for the
// ecosystem / downstream processing).
func RenderCSV(r *model.Report) string {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"doi", "title", "year", "design", "sample_size",
		"supporting", "contradicting", "total_citations", "direction", "design_quality", "relevance", "note"})
	for _, m := range r.Matrix {
		_ = w.Write([]string{
			m.DOI, m.Title, fmt.Sprintf("%d", m.Year), m.Design, fmt.Sprintf("%d", m.SampleSize),
			fmt.Sprintf("%d", m.Supporting), fmt.Sprintf("%d", m.Contradicting),
			fmt.Sprintf("%d", m.TotalCitations), m.Direction, m.DesignQuality, m.Relevance, m.Note,
		})
	}
	w.Flush()
	return buf.String()
}

// RenderContext is a compact digest of a run: everything an analyst needs to
// answer questions about it. Written to context.md and used as the base for
// `ask` and interactive agents.
func RenderContext(st *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n\n", st.Question)
	if st.Report != nil {
		r := st.Report
		fmt.Fprintf(&b, "Verdict: %s. Consensus: %s.\n", verdictLabel(r.Verdict), consensusLabel(r.ConsensusStatus))
		if r.Consensus != "" {
			fmt.Fprintf(&b, "Field position: %s\n", r.Consensus)
		}
		if r.VerdictRationale != "" {
			fmt.Fprintf(&b, "Rationale: %s\n", r.VerdictRationale)
		}
		if len(r.Contradictions) > 0 {
			fmt.Fprintf(&b, "Contradictions: %s\n", strings.Join(r.Contradictions, " | "))
		}
		if len(r.Gaps) > 0 {
			fmt.Fprintf(&b, "Gaps: %s\n", strings.Join(r.Gaps, " | "))
		}
		b.WriteString("\n")
	}
	if t := directionTally(st.Critiques); t != "" {
		fmt.Fprintf(&b, "Direction of direct studies: %s\n\n", t)
	}
	crits := critiquesFor(st)
	if len(st.Enriched) > 0 {
		fmt.Fprintf(&b, "## Papers (%d)\n", len(st.Enriched))
		for i, e := range st.Enriched {
			sup, con, total := 0, 0, 0
			if e.Tally != nil {
				sup, con, total = e.Tally.Supporting, e.Tally.Contradicting, e.Tally.Total
			}
			quality, relevance, direction, n, concerns := "—", "—", "—", e.SampleSize, ""
			if c := crits[i]; hasCritique(c) {
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
			fmt.Fprintf(&b, "- %s%s — %s (%d); design: %s; N=%d; Scite: %d support / %d contradict (of %d citations); quality: %s; relevance: %s; direction: %s",
				orDash(e.DOI), flag, e.Title, e.Year, orDash(e.StudyDesign), n, sup, con, total, quality, relevance, direction)
			if concerns != "" {
				fmt.Fprintf(&b, "; concerns: %s", concerns)
			}
			if len(e.ContradictingDOIs) > 0 {
				fmt.Fprintf(&b, "; contradicted by: %s", strings.Join(e.ContradictingDOIs, ", "))
			}
			b.WriteString("\n")
			if e.Abstract != "" {
				fmt.Fprintf(&b, "  Abstract: %s\n", truncate(e.Abstract, 900))
			}
		}
	} else if len(st.Papers) > 0 {
		fmt.Fprintf(&b, "## Papers (%d)\n", len(st.Papers))
		for _, p := range st.Papers {
			fmt.Fprintf(&b, "- %s — %s (%d)\n", orDash(p.DOI), p.Title, p.Year)
		}
	}
	if len(st.Warnings) > 0 {
		fmt.Fprintf(&b, "\nData warnings: %s\n", strings.Join(st.Warnings, " | "))
	}
	return b.String()
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
	for _, c := range critiques {
		if c.Relevance != "direct" {
			continue
		}
		direct = true
		switch strings.ToLower(c.ResultDirection) {
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
		{pos, "positive"}, {null, "null"}, {neg, "negative"},
		{mixed, "mixed"}, {unclear, "unclear"},
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
