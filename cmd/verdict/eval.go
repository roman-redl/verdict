package main

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/roman-redl/verdict/cmd/internal/app"
	"github.com/roman-redl/verdict/internal/eval"
	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ eval.Runner = (*pipeline.Pipeline)(nil)

// evalFlags are the flags of `verdict eval`; opts carries the pass-through
// LLM flags, the text mode is parsed when the command runs.
type evalFlags struct {
	set      string
	ids      []int
	repeat   int
	out      string
	modeFlag string
	opts     pipeline.Options
}

// evalCmd measures verdict against the expert conclusions of Cochrane reviews.
func (c *cli) evalCmd() *cobra.Command {
	var f evalFlags

	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Score verdict's outcomes against the expert conclusions of the MedEvidence benchmark",
		Long: "Runs every selected benchmark question through the normal pipeline on exactly the studies the " +
			"Cochrane review used (a DOI list, no search) and compares the outcome with the expert answer.\n" +
			"Every question costs a full run of LLM calls. After a usage limit, rerun the same command with " +
			"the same --out: finished runs are loaded, the rest resume.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.evaluate(cmd.Context(), &f)
		},
	}
	fl := cmd.Flags()

	fl.StringVar(&f.set, "set", "evals/medevidence.yaml", "the benchmark question set")
	fl.IntSliceVar(&f.ids, "ids", nil, "comma-separated question ids to run (default: all)")
	fl.IntVar(&f.repeat, "repeat", 1, "how many times to run each question")
	fl.StringVar(&f.out, "out", "", "output directory (default: runs/eval-<YYYYMMDD-HHMMSS>); reuse it to resume")
	fl.StringVar(&f.modeFlag, "text-mode", string(pipeline.TextHybrid),
		"what the critic reads: lean (abstracts only) | hybrid | full")
	addLLMFlags(cmd, &f.opts)

	return cmd
}

func (c *cli) evaluate(ctx context.Context, f *evalFlags) error {
	mode, err := pipeline.ParseTextMode(f.modeFlag)
	if err != nil {
		return err
	}

	f.opts.TextMode = cmp.Or(mode, pipeline.TextHybrid)

	qs, err := eval.LoadSet(f.set)
	if err != nil {
		return err
	}

	qs, err = eval.Select(qs, f.ids)
	if err != nil {
		return err
	}

	p, err := app.BuildPipeline(c.cfg, c.log, f.opts)
	if err != nil {
		return err
	}

	ev, err := eval.New(indexingRunner{inner: p, c: c}, c.log, c.now)
	if err != nil {
		return err
	}

	out := cmp.Or(f.out, filepath.Join("runs", "eval-"+c.now().Format("20060102-150405")))

	sum, err := ev.Run(ctx, qs, eval.Params{SetFile: f.set, Out: out, Repeat: f.repeat, Options: f.opts})
	if sum != nil {
		fmt.Printf("Summary: %s\n%s\n", filepath.Join(out, "summary.md"), sum.Totals().Line())
	}

	return err
}

// indexingRunner records every run the eval executes in the run index, as
// `verdict run` does. The index topic of an eval run is its "<id>-<repeat>"
// directory name.
type indexingRunner struct {
	inner eval.Runner
	c     *cli
}

func (r indexingRunner) Run(ctx context.Context, opts pipeline.Options) (*pipeline.State, string, error) {
	st, dir, err := r.inner.Run(ctx, opts)
	if err != nil {
		return nil, dir, err
	}

	if st.Report != nil {
		r.c.recordRun(ctx, dir, st)
	}

	return st, dir, nil
}
