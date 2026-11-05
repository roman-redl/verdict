package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ergochat/readline"
	"github.com/spf13/cobra"

	"github.com/roman-redl/verdict/cmd/internal/app"
	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// askCmd — Q&A with an analyst holding the run's context: the same LLM
// backend the pipeline used (by default the Claude Code tariff). REPL or a
// single question.
func (c *cli) askCmd() *cobra.Command {
	var runDir, question string

	cmd := &cobra.Command{
		Use:   "ask",
		Short: "Ask an analyst about a finished run (papers, citations, verdict)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.ask(cmd.Context(), runDir, question)
		},
	}
	cmd.Flags().StringVar(&runDir, "run-dir", "",
		"a topic directory (loads all its runs, newest first) or a single run directory")

	_ = cmd.MarkFlagRequired("run-dir") // fails only for an undefined flag; it is defined just above
	cmd.Flags().StringVarP(&question, "question", "q", "", "a single question, no interactive mode")

	return cmd
}

func (c *cli) ask(ctx context.Context, runDir, question string) error {
	completer, err := app.BuildCompleter(c.cfg, c.log, app.CompleterSpec{Role: llm.RoleAsk})
	if err != nil {
		return err
	}

	runs, err := pipeline.LoadRuns(runDir, c.log)
	if err != nil {
		return err
	}

	system := pipeline.BuildAskSystem(runs)

	fmt.Println("Loaded runs:")

	for _, r := range runs {
		fmt.Printf("  - %s (%d papers) — %s\n", r.Name, r.State.PaperCount(), shorten(r.State.Question, 80))
	}

	if question == "" {
		return c.dialogue(ctx, pipeline.NewDialogue(completer, system))
	}

	answer, usage, err := pipeline.AskOnce(ctx, completer, system, question)
	if err != nil {
		return err
	}

	c.log.Info("ask llm usage", "in", usage.InputTokens, "out", usage.OutputTokens)
	fmt.Println(renderAnswer(answer))

	return nil
}

// dialogue is the interactive loop: one question per line until exit / Ctrl-D.
func (c *cli) dialogue(ctx context.Context, d *pipeline.Dialogue) error {
	fmt.Println("Ask anything about the runs. exit / Ctrl-D to quit; empty Enter just re-prompts.")

	rl, err := readline.NewEx(&readline.Config{
		Prompt:      "❯ ",
		HistoryFile: filepath.Join(os.TempDir(), "verdict-ask-history"),
	})
	if err != nil {
		return fmt.Errorf("starting readline: %w", err)
	}

	defer func() { _ = rl.Close() }() // quitting anyway: a terminal restore failure changes nothing

	for {
		line, err := rl.Readline()
		if err != nil {
			// Ctrl-D (io.EOF) and Ctrl-C end the dialogue; anything else is a reader failure
			if !errors.Is(err, io.EOF) && !errors.Is(err, readline.ErrInterrupt) {
				c.log.Warn("reading the question failed, ending the dialogue", "err", err)
			}

			return nil
		}

		q := strings.TrimSpace(line)
		if q == "" {
			continue
		}

		if q == "exit" || q == "quit" {
			return nil
		}

		fmt.Println("…thinking (up to a couple of minutes)…")

		answer, usage, err := d.Ask(ctx, q)
		if err != nil {
			c.log.Warn("the question failed — please repeat it", "err", err)
			continue
		}

		c.log.Info("ask llm usage", "in", usage.InputTokens, "out", usage.OutputTokens)
		fmt.Println("\n" + renderAnswer(answer) + "\n")
	}
}
