package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/roman-redl/verdict/internal/pdf"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// addpdfCmd — drop a paper PDF (e.g. straight from ~/Downloads) into a
// run's fulltext/ slot: the DOI is read from the PDF itself, so no manual
// renaming. The next run over that directory gets the full text.
func (c *cli) addpdfCmd() *cobra.Command {
	var runDir string

	extractor := pdf.NewExtractor(c.log)

	cmd := &cobra.Command{
		Use:   "addpdf <file.pdf> [more.pdf...]",
		Short: "Drop paper PDF(s) into a run (the DOI is read from the PDF itself)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			for _, f := range args {
				//nolint:gosec // the path is an argument of the local CLI: the user names the file
				data, err := os.ReadFile(f)
				if err != nil {
					return fmt.Errorf("reading %s: %w", f, err)
				}

				added, err := pipeline.AddPDF(extractor, runDir, data)
				if err != nil {
					return fmt.Errorf("%s: %w", f, err)
				}

				fmt.Printf("%s -> %s (%s)\n", f, added.Path, added.DOI)
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&runDir, "run-dir", "", "the run directory (runs/<topic>/<run>)")
	_ = cmd.MarkFlagRequired("run-dir") // fails only for an undefined flag; it is defined just above

	return cmd
}
