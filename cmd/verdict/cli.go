package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/roman-redl/verdict/internal/config"
)

// cli carries what the commands share: the configuration (parsed once in
// main), the logger for diagnostics (stderr) and the clock.
type cli struct {
	cfg *config.Config
	log *slog.Logger
	now func() time.Time
}

// runsDBPath is the SQLite run index, next to the run directories.
func runsDBPath() string { return filepath.Join("runs", "verdict.db") }

func (c *cli) root() *cobra.Command {
	root := &cobra.Command{
		Use:   "verdict",
		Short: "Evidence assessment for scientific claims: discovery → Scite/OpenAlex → LLM",
	}
	root.AddCommand(c.runCmd(), webCmd(), doisCmd(), c.resolveCmd(), c.snowballCmd(),
		c.addpdfCmd(), c.askCmd(), c.briefCmd(), c.picoCmd(), c.indexCmd(), c.evalCmd())

	return root
}

// readInput reads the command input: a file path, "-" or no argument =
// stdin (the shared convention of dois/resolve/snowball/brief).
func readInput(cmd *cobra.Command, args []string) ([]byte, error) {
	if len(args) == 1 && args[0] != "-" {
		b, err := os.ReadFile(args[0])
		if err != nil {
			return nil, fmt.Errorf("reading the input file: %w", err)
		}

		return b, nil
	}

	b, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return nil, fmt.Errorf("reading the standard input: %w", err)
	}

	return b, nil
}

// shorten cuts s to n characters, marking the cut.
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[:n-1]) + "…"
}

// orDash prints a dash for an empty value.
func orDash(s string) string {
	if s == "" {
		return "—"
	}

	return s
}
