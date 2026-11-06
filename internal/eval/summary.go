package eval

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	dash = "-"
	// claimCells is the claim length in the markdown table; the CSV keeps it whole.
	claimCells = 80
)

// Totals are the aggregate figures of a summary.
type Totals struct {
	Runs int
	// Failed counts runs without a verdict (an error); they are outside
	// every denominator below.
	Failed  int
	Scored  int
	Matches int

	// StrengthScored counts scored runs whose question has an expert certainty.
	StrengthScored  int
	StrengthExact   int
	StrengthWithin1 int

	// Confusion counts scored runs by expected outcome, then by verdict outcome.
	Confusion map[Outcome]map[Outcome]int

	// Stable is the number of questions whose verdict outcome was the same in
	// every repeat (a failed repeat breaks it); meaningful when Repeat > 1.
	Stable int

	Calls        int
	InputTokens  int
	OutputTokens int
}

// Totals aggregates the results.
func (s *Summary) Totals() Totals {
	t := Totals{Runs: len(s.Results), Confusion: map[Outcome]map[Outcome]int{}}

	for _, o := range outcomes {
		t.Confusion[o] = map[Outcome]int{}
	}

	for i := range s.Results {
		r := &s.Results[i]
		if u := r.Usage; u != nil {
			t.Calls += u.Calls
			t.InputTokens += u.InputTokens
			t.OutputTokens += u.OutputTokens
		}

		if !r.Scored {
			t.Failed++
			continue
		}

		t.Scored++
		t.Confusion[r.Expected][r.Got]++

		if r.Match() {
			t.Matches++
		}

		if r.Strength.Scored {
			t.StrengthScored++
			t.StrengthExact += btoi(r.Strength.Exact)
			t.StrengthWithin1 += btoi(r.Strength.Within1)
		}
	}

	t.Stable = s.stableQuestions()

	return t
}

func btoi(b bool) int {
	if b {
		return 1
	}

	return 0
}

// stableQuestions counts the questions that have all Repeat runs scored and the
// same verdict outcome in each.
func (s *Summary) stableQuestions() int {
	type tracker struct {
		runs int
		got  Outcome
		same bool
	}

	byID := map[int]*tracker{}

	var order []int

	for i := range s.Results {
		r := &s.Results[i]

		tr, ok := byID[r.ID]
		if !ok {
			tr = &tracker{got: r.Got, same: true}
			byID[r.ID] = tr
			order = append(order, r.ID)
		}

		tr.runs++
		tr.same = tr.same && r.Scored && r.Got == tr.got
	}

	n := 0

	for _, id := range order {
		if tr := byID[id]; tr.same && tr.runs == s.Repeat {
			n++
		}
	}

	return n
}

// ratio formats "n/d (p%)"; the percentage needs a non-zero denominator.
func ratio(n, d int) string {
	if d == 0 {
		return fmt.Sprintf("%d/%d", n, d)
	}

	return fmt.Sprintf("%d/%d (%.1f%%)", n, d, 100*float64(n)/float64(d))
}

// Line is the one-line outcome summary the command prints.
func (t Totals) Line() string {
	line := fmt.Sprintf("outcome match %s; strength exact %d/%d, within one level %d/%d",
		ratio(t.Matches, t.Scored), t.StrengthExact, t.StrengthScored, t.StrengthWithin1, t.StrengthScored)

	if t.Failed > 0 {
		line += fmt.Sprintf("; %d failed run(s) not counted", t.Failed)
	}

	return line
}

// WriteSummary writes summary.md and summary.csv into dir.
func WriteSummary(dir string, s *Summary) error {
	csvText, err := RenderCSV(s)
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(dir, summaryMD), []byte(RenderMarkdown(s)), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", summaryMD, err)
	}

	if err := os.WriteFile(filepath.Join(dir, summaryCSV), []byte(csvText), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", summaryCSV, err)
	}

	return nil
}

// RenderMarkdown renders summary.md: the header, the totals, the confusion
// table and one row per run.
func RenderMarkdown(s *Summary) string {
	t := s.Totals()

	var b strings.Builder

	b.WriteString("# verdict eval (MedEvidence)\n\n")
	fmt.Fprintf(&b, "- Set: `%s`, %d question(s), %d repeat(s) each\n", s.SetFile, s.Questions, s.Repeat)
	fmt.Fprintf(&b, "- Date: %s\n", s.Date.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "- Text mode: %s\n", orDash(string(s.TextMode)))
	fmt.Fprintf(&b, "- Models: %s\n\n", s.Models)

	b.WriteString("## Totals\n\n")
	fmt.Fprintf(&b, "- Outcome match: %s\n", ratio(t.Matches, t.Scored))
	fmt.Fprintf(&b, "- Strength exact (runs with an expert certainty): %s\n", ratio(t.StrengthExact, t.StrengthScored))
	fmt.Fprintf(&b, "- Strength within one level (runs with an expert certainty): %s\n",
		ratio(t.StrengthWithin1, t.StrengthScored))
	fmt.Fprintf(&b, "- Failed runs (no verdict, outside the figures above): %d of %d\n", t.Failed, t.Runs)

	if s.Repeat > 1 {
		fmt.Fprintf(&b, "- Questions with the same verdict outcome in all %d repeats: %d/%d\n",
			s.Repeat, t.Stable, s.Questions)
	}

	fmt.Fprintf(&b, "- LLM calls: %d, input tokens: %d, output tokens: %d\n\n", t.Calls, t.InputTokens, t.OutputTokens)

	writeConfusion(&b, &t)
	writeRuns(&b, s)

	return b.String()
}

