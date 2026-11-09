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

// synthesisSystemPrompt — the reduce step: aggregation into a verdict along
// a GRADE-style rubric. Key anti-illusions are baked into the rules: the
// absence of contradictions is not confirmation; recent papers naturally
// accumulate few citations.
const synthesisSystemPrompt = `You are an evidence assessor. You aggregate per-study data (metadata, ` +
	`Scite smart-citation tallies, per-study critiques) into an evidence ` +
	`assessment for a research question, following GRADE-style reasoning.

Respond with a single JSON object, no markdown, with exactly these fields:
- verdict: "strong" | "moderate" | "weak" | "insufficient"
- verdict_rationale (string): the chain of reasoning; MUST reference concrete DOIs that drive the verdict
- effect_direction: "supports" | "no_effect" | "opposite" | "unclear" — the direction of the effect the evidence ` +
	`states relative to the claim in the question (supports = the claim holds; no_effect = the evidence shows ` +
	`no meaningful difference; opposite = the opposite effect; unclear = the direction cannot be stated: no ` +
	`direct study reports the outcome, or the direct studies conflict). It MUST agree with consensus and with ` +
	`the verdict rationale; the verdict says how certain this direction is. When the input has ` +
	`imprecision, the pipeline derives the final effect_direction from the pooled estimate and your claim_change
- claim_change: "lower" | "higher" — the change the CLAIM in the question implies for the intervention versus ` +
	`the comparator in the outcome AS MEASURED in pooled_estimate.outcome (not in the claim's own wording when ` +
	`they differ), taken from the claim and not from the evidence (e.g. "lowers the risk of stroke" → lower; ` +
	`claim "increases pain relief" with a pooled outcome "children still in pain" → lower). Without a ` +
	`pooled_estimate it is still required, read against the claim's own outcome
- consensus_status: "broad_agreement" | "contested" | "emerging" | "no_consensus" | "insufficient_data"
- consensus (string): WHAT the field's majority position actually IS (direction, not strength) — ` +
	`1-3 sentences with DOI examples; if contested, state both camps briefly
- matrix_notes (object: doi → short note): one note per DIRECT study — red flags or the essence of what ` +
	`this study contributes to the question; weak or failed direct studies get a note too (note the failure). ` +
	`Non-direct studies need no note: the pipeline auto-generates one from the critique concerns.
- contradictions (array of strings): unresolved conflicts, with DOIs; contradicting_dois in the input ` +
	`lists the papers Scite found citing a study as refuted — ground "who exactly is against" in them when present
- gaps (array of strings): what is missing to judge (no data, missing numbers, few citations, ` +
	`publication-bias risk, ...); a gap is not a downgrade by itself

Verdict definitions — the certainty that the stated direction (supports the claim / no effect / opposite) ` +
	`is right, rated GRADE-style over the direct studies:
- Start from the design: randomized trials start at strong, observational studies at weak.
- Lower one level for each SERIOUS problem in one of five domains and two levels for a VERY SERIOUS one, ` +
	`and name the domain; nothing else lowers the level, and one problem counts in one domain only:
  - risk of bias: a flaw that could plausibly change this outcome's result (allocation, missing data, ` +
	`selective or post hoc analysis). The flaw must be shown by the text; a detail the abstract does not ` +
	`report (dropouts, concealment) is a gap, not a flaw. Lack of blinding does not lower an objective outcome ` +
	`(death, survival, hospital admission, laboratory values);
  - inconsistency: the direct studies disagree in direction or in size beyond chance; variation between ` +
	`time points or subgroups of one study is not inconsistency;
  - indirectness: the population, intervention, comparator or outcome differ from the question enough to ` +
	`plausibly change the effect. Co-interventions given to both arms, a population narrower than the question ` +
	`or a different setting are not serious by themselves;
  - imprecision: when the input has imprecision, the pipeline computed it from the pooled estimate by GRADE ` +
	`rules: use its levels as the imprecision rating exactly and do not judge imprecision again; its target is ` +
	`the conclusion the levels apply to (little_or_no_difference = no effect; target "undetermined" means the ` +
	`computed rating gives no direction, as no threshold for a trivial effect is known: use the levels and ` +
	`judge the direction yourself). When imprecision is absent: ` +
	`judged from the confidence interval and the amount of data, not from the number of studies. ` +
	`Serious when the interval includes no effect, or includes both an important and a trivial effect; ` +
	`without an interval, serious when the data are small (under about 300 events, or 400 participants for a ` +
	`continuous outcome). Very serious when all the direct data are a few dozen participants (under about ` +
	`100 in total), whatever the interval or p-value; or when the interval includes no effect and reaches well ` +
	`into both a clear benefit and a clear harm (e.g. RR 0.5 to 2), not a bound just past no effect. Otherwise ` +
	`an interval that excludes no effect lowers at most one level, however wide; a narrow one that excludes ` +
	`no effect is precise, however few the trials;
  - publication bias: suspected when the evidence is a few small, positive, commercially funded studies.
- Raise observational evidence only for a very large, consistent effect.
- strong = high certainty, moderate = moderate, weak = low, insufficient = very low certainty ` +
	`or no direct study reporting the question's outcome.
- The number of studies and of research groups is not a domain: it lowers the level only through ` +
	`imprecision. One large, well-conducted randomized trial with a precise result stays strong; ` +
	`one adequately sized trial is moderate or strong, not insufficient.
- No effect is a conclusion, not the lack of one: when adequately powered direct studies consistently find ` +
	`no meaningful difference, the direction is "no effect" and the verdict rates the certainty of that. ` +
	`An underpowered null result is imprecision; it does not show the absence of an effect.

Consensus status definitions (about the DIRECTION of the field's position; ` +
	`verdict is about evidence STRENGTH — they are independent):
- broad_agreement: most direct, high/moderate-quality studies point the same way
- contested: strong studies genuinely disagree on direction
- emerging: one or a few direct studies point the same way (including "no effect")
- no_consensus: enough studies, but no coherent position
- insufficient_data: no direct study reports the question's outcome, so no direction can be stated

Hard rules:
1. The numeric evidence matrix is assembled by the pipeline from the input; ` +
	`never recompute or invent numbers anywhere in your answer.
2. Absence of contradicting citations is NOT confirmation: Scite classifies only citations from full texts it has; ` +
	`recent papers naturally accumulate few citations.
3. Retracted papers (retracted=true) must be excluded from supporting evidence and mentioned in contradictions.
4. Give every DIRECT study a matrix note, even weak or failed ones (note the failure); ` +
	`the pipeline fills in notes for the rest.
5. All human-readable text in English.
6. Build consensus ONLY from studies with relevance "direct", grounded in their structured result_direction ` +
	`fields: tally them first (e.g. "of 12 direct studies: 7 positive, 3 null, 2 negative"), then state where ` +
	`the field leans. Do not duplicate: verdict says how PROVEN it is, ` +
	`consensus_status/consensus say WHERE the field leans.
7. Compare author lists across studies: several studies from the same research group are ONE line of evidence, ` +
	`not independent replications — weigh them accordingly and note any group overlap in contradictions or gaps; ` +
	`the overlap alone does not lower the verdict.
8. When pooled_estimate is present, it is the pooled effect of the direct studies computed by the pipeline: ` +
	`use it for direction and imprecision (a CI that excludes no effect supports a direction; a narrow CI ` +
	`around no effect supports no_effect); never recompute it; individual non-significant studies do not ` +
	`outweigh a significant pooled estimate. The measure is intervention versus control: ratios (RR, OR, HR) ` +
	`have no effect at 1, differences (MD, SMD) at 0; read the sign against the change the claim states ` +
	`(e.g. a claim of lower risk is supported by RR below 1) before stating the direction. ` +
	`With method "single_study" nothing was pooled: the numbers are one ` +
	`study's own effect, and a CI that spans no effect with a point estimate near no effect is evidence for ` +
	`little or no difference only when the CI is narrow; a wide CI is imprecision, not no_effect. ` +
	`When imprecision is present, it replaces the imprecision reading above: its target is the conclusion the ` +
	`pooled estimate supports (read against the change the claim states; little_or_no_difference = no_effect; ` +
	`"undetermined" = you judge the direction), and its levels are the imprecision rating; do not state a ` +
	`different direction (unless the target is "undetermined") or rate imprecision again. ` +
	`Studies listed in left_out reported the outcome on another scale and are not in the pooled numbers: ` +
	`weigh them as individual results.
9. Systematic reviews and meta-analyses (see design and critique.study_design) are a cross-check of the ` +
	`primary studies, not independent evidence: do not count a review in the direct tally together with the ` +
	`trials it includes. The pooled_estimate never includes them.`

