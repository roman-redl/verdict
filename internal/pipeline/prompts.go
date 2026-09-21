package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/model"
)

// critiqueSystemPrompt — the map step: a strict methodologist reviewing one
// paper. Data comes only from what is provided; the sample size is reported
// only when it is actually stated in the abstract (otherwise the LLM
// invents one).
const critiqueSystemPrompt = `You are a rigorous methodologist and peer reviewer. You assess a single study's evidential value for a specific research question, based ONLY on the data provided (title, abstract or full text, design tags, citation tallies). Never invent facts; if data is missing, use the "unknown"/"unclear" values. Report sample size only if it is explicitly stated. When full_text is provided, it supersedes the abstract: read the methods and results sections, and report concerns invisible in the abstract (statistical issues, endpoint switching, dropout handling, unreported exclusions).

Respond with a single JSON object, no markdown, with exactly these fields:
doi (string, copy verbatim), title (string, original),
design_quality: "high" | "moderate" | "low" | "unknown",
sample_adequacy: "adequate" | "small" | "very_small" | "unclear" | "na",
sample_size_reported (integer, 0 if not stated),
relevance: "direct" | "indirect" | "tangential" | "unknown",
result_direction: "positive" | "null" | "negative" | "mixed" | "unclear" — the direction of THIS study's own findings relative to the research question (positive = supports the claim, null = no effect found, negative = opposite effect, mixed = inconsistent outcomes, unclear = not determinable from the abstract); judge from the reported results only, never from the title's framing,
strengths (array of short strings),
concerns (array of short strings).

Design quality guidance: meta-analysis / systematic review of RCTs = high; RCT = high; large prospective cohort = moderate; small cohort / case-control = low; case report / narrative review / animal or in-vitro only = low.`

// critiquePaper reviews a single paper; a call failure does not panic — it
// is returned in Critique.Error and the stage keeps working on the rest.
func critiquePaper(ctx context.Context, client llm.Completer, e model.EnrichedPaper, question string) model.Critique {
	c := model.Critique{
		DOI:             e.DOI,
		Title:           e.Title,
		DesignQuality:   "unknown",
		SampleAdequacy:  "unclear",
		Relevance:       "unknown",
		ResultDirection: "unclear",
	}
	input := map[string]any{
		"question":           question,
		"doi":                e.DOI,
		"title":              e.Title,
		"year":               e.Year,
		"authors":            cappedAuthors(e.Authors, 15),
		"venue":              e.Venue,
		"study_design":       e.StudyDesign,
		"cited_by_count":     e.CitedByCount,
		"abstract":           truncate(e.Abstract, 3500),
		"full_text":          truncate(e.FullText, 12000),
		"tldr":               e.TLDR, // when no abstract exists at all
		"scite_tally":        e.Tally,
		"contradicting_dois": e.ContradictingDOIs,
		"red_flags":          e.Flags,
	}
	b, _ := json.Marshal(input)
	var out model.Critique
	if err := llm.AskJSON(ctx, client, critiqueSystemPrompt, "Assess this study:\n\n"+string(b), &out, 2000); err != nil {
		c.Error = err.Error()
		return c
	}
	out.DOI = firstNonEmpty(out.DOI, e.DOI)
	out.Title = firstNonEmpty(out.Title, e.Title)
	out.DesignQuality = firstNonEmpty(out.DesignQuality, "unknown")
	out.SampleAdequacy = firstNonEmpty(out.SampleAdequacy, "unclear")
	out.Relevance = firstNonEmpty(out.Relevance, "unknown")
	out.ResultDirection = firstNonEmpty(out.ResultDirection, "unclear")
	return out
}

