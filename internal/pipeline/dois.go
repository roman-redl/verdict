package pipeline

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

var doiRe = regexp.MustCompile(`10\.\d{4,9}/[^\s"'<>]+`)

// ExtractDOIs pulls DOIs out of arbitrary text: a list of links, a table
// copied from a web UI, a bibliography — anything. Order is preserved,
// duplicates are removed.
func ExtractDOIs(text string) []string {
	seen := map[string]bool{}

	var out []string

	for _, m := range doiRe.FindAllString(text, -1) {
		m = strings.TrimRight(m, ".,;:)]}") // trailing punctuation picked up when copying

		d := model.NormalizeDOI(m)
		if d != "" && !seen[d] {
			seen[d] = true

			out = append(out, d)
		}
	}

	return out
}

func readDOIFile(path string) ([]string, error) {
	//nolint:gosec // the path comes from the --dois flag of the local CLI (the user's own file)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	return ExtractDOIs(string(b)), nil
}
