package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roman-redl/verdict/cmd/internal/app"
	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// defaultBriefDir holds the saved briefs unless --out says otherwise.
const defaultBriefDir = "briefs"

// briefCmd — triage a messy problem description: agent-solvable tasks stay
// with the agent, scientific questions are handed to `verdict run`.
func (c *cli) briefCmd() *cobra.Command {
	var outDir string

	cmd := &cobra.Command{
		Use:   "brief [file]",
		Short: "Turn a messy problem description into agent tasks + evidence questions",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			completer, err := app.BuildCompleter(c.cfg, c.log, app.CompleterSpec{Role: llm.RolePlan})
			if err != nil {
				return err
			}

			input, err := readInput(cmd, args)
			if err != nil {
				return err
			}

			b, err := pipeline.BriefFromText(cmd.Context(), completer, string(input))
			if err != nil {
				return err
			}

			if outDir == "" {
				outDir = defaultBriefDir
			}

			path, err := pipeline.SaveBrief(outDir, b, c.now())
			if err != nil {
				return err
			}

			fmt.Printf("Brief: %s\n", path)
			printSuggestions(b)

			return nil
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "", "output directory (defaults to briefs/)")

	return cmd
}

// printSuggestions prints a ready command for every evidence question.
func printSuggestions(b *pipeline.Brief) {
	if len(b.Questions) == 0 {
		return
	}

	fmt.Println("\nSuggested evidence questions (discovery default = mode B: run -q searches Consensus itself):")

	for _, q := range b.Questions {
		fmt.Printf("  # %s\n  bin/verdict run -q %q\n", orDash(q.Priority), q.Question)
	}

	fmt.Println(`  no CONSENSUS_API_KEY? mode C (free): pick papers in the browser —
    bin/verdict web -q "<question>"; pbpaste | bin/verdict dois > dois.txt &&
    bin/verdict run --dois dois.txt -q "<question>"`)
}