// synthesisSystemPrompt — the reduce step: aggregation into a verdict along
// a GRADE-style rubric. Key anti-illusions are baked into the rules: the
// absence of contradictions is not confirmation; recent papers naturally
// accumulate few citations.
const synthesisSystemPrompt = `You are an evidence assessor. You aggregate per-study data (metadata, Scite smart-citation tallies, per-study critiques) into an evidence assessment for a research question, following GRADE-style reasoning.

Respond with a single JSON object, no markdown, with exactly these fields:
- verdict: "strong" | "moderate" | "weak" | "insufficient"
- verdict_rationale (string): the chain of reasoning; MUST reference concrete DOIs that drive the verdict
- consensus_status: "broad_agreement" | "contested" | "emerging" | "no_consensus" | "insufficient_data"
- consensus (string): WHAT the field's majority position actually IS (direction, not strength) — 1-3 sentences with DOI examples; if contested, state both camps briefly
- matrix_notes (object: doi → short note): one note per input study — red flags or the essence of what this study contributes to the question; every study gets a note, even weak or failed ones (note the failure)
- contradictions (array of strings): unresolved conflicts, with DOIs; contradicting_dois in the input lists the papers Scite found citing a study as refuted — ground "who exactly is against" in them when present
- gaps (array of strings): what is missing to judge (no data, single study, few citations, publication-bias risk, ...)

Verdict definitions:
- strong: multiple independent studies of high/moderate design quality with adequate samples, consistent direction, no unresolved contradictions
- moderate: consistent but limited evidence (few studies, weaker designs, small samples, or indirect relevance)
- weak: sparse, low-quality, indirect, or unreplicated evidence
- insufficient: evidence body too small or too contradictory to judge

Consensus status definitions (about the DIRECTION of the field's position; verdict is about evidence STRENGTH — they are independent):
- broad_agreement: most direct, high/moderate-quality studies point the same way
- contested: strong studies genuinely disagree on direction
- emerging: few studies yet, a direction is taking shape
- no_consensus: enough studies, but no coherent position
- insufficient_data: too few direct studies to speak of consensus at all

Hard rules:
1. The numeric evidence matrix is assembled by the pipeline from the input; never recompute or invent numbers anywhere in your answer.
2. Absence of contradicting citations is NOT confirmation: Scite classifies only citations from full texts it has; recent papers naturally accumulate few citations.
3. Retracted papers (retracted=true) must be excluded from supporting evidence and mentioned in contradictions.
4. Give every input study a matrix note, even weak or failed ones (note the failure).
5. All human-readable text in English.
6. Build consensus ONLY from studies with relevance "direct", grounded in their structured result_direction fields: tally them first (e.g. "of 12 direct studies: 7 positive, 3 null, 2 negative"), then state where the field leans. Do not duplicate: verdict says how PROVEN it is, consensus_status/consensus say WHERE the field leans.
7. Compare author lists across studies: several studies from the same research group are ONE line of evidence, not independent replications — weigh them accordingly and note any group overlap in contradictions or gaps.`

// synthesisResult is the judge LLM's output. The evidence matrix is NOT part
// of it: rows are assembled deterministically in Go from the stage data, so
// numbers never pass through the model.
type synthesisResult struct {
	Verdict          string            `json:"verdict"`
	VerdictRationale string            `json:"verdict_rationale"`
	ConsensusStatus  string            `json:"consensus_status,omitempty"`
	Consensus        string            `json:"consensus,omitempty"`
	Notes            map[string]string `json:"matrix_notes,omitempty"` // doi → note
	Contradictions   []string          `json:"contradictions,omitempty"`
	Gaps             []string          `json:"gaps,omitempty"`
}

