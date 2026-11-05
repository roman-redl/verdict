package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roman-redl/verdict/internal/openalex"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// resolveCmd — titles → DOIs via OpenAlex title search: the fallback for
// pastes that carry no DOIs (mode C without the clipboard luck). Lines that
// already contain DOIs pass through; every match prints its own title so the
// result is verifiable at a glance.
func (c *cli) resolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve [file]",
		Short: "Resolve paper titles to DOIs via OpenAlex (for DOI-less pastes)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := readInput(cmd, args)
			if err != nil {
				return err
			}

			finder := openalex.New(c.cfg.OpenAlexEmail)
			resolved := 0

			for _, r := range pipeline.ResolveTitles(cmd.Context(), finder, string(input)) {
				printResolution(r)

				if r.Kind == pipeline.ResolvedExact || r.Kind == pipeline.ResolvedApproximate {
					resolved++
				}
			}

			c.log.Info("titles resolved; UNRESOLVED lines need a manual lookup", "resolved", resolved)

			return nil
		},
	}
}

// printResolution prints one line of the result: passthrough lines as they
// are, matches as tab-separated fields, failures marked UNRESOLVED.
func printResolution(r pipeline.TitleResolution) {
	switch r.Kind {
	case pipeline.ResolvedPassthrough:
		fmt.Println(r.Line) // already has a DOI
	case pipeline.ResolvedExact, pipeline.ResolvedApproximate:
		fmt.Printf("%s\t%d\t%s\t%s\n", r.DOI, r.Year, r.Kind, r.Title)
	case pipeline.Unresolved:
		if r.Err != nil {
			fmt.Printf("UNRESOLVED (%v): %s\n", r.Err, r.Line)
			return
		}

		fmt.Printf("UNRESOLVED: %s\n", r.Line)
	}
}
