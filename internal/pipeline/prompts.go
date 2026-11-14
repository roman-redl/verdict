package pipeline

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// critiqueSystemPrompt and critiquePaper (the critic stage) live in
// critique.go: the stage is batched, with the single-study form as fallback.

// buildMatrix assembles the evidence matrix from the enrichment and critique
// data; the LLM contributes only the per-study notes.
func buildMatrix(st *State, notes map[string]string) []model.MatrixRow {
	norm := make(map[string]string, len(notes))
	for k, v := range notes {
		norm[model.NormalizeDOI(k)] = v
	}

	crits := critiquesFor(st)
	rows := make([]model.MatrixRow, len(st.Enriched))

	for i := range st.Enriched {
		e := &st.Enriched[i]
		c := crits[i]

		row := model.MatrixRow{
			DOI:           e.DOI,
			Title:         e.Title,
			Year:          e.Year,
			Design:        displayDesign(e, c),
			Direction:     cmp.Or(c.ResultDirection, valueUnclear),
			DesignQuality: cmp.Or(c.DesignQuality, valueUnknown),
			Relevance:     cmp.Or(c.Relevance, valueUnknown),
			Note:          noteFor(c, norm[model.NormalizeDOI(e.DOI)]),
		}
		if e.Tally != nil {
			row.Supporting = e.Tally.Supporting
			row.Contradicting = e.Tally.Contradicting
			row.TotalCitations = e.Tally.Total
		} else {
			row.TotalCitations = e.CitedByCount
		}

		row.SampleSize = cmp.Or(c.SampleSizeReported, e.SampleSize)

		rows[i] = row
	}

	return rows
}

// noteFor prefers the LLM's note; without one, the critique's leading
// concerns stand in.
func noteFor(c model.Critique, llmNote string) string {
	if strings.TrimSpace(llmNote) != "" {
		return llmNote
	}

	if len(c.Concerns) > 2 {
		return strings.Join(c.Concerns[:2], "; ")
	}

	return strings.Join(c.Concerns, "; ")
}

// hasCritique reports whether a critique carries any payload (a zero-value
// critique means "no critique for this paper").
func hasCritique(c model.Critique) bool {
	return c.DOI != "" || c.Title != "" || c.DesignQuality != "" ||
		c.Relevance != "" || len(c.Strengths) > 0 || len(c.Concerns) > 0 || c.Error != ""
}

// judgeCritique is the copy of a critique that goes to the synthesis model:
// the cleaned effect (the stored critique keeps the critic's numbers as they
// are) and none of the pipeline's markers, which are resume bookkeeping and
// mean nothing to the judge. The state keeps them; usableEffect returns a
// copy, so clearing the effect's markers does not touch the original.
func judgeCritique(c model.Critique) model.Critique {
	c.Effect = usableEffect(c.Effect)
	c.EffectAsked, c.MatchAdjudicated = false, false

	if c.Effect != nil {
		c.Effect.OutcomeMatchCritic, c.Effect.TimepointMatchCritic = "", ""
	}

	return c
}

// synthesisUserPrompt compacts enriched papers + critiques into one dataset.
// Abstracts ride along only where the verdict is actually argued — direct
// studies, and studies whose critique is missing or failed (the abstract is
// then all the judge has). Everywhere else the critique already carries what
// the abstract said; resending the text would pay twice for the same read.
func synthesisUserPrompt(st *State) (string, error) {
	type item struct {
		DOI                  string               `json:"doi"`
		Title                string               `json:"title"`
		Year                 int                  `json:"year,omitempty"`
		Authors              []string             `json:"authors,omitempty"`
		Design               string               `json:"design,omitempty"`
		SampleSize           int                  `json:"sample_size,omitempty"`
		TrialRegistrations   []string             `json:"trial_registrations,omitempty"`
		Abstract             string               `json:"abstract,omitempty"`
		CitedBy              int                  `json:"cited_by_count,omitempty"`
		TLDR                 string               `json:"tldr,omitempty"` // when no abstract exists
		Tally                *model.CitationTally `json:"scite,omitempty"`
		InfluentialCitations int                  `json:"influential_citations,omitempty"`
		ContradictingDOIs    []string             `json:"contradicting_dois,omitempty"`
		Retracted            bool                 `json:"retracted,omitempty"`
		Critique             *model.Critique      `json:"critique,omitempty"`
	}

	crits := critiquesFor(st)
	items := make([]item, 0, len(st.Enriched))

	for i := range st.Enriched {
		e := &st.Enriched[i]
		it := item{
			DOI:                  e.DOI,
			Title:                e.Title,
			Year:                 e.Year,
			Authors:              cappedAuthors(e.Authors, 15),
			Design:               displayDesign(e, crits[i]),
			SampleSize:           cmp.Or(crits[i].SampleSizeReported, e.SampleSize),
			TrialRegistrations:   e.TrialIDs(),
			CitedBy:              e.CitedByCount,
			TLDR:                 e.TLDR,
			Tally:                e.Tally,
			InfluentialCitations: e.InfluentialCitations,
			ContradictingDOIs:    e.ContradictingDOIs,
			Retracted:            e.Flags.Retracted,
		}

		if c := crits[i]; hasCritique(c) {
			cc := judgeCritique(c)
			it.Critique = &cc

			if it.SampleSize == 0 {
				it.SampleSize = cc.SampleSizeReported
			}

			if cc.Error != "" || cc.Relevance == relevanceDirect {
				it.Abstract = truncate(e.Abstract, 1200)
			}
		} else {
			it.Abstract = truncate(e.Abstract, 1200)
		}

		items = append(items, it)
	}

	data := map[string]any{
		"question":            st.Question,
		"studies":             items,
		"inconsistency_basis": inconsistencyBasis{ComparableStudies: comparableStudies(st)},
	}

	if st.Pooled != nil {
		data["pooled_estimate"] = st.Pooled
	}

	if st.Imprecision != nil {
		data["imprecision"] = st.Imprecision
	}

	if st.BlindingBias != nil {
		data["blinding_bias"] = st.BlindingBias
	}

	b, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("encoding the dataset: %w", err)
	}

	return "Build the evidence assessment for this dataset:\n\n" + string(b), nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[:n]) + "…"
}

