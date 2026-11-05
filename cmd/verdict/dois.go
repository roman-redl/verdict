package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roman-redl/verdict/internal/pipeline"
)

// doisCmd — clean arbitrary copied text down to a list of DOIs.
func doisCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dois [file]",
		Short: "Extract DOIs from arbitrary text (clipboard dump, links, a table) into a clean list",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := readInput(cmd, args)
			if err != nil {
				return err
			}

			dois := pipeline.ExtractDOIs(string(input))
			if len(dois) == 0 {
				return errors.New("no DOIs found")
			}

			for _, d := range dois {
				fmt.Println(d)
			}

			return nil
		},
	}
}