// synthesisResult is the judge LLM's output. The evidence matrix is NOT part
// of it: rows are assembled deterministically in Go from the stage data, so
// numbers never pass through the model.
type synthesisResult struct {
	Verdict          string            `json:"verdict"`
	VerdictRationale string            `json:"verdict_rationale"`
	EffectDirection  string            `json:"effect_direction,omitempty"`
	ClaimChange      string            `json:"claim_change,omitempty"`
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
			SampleSize:           e.SampleSize,
			CitedBy:              e.CitedByCount,
			TLDR:                 e.TLDR,
			Tally:                e.Tally,
			InfluentialCitations: e.InfluentialCitations,
			ContradictingDOIs:    e.ContradictingDOIs,
			Retracted:            e.Flags.Retracted,
		}

		if c := crits[i]; hasCritique(c) {
			cc := c
			cc.Effect = usableEffect(c.Effect) // the stored critique keeps the critic's numbers as they are
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

	data := map[string]any{"question": st.Question, "studies": items}
	if st.Pooled != nil {
		data["pooled_estimate"] = st.Pooled
	}

	if st.Imprecision != nil {
		data["imprecision"] = st.Imprecision
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
		r := st.Report
		fmt.Fprintf(&b, "Verdict: %s.%s Consensus: %s.\n",
			verdictLabel(r.Verdict), effectDirectionSentence(r.EffectDirection), consensusLabel(r.ConsensusStatus))

		if line := pooledLine(r.PooledEstimate); line != "" {
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
