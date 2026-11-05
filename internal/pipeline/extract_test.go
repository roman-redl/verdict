package pipeline

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ExtractDOIs must survive any junk around the DOIs: URLs, tables,
// bibliographies, mixed case and trailing punctuation.
func TestExtractDOIs(t *testing.T) {
	text := `
1. Gordon et al. https://doi.org/10.1038/nature12373.
2. <doi:10.1126/SCIENCE.abc123> — contradicts
3. see also 10.1016/j.cell.2024.01.015,
some plain text without a DOI, and a duplicate 10.1038/NATURE12373
`
	want := []string{
		"10.1038/nature12373",
		"10.1126/science.abc123",
		"10.1016/j.cell.2024.01.015",
	}

	require.Equal(t, want, ExtractDOIs(text))
}

func TestExtractDOIsEdgeCases(t *testing.T) {
	require.Empty(t, ExtractDOIs(""))
	require.Empty(t, ExtractDOIs("no identifiers, only 10.12/short prefixes"))
	require.Equal(t, []string{"10.1000/abc"}, ExtractDOIs("(see 10.1000/abc)."),
		"trailing punctuation from a copy is trimmed")
}

// Whatever a clipboard dump looks like, the result is a clean, duplicate-free
// list of normalized DOIs.
func FuzzExtractDOIs(f *testing.F) {
	for _, seed := range []string{
		"", "10.1038/nature12373", "https://doi.org/10.1000/ABC.1, (10.1000/xyz-2); 10.1000/abc.1",
		"<doi:10.1126/SCIENCE.abc123>", "10.1000/", "10.99999999999/x", "\x00\xff10.1000/é",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		seen := map[string]bool{}

		for _, d := range ExtractDOIs(text) {
			if d == "" || !strings.HasPrefix(d, "10.") || d != strings.ToLower(d) {
				t.Fatalf("not a normalized DOI: %q", d)
			}

			if seen[d] {
				t.Fatalf("duplicate DOI: %q", d)
			}

			seen[d] = true
		}
	})
}
