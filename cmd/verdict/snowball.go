package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roman-redl/verdict/internal/openalex"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// snowballCmd — citation-chaining corpus expansion: finds works connected to
// several seed papers at once (cited by them or citing them). Prints
// candidates for curation; nothing is added to any corpus automatically.
func (c *cli) snowballCmd() *cobra.Command {
	var minShared, maxOut int

	cmd := &cobra.Command{
		Use:   "snowball [file]",
		Short: "Expand a DOI list via citation chaining (co-citation candidates for curation)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := readInput(cmd, args)
			if err != nil {
				return err
			}

			dois := pipeline.ExtractDOIs(string(input))
			if len(dois) < 2 {
				return fmt.Errorf("snowball needs at least 2 seed DOIs, got %d", len(dois))
			}

			works := openalex.New(c.cfg.OpenAlexEmail)

			cands, err := pipeline.Snowball(cmd.Context(), works, dois, minShared, maxOut, c.log)
			if err != nil {
				return err
			}

			if len(cands) == 0 {
				fmt.Println("no candidates shared by several seeds — the corpus looks complete")
				return nil
			}

			fmt.Println("doi\tyear\tcited-by-seeds\tcites-seeds\ttitle")

			for _, cand := range cands {
				fmt.Printf("%s\t%d\t%d\t%d\t%s\n", cand.DOI, cand.Year, cand.CitedBySeeds, cand.CitesSeeds, cand.Title)
			}

			c.log.Info("curate manually: shared-citation counts are a signal, not a relevance verdict")

			return nil
		},
	}
	cmd.Flags().IntVar(&minShared, "min-shared", 2, "how many seeds a candidate must connect to")
	cmd.Flags().IntVar(&maxOut, "max", 10, "how many candidates to print")

	return cmd
}