// cappedAuthors keeps prompts small on mega-authorship papers while the
// leading names remain enough to spot research-group overlap.
func cappedAuthors(a []string, n int) []string {
	if len(a) <= n {
		return a
	}

	rest := append([]string{}, a[:n]...)

	return append(rest, fmt.Sprintf("… and %d more", len(a)-n))
}

// AskPreamble holds the run-analyst rules: answer only from the data, cite
// DOIs, label the kind of evidence, explain terms for a layperson.
const AskPreamble = `You are a scientific analyst answering follow-up questions about evidence-assessment runs. ` +
	`The collected contexts (papers, Scite tallies, critiques, verdicts) of several runs are provided below, ` +
	`each under a "===== RUN ... =====" header.

Audience and style:
- The user is an intelligent layperson, NOT a clinician. Explain every medical or technical term the first time ` +
	`you use it, in one short parenthetical (e.g. "IPL (intense pulsed light — not a laser and not ultraviolet)"), ` +
	`and prefer plain wording over jargon throughout.
- Answer in the language of the user's question.
- Be concise by default; go deeper only when asked.
- When several runs are in context and they cover different questions, ` +
	`make clear which run's data you are answering from.

Evidence discipline:
- Answer ONLY from this data: cite the DOIs you rely on as short bracketed references.
- Label the KIND of evidence behind each claim ` +
	`(randomized trial / case series / clinician survey / narrative review): ` +
	`a survey of practice habits shows what doctors prescribe, not what works — ` +
	`never present it as efficacy evidence.
- Explicitly say when the data does not contain an answer. Never invent papers, numbers or citations.
- Distinguish clearly between "the data says X" and "generally known".
`

// BuildAskSystem builds the analyst system prompt over one or several runs.
// The newest run gets the full digest; older runs are compact (no abstracts,
// papers already covered by a newer run skipped) — follow-ups mostly target
// the freshest evidence, and repeated abstracts only burn context.
func BuildAskSystem(runs []RunState) string {
	var sb strings.Builder

	sb.WriteString(AskPreamble)

	seen := map[string]bool{}
	markSeen := func(st *State) {
		for i := range st.Enriched {
			if d := model.NormalizeDOI(st.Enriched[i].DOI); d != "" {
				seen[d] = true
			}
		}
	}

	for i, r := range runs {
		if i == 0 {
			markSeen(r.State)
			fmt.Fprintf(&sb, "\n===== RUN: %s (newest, full context) =====\nQuestion: %s\n\n%s\n",
				r.Name, r.State.Question, RenderContext(r.State))

			continue
		}

		fmt.Fprintf(&sb, "\n===== RUN: %s (older, compact) =====\nQuestion: %s\n\n%s\n",
			r.Name, r.State.Question, renderContextCompact(r.State, seen))
		markSeen(r.State)
	}

	return sb.String()
}

// renderContextCompact is the older-run digest: verdict, per-paper one-liner,
// no abstracts; papers already covered by a newer run are skipped.
func renderContextCompact(st *State, skip map[string]bool) string {
	var b strings.Builder

	if st.Report != nil {
		r, lx := st.Report, english() // the digests are English: they feed the model, not the reader
		fmt.Fprintf(&b, "Verdict: %s.%s Consensus: %s.\n",
			lx.verdictLabel(r.Verdict), lx.effectDirectionSentence(r.EffectDirection), lx.consensusLabel(r.ConsensusStatus))

		if line := lx.pooledLine(r.PooledEstimate); line != "" {
			b.WriteString(line + "\n")
		}

		if r.VerdictRationale != "" {
			fmt.Fprintf(&b, "Rationale: %s\n", truncate(r.VerdictRationale, 1200))
		}

		if len(r.Contradictions) > 0 {
			fmt.Fprintf(&b, "Contradictions: %s\n", strings.Join(r.Contradictions, " | "))
		}

		b.WriteString("\n")
	}

	crits := critiquesFor(st)
	skipped := 0

	fmt.Fprintf(&b, "## Papers (%d)\n", len(st.Enriched))

	for i := range st.Enriched {
		e := &st.Enriched[i]
		if d := model.NormalizeDOI(e.DOI); d != "" && skip[d] {
			skipped++
			continue
		}

		flag := ""
		if e.Flags.Retracted {
			flag = " [RETRACTED]"
		}

		quality, relevance, direction := "—", "—", "—"
		if c := crits[i]; hasCritique(c) {
			quality, relevance, direction = orDash(c.DesignQuality), orDash(c.Relevance), orDash(c.ResultDirection)
		}

		sup, con := 0, 0
		if e.Tally != nil {
			sup, con = e.Tally.Supporting, e.Tally.Contradicting
		}

		fmt.Fprintf(&b, "- %s%s — %s (%d); Scite: %d/%d; quality: %s; relevance: %s; direction: %s\n",
			orDash(e.DOI), flag, e.Title, e.Year, sup, con, quality, relevance, direction)
	}

	if skipped > 0 {
		fmt.Fprintf(&b, "(%d paper(s) of this run are already listed under a newer run)\n", skipped)
	}

	if len(st.Warnings) > 0 {
		fmt.Fprintf(&b, "\nData warnings: %s\n", strings.Join(st.Warnings, " | "))
	}

	return b.String()
}
