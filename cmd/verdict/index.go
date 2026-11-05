package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roman-redl/verdict/internal/db"
)

// indexCmd — the run index: topics, verdicts and paper counts without
// walking runs/ on disk.
func (c *cli) indexCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "index [topic]",
		Short: "List indexed runs (topics, verdicts, paper counts)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			topic := ""
			if len(args) == 1 {
				topic = args[0]
			}

			d, err := db.Open(cmd.Context(), runsDBPath())
			if err != nil {
				return err
			}

			defer func() {
				if err := d.Close(); err != nil {
					c.log.Warn("closing the run index failed", "err", err)
				}
			}()

			rows, err := d.Runs(cmd.Context(), topic)
			if err != nil {
				return err
			}

			if len(rows) == 0 {
				fmt.Println("no indexed runs")
				return nil
			}

			fmt.Println("topic\trun\tverdict\tconsensus\tpapers\tquestion")

			for _, r := range rows {
				fmt.Printf("%s\t%s\t%s\t%s\t%d\t%s\n",
					orDash(r.Topic), r.Run, orDash(r.Verdict), orDash(r.Consensus), r.Papers, shorten(r.Question, 60))
			}

			return nil
		},
	}
}
