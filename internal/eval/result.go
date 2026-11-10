package eval

import (
	"time"

	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// Usage is the LLM spend of one run.
type Usage struct {
	Calls        int
	InputTokens  int
	OutputTokens int
}

// usageOf adds up the screening, critique and synthesis stages; nil when the
// backend reported no usage at all.
func usageOf(c *pipeline.CostReport) *Usage {
	if c == nil || (c.Screening == nil && c.Critique == nil && c.Synthesis == nil) {
		return nil
	}

	var u Usage

	for _, s := range []*pipeline.StageCost{c.Screening, c.Critique, c.Synthesis} {
		if s == nil {
			continue
		}

		u.Calls += s.Calls
		u.InputTokens += s.InputTokens
		u.OutputTokens += s.OutputTokens
	}

	return &u
}

// Result is one run of one question. A run that failed carries Err and
// nothing is scored.
type Result struct {
	ID        int
	Repeat    int
	Specialty string
	Claim     string
	Answer    Answer
	Expected  Outcome
	Certainty Certainty
	// Given is the number of studies handed to the pipeline.
	Given int

	// Scored is true when the run produced a report; the fields below are
	// set only then.
	Scored    bool
	Got       Outcome
	Tally     string
	Consensus ConsensusStatus // the report's own statement about the direction of the evidence
	// Effect is the report's effect direction; empty for an old report.
	Effect   model.EffectDirection
	Level    Level
	Strength Strength
	// Reached is the number of studies that reached the report's matrix.
	Reached int
	Usage   *Usage
	// Report is the path of report.md relative to the eval's output directory.
	Report string

	Err string
}

// Match reports whether verdict's outcome equals the expert's.
func (r *Result) Match() bool { return r.Scored && r.Got == r.Expected }

func newResult(q *Question, n int) Result {
	return Result{
		ID: q.ID, Repeat: n, Specialty: q.Specialty, Claim: q.Claim, Answer: q.Answer,
		Expected: q.Expected(), Certainty: q.Certainty, Given: len(q.DOIs()),
	}
}

// score fills the result from the final state of a run.
func (r *Result) score(q *Question, st *pipeline.State) error {
	j, err := Judge(st.Report)
	if err != nil {
		return err
	}

	r.Scored = true
	r.Got = j.Outcome
	r.Tally = j.Tally
	r.Consensus = j.Consensus
	r.Effect = j.Effect
	r.Level = j.Level
	r.Strength = ScoreStrength(q.Certainty, j.Level)
	r.Reached = len(st.Report.Matrix)
	r.Usage = usageOf(st.Cost)

	return nil
}

// Summary is everything the summary files show: the header facts and the
// results of the finished runs.
type Summary struct {
	SetFile   string
	Date      time.Time
	TextMode  pipeline.TextMode
	Models    string
	Repeat    int
	Questions int
	Results   []Result
}
