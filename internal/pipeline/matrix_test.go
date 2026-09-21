package pipeline

import (
	"strings"
	"testing"

	"github.com/roman-redl/verdict/internal/model"
)

func TestBuildMatrix(t *testing.T) {
	st := &State{
		Enriched: []model.EnrichedPaper{
			{
				Paper: model.Paper{DOI: "10.1/a", Title: "Trial A", Year: 2020, CitedByCount: 41},
				Tally: &model.CitationTally{Total: 39, Supporting: 5, Contradicting: 2},
			},
			{
				Paper: model.Paper{DOI: "10.1/b", Title: "Trial B", Year: 2023, CitedByCount: 7},
			},
		},
		Critiques: []model.Critique{
			{DOI: "10.1/a", DesignQuality: "high", Relevance: "direct", ResultDirection: "positive"},
			{DOI: "10.1/b", SampleSizeReported: 60, DesignQuality: "low", Relevance: "direct",
				ResultDirection: "null", Concerns: []string{"no control group", "4-week follow-up", "single center"}},
		},
	}
	notes := map[string]string{"https://doi.org/10.1/A": "LLM note for A"} // non-normalized key
	rows := buildMatrix(st, notes)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	a := rows[0]
	if a.Supporting != 5 || a.Contradicting != 2 || a.TotalCitations != 39 {
		t.Errorf("row A tallies: got %d/%d of %d, want 5/2 of 39", a.Supporting, a.Contradicting, a.TotalCitations)
	}
	if a.Note != "LLM note for A" {
		t.Errorf("row A note: got %q, want the LLM note (DOI keys must normalize)", a.Note)
	}
	if a.Direction != "positive" {
		t.Errorf("row A direction: got %q, want positive", a.Direction)
	}
	b := rows[1]
	if b.Supporting != 0 || b.TotalCitations != 7 {
		t.Errorf("row B: no tally — want 0 support and OpenAlex cited_by 7, got %d support / %d total", b.Supporting, b.TotalCitations)
	}
	if b.SampleSize != 60 {
		t.Errorf("row B sample size: got %d, want 60 from the critique", b.SampleSize)
	}
	if b.Note != "no control group; 4-week follow-up" {
		t.Errorf("row B note fallback (first 2 concerns): got %q", b.Note)
	}
}

func TestCritiquesFor(t *testing.T) {
	// Index-aligned (the normal case): empty DOIs must not collide.
	st := &State{
		Enriched: []model.EnrichedPaper{
			{Paper: model.Paper{Title: "No DOI one"}},
			{Paper: model.Paper{Title: "No DOI two"}},
		},
		Critiques: []model.Critique{
			{Title: "No DOI one", ResultDirection: "positive"},
			{Title: "No DOI two", ResultDirection: "negative"},
		},
	}
	crits := critiquesFor(st)
	if crits[0].ResultDirection != "positive" || crits[1].ResultDirection != "negative" {
		t.Fatalf("index alignment broken for DOI-less papers: %+v", crits)
	}

	// Mismatched lengths (hand-edited artifact): DOI fallback, empty keys ignored.
	st2 := &State{
		Enriched: []model.EnrichedPaper{
			{Paper: model.Paper{DOI: "10.1/x"}},
			{Paper: model.Paper{DOI: "10.1/y"}},
		},
		Critiques: []model.Critique{
			{DOI: "", ResultDirection: "mixed"},
			{DOI: "10.1/y", ResultDirection: "null"},
		},
	}
	crits2 := critiquesFor(st2)
	if crits2[1].ResultDirection != "null" {
		t.Errorf("DOI fallback: got %+v, want y -> null", crits2)
	}
	if hasCritique(crits2[0]) {
		t.Errorf("empty-DOI critique must not attach to paper x: %+v", crits2[0])
	}
}

func TestRenderMarkdownEscapesPipes(t *testing.T) {
	st := &State{
		Report: &model.Report{
			Verdict: "weak",
			Matrix: []model.MatrixRow{
				{DOI: "10.1/p", Title: "Pipe study", Design: "RCT|pilot", Note: "concerns: a | b"},
			},
		},
	}
	out := RenderMarkdown(st)
	if !strings.Contains(out, "RCT\\|pilot") || !strings.Contains(out, "a \\| b") {
		t.Fatalf("pipes must be escaped in table cells:\n%s", out)
	}
	if !strings.Contains(out, "| Direction |") {
		t.Fatalf("Direction column missing:\n%s", out)
	}
}
