package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roman-redl/verdict/cmd/internal/app"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// picoCmd — question normalization before discovery: a messy question
// becomes PICO components plus ready-to-run queries — a PubMed-style
// keyword query (mode A) and 1-3 semantic queries (mode B/C: run through
// the Consensus API via `run --queries`, or the free web UI).
func (c *cli) picoCmd() *cobra.Command {
	var question string

	cmd := &cobra.Command{
		Use:   "pico",
		Short: "Normalize a question into PICO + ready keyword/semantic queries",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if question == "" {
				return errors.New("--question/-q is required")
			}

			completer, err := app.BuildCompleter(c.cfg, c.log, "")
			if err != nil {
				return err
			}

			p, err := pipeline.PICOFromQuestion(cmd.Context(), completer, question)
			if err != nil {
				return err
			}

			fmt.Println(pipeline.RenderPICO(p))

			return nil
		},
	}
	cmd.Flags().StringVarP(&question, "question", "q", "", "the research question (any form)")
	_ = cmd.MarkFlagRequired("question") // fails only for an undefined flag; it is defined just above

	return cmd
}
