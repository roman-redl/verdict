package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	natureDOI = "10.1038/nature12373"
	shortDOI  = "10.1/x"
)

func TestNormalizeDOI(t *testing.T) {
	for in, want := range map[string]string{
		"10.1038/NATURE12373":                  natureDOI,
		"https://doi.org/10.1038/Nature12373":  natureDOI,
		"http://doi.org/10.1038/nature12373":   natureDOI,
		"https://dx.doi.org/10.1038/nature123": "10.1038/nature123",
		"doi.org/10.1/x":                       shortDOI,
		"doi:10.1/x":                           shortDOI,
		"  DOI:10.1/X  ":                       shortDOI,
		"doi: https://doi.org/10.1/x":          shortDOI,
		"doi:doi:10.1/x":                       shortDOI,
		"":                                     "",
	} {
		assert.Equal(t, want, NormalizeDOI(in), in)
	}
}

// Normalizing twice changes nothing, whatever the input.
func FuzzNormalizeDOI(f *testing.F) {
	for _, seed := range []string{"", "10.1/x", "https://doi.org/10.1/X", "doi:doi:10.1/x", "DOI.ORG/10.1/x"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		once := NormalizeDOI(s)
		if twice := NormalizeDOI(once); twice != once {
			t.Fatalf("not idempotent: %q -> %q -> %q", s, once, twice)
		}
	})
}

func TestWorkToPaperFallsBackToTheLookupDOI(t *testing.T) {
	w := &Work{Title: "T", OAURL: "", PDFURL: "https://pdf/x.pdf"}

	p := w.ToPaper("10.1/Fallback")
	assert.Equal(t, "10.1/fallback", p.DOI)
	assert.Equal(t, "https://pdf/x.pdf", p.OALink)
	assert.Equal(t, SourceOpenAlex, p.Source)
}

func TestNoticeIsRetraction(t *testing.T) {
	assert.True(t, Notice{Type: "retraction"}.IsRetraction())
	assert.False(t, Notice{Type: "correction"}.IsRetraction())
}