// buildMatrix assembles the evidence matrix from the enrichment and critique
// data; the LLM contributes only the per-study notes.
func buildMatrix(st *State, notes map[string]string) []model.MatrixRow {
	norm := make(map[string]string, len(notes))
	for k, v := range notes {
		norm[model.NormalizeDOI(k)] = v
	}
	crits := critiquesFor(st)
	rows := make([]model.MatrixRow, len(st.Enriched))
	for i, e := range st.Enriched {
		c := crits[i]
		row := model.MatrixRow{
			DOI:           e.DOI,
			Title:         e.Title,
			Year:          e.Year,
			Design:        e.StudyDesign,
			Direction:     firstNonEmpty(c.ResultDirection, "unclear"),
			DesignQuality: firstNonEmpty(c.DesignQuality, "unknown"),
			Relevance:     firstNonEmpty(c.Relevance, "unknown"),
			Note:          noteFor(c, norm[model.NormalizeDOI(e.DOI)]),
		}
		if e.Tally != nil {
			row.Supporting = e.Tally.Supporting
			row.Contradicting = e.Tally.Contradicting
			row.TotalCitations = e.Tally.Total
		} else {
			row.TotalCitations = e.CitedByCount
		}
		row.SampleSize = e.SampleSize
		if row.SampleSize == 0 {
			row.SampleSize = c.SampleSizeReported
		}
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

// synthesisUserPrompt compacts enriched papers + critiques into one dataset.
func synthesisUserPrompt(st *State) string {
	type item struct {
		DOI                  string               `json:"doi"`
		Title                string               `json:"title"`
		Year                 int                  `json:"year,omitempty"`
		Authors              []string             `json:"authors,omitempty"`
		Design               string               `json:"design,omitempty"`
		SampleSize           int                  `json:"sample_size,omitempty"`
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
	for i, e := range st.Enriched {
		it := item{
			DOI:                  e.DOI,
			Title:                e.Title,
			Year:                 e.Year,
			Authors:              cappedAuthors(e.Authors, 15),
			Design:               e.StudyDesign,
			SampleSize:           e.SampleSize,
			Abstract:             truncate(e.Abstract, 2500),
			CitedBy:              e.CitedByCount,
			TLDR:                 e.TLDR,
			Tally:                e.Tally,
			InfluentialCitations: e.InfluentialCitations,
			ContradictingDOIs:    e.ContradictingDOIs,
			Retracted:            e.Flags.Retracted,
		}
		if c := crits[i]; hasCritique(c) {
			cc := c
			it.Critique = &cc
			if it.SampleSize == 0 {
				it.SampleSize = cc.SampleSizeReported
			}
		}
		items = append(items, it)
	}
	b, _ := json.Marshal(map[string]any{"question": st.Question, "studies": items})
	return "Build the evidence assessment for this dataset:\n\n" + string(b)
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
const AskPreamble = `You are a scientific analyst answering follow-up questions about evidence-assessment runs. The collected contexts (papers, Scite tallies, critiques, verdicts) of several runs are provided below, each under a "===== RUN ... =====" header.

Audience and style:
- The user is an intelligent layperson, NOT a clinician. Explain every medical or technical term the first time you use it, in one short parenthetical (e.g. "IPL (интенсивный импульсный свет — не лазер и не ультрафиолет)"), and prefer plain wording over jargon throughout.
- Answer in the language of the user's question.
- Be concise by default; go deeper only when asked.
- When several runs are in context and they cover different questions, make clear which run's data you are answering from.

Evidence discipline:
- Answer ONLY from this data: cite the DOIs you rely on as short bracketed references.
- Label the KIND of evidence behind each claim (randomized trial / case series / clinician survey / narrative review): a survey of practice habits shows what doctors prescribe, not what works — never present it as efficacy evidence.
- Explicitly say when the data does not contain an answer. Never invent papers, numbers or citations.
- Distinguish clearly between "the data says X" and "generally known".
`

// RunState pairs a run directory name with its loaded state.
type RunState struct {
	Name  string
	State *State
}

// BuildAskSystem builds the analyst system prompt over one or several runs.
// The newest run gets the full digest; older runs are compact (no abstracts,
// papers already covered by a newer run skipped) — follow-ups mostly target
// the freshest evidence, and repeated abstracts only burn context.
func BuildAskSystem(runs []RunState) string {
	var sb strings.Builder
	sb.WriteString(AskPreamble)
	seen := map[string]bool{}
	markSeen := func(st *State) {
		for _, e := range st.Enriched {
			if d := model.NormalizeDOI(e.DOI); d != "" {
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
		r := st.Report
		fmt.Fprintf(&b, "Verdict: %s. Consensus: %s.\n", verdictLabel(r.Verdict), consensusLabel(r.ConsensusStatus))
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
	for i, e := range st.Enriched {
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
