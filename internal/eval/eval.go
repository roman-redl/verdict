package eval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/roman-redl/verdict/internal/pipeline"
)

// Runner is the part of the pipeline the eval needs: one full run. A
// *pipeline.Pipeline satisfies it.
type Runner interface {
	Run(ctx context.Context, opts pipeline.Options) (*pipeline.State, string, error)
}

// Params are the parameters of one eval.
type Params struct {
	// SetFile is the path of the input set, shown in the summary.
	SetFile string
	// Out is the directory of the eval: <Out>/<id>-<n>/ per run, plus the summary.
	Out string
	// Repeat is how many times every question runs (>= 1).
	Repeat int
	// Mode is how the studies reach the pipeline; empty means ModeDOIs.
	Mode Mode
	// Options carries the pass-through settings of the pipeline (text mode,
	// models, wrapper); the per-run fields are overwritten.
	Options pipeline.Options
}

// Evaluator runs the questions one at a time through a Runner and scores the
// results.
type Evaluator struct {
	runner Runner
	log    *slog.Logger
	now    func() time.Time
}

// New builds an evaluator; now stamps the summary.
func New(runner Runner, log *slog.Logger, now func() time.Time) (*Evaluator, error) {
	if runner == nil {
		return nil, errors.New("the eval needs a runner")
	}

	if log == nil {
		return nil, errors.New("the eval needs a logger")
	}

	if now == nil {
		return nil, errors.New("the eval needs a clock")
	}

	return &Evaluator{runner: runner, log: log, now: now}, nil
}

const (
	summaryMD  = "summary.md"
	summaryCSV = "summary.csv"
	// synthesisArtifact marks a finished run: the last stage's checkpoint.
	synthesisArtifact = "4-synthesis.json"
)

// Run executes every question Repeat times, sequentially, and writes
// summary.md and summary.csv into Out from all the results. A run that
// finished earlier (its synthesis artifact is on disk) is loaded, not repeated.
// A usage limit (or a canceled context) ends the eval: the summary still covers
// the finished runs, and the returned error says to rerun the same command.
// Any other error of a run is recorded in its result and the eval goes on.
// The summary is returned even together with an error — except for a failed
// preflight (a run directory made in the other mode, a question the mode
// cannot use): nothing ran and the files of the earlier eval stay as they are.
func (e *Evaluator) Run(ctx context.Context, qs []Question, p Params) (*Summary, error) {
	if p.Repeat < 1 {
		return nil, fmt.Errorf("repeat must be at least 1, got %d", p.Repeat)
	}

	if p.Out == "" {
		return nil, errors.New("the output directory is not set")
	}

	if err := preflight(qs, p); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(p.Out, 0o750); err != nil {
		return nil, fmt.Errorf("creating the output directory: %w", err)
	}

	sum := &Summary{
		SetFile: p.SetFile, Date: e.now(), Mode: p.mode(), TextMode: p.Options.TextMode,
		Models: modelFlags(&p.Options), Repeat: p.Repeat, Questions: len(qs),
	}
	stopErr := e.runAll(ctx, qs, p, sum)

	if err := WriteSummary(p.Out, sum); err != nil {
		if stopErr == nil {
			return sum, err
		}

		return sum, fmt.Errorf("%w (and the summary was not written: %w)", stopErr, err)
	}

	return sum, stopErr
}

func (e *Evaluator) runAll(ctx context.Context, qs []Question, p Params, sum *Summary) error {
	for i := range qs {
		for n := 1; n <= p.Repeat; n++ {
			res, err := e.runOne(ctx, &qs[i], n, p)
			if err != nil {
				return fmt.Errorf("stopped at question %d, repeat %d: rerun the same command with the same "+
					"--out (%s) to continue, after the limit resets if it was a usage limit: %w",
					qs[i].ID, n, p.Out, err)
			}

			sum.Results = append(sum.Results, res)
		}
	}

	return nil
}

