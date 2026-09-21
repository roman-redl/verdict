package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/roman-redl/verdict/internal/llm"
)

// Brief is the triage result for a messy real-life problem: what a capable
// agent settles on its own, and which scientific questions go to the
// evidence pipeline (`verdict run`).
type Brief struct {
	Problem   string          `json:"problem"`
	Decision  string          `json:"decision"`
	Tasks     []BriefTask     `json:"agent_tasks"`
	Questions []BriefQuestion `json:"evidence_questions"`
	Claims    []BriefClaim    `json:"claims_to_verify"`
	Warning   string          `json:"warning,omitempty"`
}

type BriefTask struct {
	Task string `json:"task"`
	Kind string `json:"kind"` // shopping | engineering | logistics | planning | other
	Note string `json:"note,omitempty"`
}

type BriefQuestion struct {
	Question string `json:"question"`
	Why      string `json:"why"`
	Priority string `json:"priority"` // high | medium | low
}

type BriefClaim struct {
	Claim  string `json:"claim"`
	Source string `json:"source,omitempty"`
	Check  string `json:"check"` // agent | verdict
}

const briefSystemPrompt = `You are a research triage analyst. The user sends a messy real-life problem description, often a dialogue with another AI assistant. Split it into what a capable agent can settle on its own (shopping, engineering, logistics, spec sheets, arithmetic, planning) versus what requires a scientific evidence assessment. Also extract the concrete factual claims made along the way that deserve verification.

Respond with a single JSON object, no markdown, with exactly these fields:
- problem: a 1-2 sentence restatement
- decision: the decision the problem actually hinges on
- agent_tasks: array of {task, kind (shopping|engineering|logistics|planning|other), note} — things an agent settles without scientific literature
- evidence_questions: array of {question, why, priority (high|medium|low)} — 2-6 self-contained research questions in English, answerable from the scientific literature, ordered by importance; each must be usable verbatim as a literature search query
- claims_to_verify: array of {claim, source, check} — notable factual claims from the text; check="agent" for spec/price/arithmetic claims, check="verdict" for medical or scientific ones
- warning: a short medical-safety note if the problem touches diagnosis or dosing (defer to a physician), empty otherwise

All human-readable text in English.`

// BriefFromText runs the triage over a problem description.
func BriefFromText(ctx context.Context, client llm.Completer, text string) (*Brief, error) {
	var b Brief
	if err := llm.AskJSON(ctx, client, briefSystemPrompt, truncate(text, 24000), &b, 4000); err != nil {
		return nil, err
	}
	return &b, nil
}

// RenderBrief renders the triage result as markdown.
func RenderBrief(b *Brief) string {
	var sb strings.Builder
	sb.WriteString("# Research brief\n\n")
	fmt.Fprintf(&sb, "**Problem:** %s\n\n", b.Problem)
	fmt.Fprintf(&sb, "**Decision:** %s\n\n", b.Decision)
	if len(b.Tasks) > 0 {
		sb.WriteString("## For the agent (no science needed)\n\n")
		for _, t := range b.Tasks {
			fmt.Fprintf(&sb, "- **%s** %s\n", orDash(t.Kind), t.Task)
			if t.Note != "" {
				fmt.Fprintf(&sb, "  - %s\n", t.Note)
			}
		}
		sb.WriteString("\n")
	}
	if len(b.Questions) > 0 {
		sb.WriteString("## For verdict (evidence questions)\n\n")
		for i, q := range b.Questions {
			fmt.Fprintf(&sb, "%d. **[%s]** %s\n", i+1, orDash(q.Priority), q.Question)
			if q.Why != "" {
				fmt.Fprintf(&sb, "   _%s_\n", q.Why)
			}
		}
		sb.WriteString("\n")
	}
	if len(b.Claims) > 0 {
		sb.WriteString("## Claims to verify\n\n")
		for _, c := range b.Claims {
			fmt.Fprintf(&sb, "- **[%s]** %s", orDash(c.Check), c.Claim)
			if c.Source != "" {
				fmt.Fprintf(&sb, " _(source: %s)_", c.Source)
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	if b.Warning != "" {
		fmt.Fprintf(&sb, "> ⚠️ %s\n", b.Warning)
	}
	return sb.String()
}
