package main

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

// consensusURL is the Consensus web UI the free bridge opens.
const consensusURL = "https://consensus.app/"

// webCmd — discovery mode C (the free bridge): open the Consensus web UI
// with the question ready to paste, pick papers by hand (scroll to the end
// of the list), copy them and feed the pipeline via dois/resolve. The
// semantic path with an API key is mode B (`run -q` does it automatically).
func webCmd() *cobra.Command {
	var question string

	cmd := &cobra.Command{
		Use: "web",
		Short: "Open the Consensus web UI with the question ready to paste " +
			"(free manual selection — the semantic path without an API key)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if question == "" {
				return errors.New("--question/-q is required")
			}

			fmt.Println("Run this question in Consensus (consensus.app):")
			fmt.Println()
			fmt.Println("  " + question)
			fmt.Println()

			opener := "xdg-open"
			if runtime.GOOS == "darwin" {
				opener = "open"
			}

			if err := exec.CommandContext(cmd.Context(), opener, consensusURL).Start(); err != nil {
				fmt.Println("(the browser did not open — open consensus.app yourself)")
			}

			printNextSteps(question)

			return nil
		},
	}
	cmd.Flags().StringVarP(&question, "question", "q", "", "research question")

	return cmd
}

func printNextSteps(question string) {
	fmt.Println("Next steps:")
	fmt.Println("  1) scroll the results to the end (\"Load more\" caps at ~50), then")
	fmt.Println("     select and copy the whole list — raw paste back is fine")
	fmt.Println("  2) pbpaste | bin/verdict dois > dois.txt        (DOIs present)")
	fmt.Println("     pbpaste | bin/verdict resolve > dois.txt    (titles only)")
	fmt.Println("  3) bin/verdict run --dois dois.txt -q \"" + question + "\"")
}