// runOne produces the result of one question run. It returns an error only for
// the failures that must stop the whole eval; any other failure is recorded in
// the result.
func (e *Evaluator) runOne(ctx context.Context, q *Question, n int, p Params) (Result, error) {
	res := newResult(q, n)

	st, err := e.obtain(ctx, q, n, p)
	if err != nil {
		if errors.Is(err, pipeline.ErrLLMStop) || ctx.Err() != nil {
			return res, err
		}

		e.log.Error("eval run failed", "id", q.ID, "repeat", n, "err", err)
		res.Err = oneLine(err)

		return res, nil
	}

	if err := res.score(q, st); err != nil {
		e.log.Error("eval run not scored", "id", q.ID, "repeat", n, "err", err)
		res.Err = oneLine(err)

		return res, nil
	}

	if p.mode() == ModeSearch {
		rec := recallOf(q, st)
		res.Recall = &rec
	}

	res.Report = path.Join(runName(q.ID, n), "run", "report.md")
	e.log.Info("eval run scored", "id", q.ID, "repeat", n, "expected", res.Expected, "got", res.Got)

	return res, nil
}

func runName(id, n int) string { return fmt.Sprintf("%d-%d", id, n) }

// obtain returns the final state of a run: loaded when the run directory
// already holds the synthesis artifact, otherwise produced by the Runner
// (resuming a started run directory).
func (e *Evaluator) obtain(ctx context.Context, q *Question, n int, p Params) (*pipeline.State, error) {
	base := filepath.Join(p.Out, runName(q.ID, n))
	runDir := filepath.Join(base, "run")

	if fileExists(filepath.Join(runDir, synthesisArtifact)) {
		st, err := pipeline.LoadState(runDir)
		if err != nil {
			return nil, fmt.Errorf("loading the finished run: %w", err)
		}

		e.log.Info("eval run reused: synthesis artifact on disk", "id", q.ID, "repeat", n)

		return st, nil
	}

	if err := os.MkdirAll(base, 0o750); err != nil {
		return nil, fmt.Errorf("creating the run directory: %w", err)
	}

	opts, err := buildOptions(q, n, p)
	if err != nil {
		return nil, err
	}

	e.log.Info("eval run starting", "id", q.ID, "repeat", n, "mode", p.mode(), "studies", len(q.DOIs()),
		"resume", opts.Resume)

	st, _, err := e.runner.Run(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("running the pipeline: %w", err)
	}

	return st, nil
}

// buildOptions makes the pipeline options of one run and records the mode in
// the run directory. Both modes research the claim; the DOI-list mode hands
// the pipeline the review's studies, the search mode the two queries and the
// source review to exclude, and leaves the rest to the defaults of `verdict
// run`. The Consensus engine is explicit: without a key the automatic choice
// would fall back to keyword search, which is not the mode being measured.
func buildOptions(q *Question, n int, p Params) (pipeline.Options, error) {
	base := filepath.Join(p.Out, runName(q.ID, n))
	runDir := filepath.Join(base, "run")

	opts := p.Options
	opts.Question = q.Claim
	opts.RunDir = runDir
	opts.Topic = fmt.Sprintf("eval-%d-%d", q.ID, n)
	opts.Resume = fileExists(runDir)

	if err := writeMode(base, p.mode()); err != nil {
		return pipeline.Options{}, err
	}

	if p.mode() == ModeSearch {
		opts.Engine = pipeline.EngineConsensus
		opts.Queries = searchQueries(q)
		opts.ExcludeDOIs = []string{q.ReviewDOI}

		return opts, nil
	}

	opts.DOIFile = filepath.Join(base, doiListFile)
	if err := os.WriteFile(opts.DOIFile, []byte(strings.Join(q.DOIs(), "\n")+"\n"), 0o600); err != nil {
		return pipeline.Options{}, fmt.Errorf("writing the DOI list: %w", err)
	}

	return opts, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// oneLine is the error text for the summary: a table cell cannot hold line breaks.
func oneLine(err error) string {
	//nolint:forbidigo // the text goes into the summary files, an output boundary
	return strings.Join(strings.Fields(err.Error()), " ")
}

// modelFlags describes the pass-through pipeline settings for the summary header.
func modelFlags(o *pipeline.Options) string {
	var parts []string

	add := func(name, val string) {
		if val != "" {
			parts = append(parts, name+"="+val)
		}
	}

	add("llm-command", o.LLMCommand)
	add("llm-model", o.LLMModel)
	add("model-critique", o.ModelCritique)
	add("model-deep", o.ModelDeep)
	add("model-synthesis", o.ModelSynthesis)

	if o.Deep {
		parts = append(parts, "deep")
	}

	if len(parts) == 0 {
		return "defaults"
	}

	return strings.Join(parts, ", ")
}
