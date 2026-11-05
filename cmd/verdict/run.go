package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roman-redl/verdict/cmd/internal/app"
	"github.com/roman-redl/verdict/internal/db"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// runFlags are the flags of `verdict run`; the engine and the text mode are
// parsed into their types when the command runs.
type runFlags struct {
	opts                 pipeline.Options
	engineFlag, modeFlag string
}

// runCmd is the full pipeline for a research question or a list of DOIs.
func (c *cli) runCmd() *cobra.Command {
	var f runFlags

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Full pipeline for a research question or a list of DOIs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.execute(cmd.Context(), &f)
		},
	}
	addRunFlags(cmd, &f)

	return cmd
}

func (c *cli) execute(ctx context.Context, f *runFlags) error {
	var err error

	if f.opts.Engine, err = pipeline.ParseEngine(f.engineFlag); err != nil {
		return err
	}

	if f.opts.TextMode, err = pipeline.ParseTextMode(f.modeFlag); err != nil {
		return err
	}

	p, err := app.BuildPipeline(c.cfg, c.log, f.opts)
	if err != nil {
		return err
	}

	st, dir, err := p.Run(ctx, f.opts)
	if err != nil {
		return err
	}

	if st.Report == nil {
		fmt.Printf("Stage artifacts: %s\n", dir)
		return nil
	}

	fmt.Printf("Verdict: %s\nReport: %s/report.md\nMatrix: %s/matrix.csv\n", st.Report.Verdict, dir, dir)
	c.recordRun(ctx, dir, st)

	return nil
}

// recordRun indexes the finished run in the SQLite run database (best
// effort — the stage artifacts on disk remain the source of truth).
func (c *cli) recordRun(ctx context.Context, dir string, st *pipeline.State) {
	d, err := db.Open(ctx, runsDBPath())
	if err != nil {
		c.log.Warn("run index not updated: cannot open the database", "dir", dir, "err", err)
		return
	}

	if err := d.RecordRun(ctx, dir, st, c.now()); err != nil {
		c.log.Warn("run index not updated: cannot record the run", "dir", dir, "err", err)
	}

	if err := d.Close(); err != nil {
		c.log.Warn("closing the run index failed", "err", err)
	}
}

func addRunFlags(cmd *cobra.Command, f *runFlags) {
	fl := cmd.Flags()
	opts := &f.opts

	fl.StringVarP(&opts.Question, "question", "q", "",
		"research question, e.g.: does creatine improve cognitive performance")
	fl.StringVar(&opts.DOIFile, "dois", "",
		"file with a list of DOIs (one per line) — the manual-corpus mode")
	fl.StringArrayVar(&opts.Queries, "queries", nil,
		"semantic discovery queries, repeatable (mode B; defaults to -q) — e.g. from `verdict pico`")
	fl.StringVar(&f.engineFlag, "engine", "",
		"discovery engine: consensus | openalex (default: consensus when CONSENSUS_API_KEY is set, else openalex)")
	fl.IntVar(&opts.ConsensusPages, "consensus-pages", 0,
		"max Consensus pages per query; each page is one API use (defaults to CONSENSUS_PAGES, 3)")
	fl.StringVar(&opts.Topic, "topic", "",
		"group the run under runs/<topic>/ (one topic per research problem)")
	fl.StringVar(&opts.RunDir, "run-dir", "",
		"artifact directory (overrides --topic and the default runs/<topic>/<slug>-<time>)")
	fl.BoolVar(&opts.Resume, "resume", false,
		"resume a failed run from the stage artifacts")
	fl.StringVar(&opts.StopAfter, "stop-after", "",
		"stop after a stage: 1..4 or the full name (e.g. 2-enrichment)")
	fl.IntVar(&opts.MaxPapers, "max-papers", 0,
		"how many papers to take from discovery (defaults to MAX_PAPERS from env)")
	fl.IntVar(&opts.Concurrency, "concurrency", 0,
		"enrichment parallelism (defaults to CONCURRENCY from env)")
	fl.IntVar(&opts.LLMConcurrency, "llm-concurrency", 0,
		"LLM stage parallelism (defaults to LLM_CONCURRENCY from env)")
	fl.IntVar(&opts.LLMChunk, "llm-chunk", 0,
		"studies per critique call — the call count, not the payload, dominates the cost "+
			"(0 = LLM_CHUNK from env, 5; 1 = one call per study)")
	fl.StringVar(&opts.LLMCommand, "llm-command", "",
		"claude-cli wrapper for this run (default: the session's CLAUDE_WRAPPER, then LLM_COMMAND, then claude-max)")
	fl.StringVar(&opts.LLMModel, "llm-model", "",
		"model for every LLM stage: a name or an alias (opus | sonnet | haiku | fable); empty = each stage's default")
	fl.StringVar(&opts.ModelCritique, "model-critique", "",
		"critique-stage model override (default sonnet)")
	fl.StringVar(&opts.ModelDeep, "model-deep", "",
		"model override for the full-text re-critique of the hybrid pass (default opus)")
	fl.StringVar(&opts.ModelSynthesis, "model-synthesis", "",
		"synthesis-stage model override (default opus, fable with --deep)")
	fl.BoolVar(&opts.Deep, "deep", false,
		"the strongest model (fable) for the synthesis unless --model-synthesis says otherwise")
	fl.StringVar(&opts.ChallengeNote, "challenge-note", "",
		"counterargument to weigh and answer in the verdict rationale (not data)")
	fl.StringVar(&f.modeFlag, "text-mode", "",
		"what the critic reads: lean (abstracts only) | hybrid (abstracts, then full texts for the pivotal studies — "+
			"default) | full (full texts for all) (defaults to TEXT_MODE from env)")
}
