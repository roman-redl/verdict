package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/roman-redl/verdict/internal/llm"
)

// PICO is a research question normalized through the evidence-medicine
// frame (Population, Intervention, Comparison, Outcome). Sharpening a
// messy question into PICO before discovery improves every search mode —
// and both query flavors (keyword for mode A, natural language for mode C)
// come out ready to run.
type PICO struct {
	Question        string   `json:"question"`
	Population      string   `json:"population,omitempty"`
	Intervention    string   `json:"intervention,omitempty"`
	Comparison      string   `json:"comparison,omitempty"`
	Outcome         string   `json:"outcome,omitempty"`
	KeywordQuery    string   `json:"keyword_query"`    // mode A: 2-4 keywords, PubMed-style
	SemanticQueries []string `json:"semantic_queries"` // mode C: 1-3 semantic queries (Consensus)
	Notes           string   `json:"notes,omitempty"`
}

const picoSystemPrompt = `You normalize a research question for literature search using the PICO frame (Population, Intervention, Comparison, Outcome).

Respond with a single JSON object, no markdown, with exactly these fields:
- question: the question restated as one self-contained, unambiguous English sentence
- population, intervention, comparison, outcome: the four PICO components (comparison may be empty when there is none)
- keyword_query: a PubMed-style query — 2-4 keywords, no stopwords, no full sentences (keyword engines match words, not meaning)
- semantic_queries: 1-3 full natural-language queries for semantic search (consensus.app) covering the meaningful variants of the question (different populations or formulations) so they can be run in the free web UI
- notes: ambiguities or sub-questions that deserve separate runs, empty if none

All human-readable text in English.`

// PICOFromQuestion normalizes a (possibly messy) question into PICO.
func PICOFromQuestion(ctx context.Context, client llm.Completer, question string) (*PICO, error) {
	var p PICO
	if err := llm.AskJSON(ctx, client, picoSystemPrompt, truncate(question, 2000), &p, 1500); err != nil {
		return nil, err
	}
	return &p, nil
}

// RenderPICO renders the normalized question as markdown.
func RenderPICO(p *PICO) string {
	var sb strings.Builder
	sb.WriteString("# PICO\n\n")
	fmt.Fprintf(&sb, "**Question:** %s\n\n", orDash(p.Question))
	fmt.Fprintf(&sb, "- Population: %s\n", orDash(p.Population))
	fmt.Fprintf(&sb, "- Intervention: %s\n", orDash(p.Intervention))
	fmt.Fprintf(&sb, "- Comparison: %s\n", orDash(p.Comparison))
	fmt.Fprintf(&sb, "- Outcome: %s\n", orDash(p.Outcome))
	fmt.Fprintf(&sb, "\nKeyword query (mode A): `%s`\n", orDash(p.KeywordQuery))
	sb.WriteString("\nSemantic queries (mode C, free Consensus web UI):\n")
	for _, q := range p.SemanticQueries {
		fmt.Fprintf(&sb, "  - %s\n", q)
	}
	if p.Notes != "" {
		fmt.Fprintf(&sb, "\nNotes: %s\n", p.Notes)
	}
	return sb.String()
}
