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
	search   bool
	opts     pipeline.Options
}

// evalCmd measures verdict against the expert conclusions of Cochrane reviews.
func (c *cli) evalCmd() *cobra.Command {
	var f evalFlags

	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Score verdict's outcomes against the expert conclusions of the MedEvidence benchmark",
		Long: "Runs every selected benchmark question through the normal pipeline on exactly the studies the " +
			"Cochrane review used (a DOI list, no search; see --search for the other mode) and compares the " +
			"outcome with the expert answer.\n" +
			"Every question costs a full run of LLM calls. After a usage limit, rerun the same command with " +
			"the same --out: finished runs are loaded, the rest resume.\n\n" +
			"With --search the studies are not given: every question goes through the normal semantic " +
			"discovery (mode B) as a user's run would. The two queries are the benchmark question and the " +
			"claim; the source Cochrane review (review_doi) is excluded automatically, so mining its " +
			"reference list cannot hand the answer back. Consensus, review reference mining, PubMed (auto), " +
			"screening and the MAX_PAPERS selection run as in `verdict run`. The summary then adds the search " +
			"recall of the review's studies: found in the pool, kept by the screening, in the corpus of the " +
			"critic. It needs CONSENSUS_API_KEY and spends Consensus calls (2 queries x 1 page per question) " +
			"on top of the screening, critique and synthesis LLM calls. A --out directory made in one mode " +
			"cannot be reused in the other.",
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
	fl.BoolVar(&f.search, "search", false,
		"find the studies by the normal semantic discovery instead of giving the review's DOI list; "+
			"the source review is excluded, the summary adds the search recall (spends Consensus calls)")
	addLLMFlags(cmd, &f.opts)

	return cmd
}

func (c *cli) evaluate(ctx context.Context, f *evalFlags) error {
	mode, err := pipeline.ParseTextMode(f.modeFlag)
	if err != nil {
		return err
	}

	evalMode := eval.ModeDOIs

	if f.search {
		evalMode = eval.ModeSearch

		if c.cfg.ConsensusAPIKey == "" {
			return &pipeline.UserError{Msg: "--search needs semantic discovery: set CONSENSUS_API_KEY"}
		}
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

	sum, err := ev.Run(ctx, qs, eval.Params{
		SetFile: f.set, Out: out, Repeat: f.repeat, Mode: evalMode, Options: f.opts,
	})
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