func writeConfusion(b *strings.Builder, t *Totals) {
	b.WriteString("## Expected outcome by verdict outcome\n\n")
	b.WriteString("Rows: the outcome the expert answer means for the claim. Columns: the outcome of verdict's report.\n\n")
	b.WriteString("| Expected \\ Verdict |")

	for _, o := range outcomes {
		fmt.Fprintf(b, " %s |", o)
	}

	b.WriteString("\n|---|")
	b.WriteString(strings.Repeat("---|", len(outcomes)))
	b.WriteString("\n")

	for _, exp := range outcomes {
		fmt.Fprintf(b, "| %s |", exp)

		for _, got := range outcomes {
			fmt.Fprintf(b, " %d |", t.Confusion[exp][got])
		}

		b.WriteString("\n")
	}

	b.WriteString("\n")
}

var runColumns = []string{
	"ID", "Repeat", "Specialty", "Claim", "Expert answer", "Expected", "Verdict outcome", "Match", "Direct tally",
	"Effect direction", "Consensus status", "Certainty", "Verdict level", "Strength exact", "Strength ±1", "Studies given",
	"Studies in report", "LLM calls", "Input tokens", "Output tokens", "Report", "Error",
}

func writeRuns(b *strings.Builder, s *Summary) {
	b.WriteString("## Runs\n\n")
	b.WriteString("| " + strings.Join(runColumns, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat("---|", len(runColumns)) + "\n")

	for i := range s.Results {
		row := resultRow(&s.Results[i], dash)
		row[3] = shorten(row[3], claimCells)

		for j := range row {
			row[j] = mdCell(row[j])
		}

		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
}

// RenderCSV renders summary.csv: one row per run, the claim whole, an empty
// cell where the markdown table shows a dash.
func RenderCSV(s *Summary) (string, error) {
	var b strings.Builder

	w := csv.NewWriter(&b)
	if err := w.Write(runColumns); err != nil {
		return "", fmt.Errorf("writing the CSV header: %w", err)
	}

	for i := range s.Results {
		if err := w.Write(resultRow(&s.Results[i], "")); err != nil {
			return "", fmt.Errorf("writing a CSV row: %w", err)
		}
	}

	w.Flush()

	if err := w.Error(); err != nil {
		return "", fmt.Errorf("writing the CSV: %w", err)
	}

	return b.String(), nil
}

// resultRow lists the cells of a run in the order of runColumns; none is the
// text of an unknown value.
func resultRow(r *Result, none string) []string {
	or := func(s string) string {
		if s == "" {
			return none
		}

		return s
	}
	yn := func(ok bool) string {
		if !r.Scored {
			return none
		}

		return yesNo(ok)
	}
	strength := func(ok bool) string {
		if !r.Strength.Scored {
			return none
		}

		return yesNo(ok)
	}
	num := func(n int, known bool) string {
		if !known {
			return none
		}

		return strconv.Itoa(n)
	}

	u := r.Usage
	if u == nil {
		u = &Usage{}
	}

	return []string{
		strconv.Itoa(r.ID), strconv.Itoa(r.Repeat), r.Specialty, r.Claim, string(r.Answer), string(r.Expected),
		or(string(r.Got)), yn(r.Match()), or(r.Tally),
		or(string(r.Effect)), or(string(r.Consensus)),
		or(string(r.Certainty)), or(string(r.Level)),
		strength(r.Strength.Exact), strength(r.Strength.Within1),
		strconv.Itoa(r.Given), num(r.Reached, r.Scored),
		num(u.Calls, r.Usage != nil), num(u.InputTokens, r.Usage != nil), num(u.OutputTokens, r.Usage != nil),
		or(r.Report), r.Err,
	}
}

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}

	return "no"
}

// mdCell makes text safe for a markdown table cell.
func mdCell(s string) string {
	s = strings.Join(strings.Fields(s), " ")

	return strings.ReplaceAll(s, "|", `\|`)
}

func orDash(s string) string {
	if s == "" {
		return dash
	}

	return s
}

// shorten cuts s to n characters, marking the cut.
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[:n-1]) + "…"
}
