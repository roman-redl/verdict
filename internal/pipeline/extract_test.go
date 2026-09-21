package pipeline

import (
	"reflect"
	"testing"
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
	if got := ExtractDOIs(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
