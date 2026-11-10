package jats

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	//nolint:gosec // a fixed file name inside testdata
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)

	return b
}

// The converter puts the tables first, then the results, then the methods,
// then the rest; the front matter, the references, the formulas and the
// citation markers are gone, the table keeps its rows and footnotes.
func TestTextGolden(t *testing.T) {
	got, err := Text(readFixture(t, "article.xml"))

	require.NoError(t, err)

	want := strings.TrimSuffix(string(readFixture(t, "article.golden")), "\n")
	assert.Equal(t, want, got)

	for _, noise := range []string{"FRONT-MATTER", "REFERENCE ENTRY", "We thank", "mml", "RR RR"} {
		assert.NotContains(t, got, noise)
	}
}

func TestTextShapes(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want string
	}{
		{
			name: "no body: nothing to read",
			xml:  `<article><front><article-meta/></front></article>`,
			want: "",
		},
		{
			name: "the NCBI envelope around the article",
			xml: `<?xml version="1.0"?><pmc-articleset><article><body><sec><title>Results</title>` +
				`<p>What.</p></sec></body></article></pmc-articleset>`,
			want: "## Results\nWhat.",
		},
		{
			name: "the NCBI envelope with a front matter only (the publisher does not allow the download)",
			xml: `<pmc-articleset><article><!--The publisher of this article does not allow downloading ` +
				`of the full text in XML form.--><front><article-meta><abstract><p>Only this.</p></abstract>` +
				`</article-meta></front></article></pmc-articleset>`,
			want: "",
		},
		{
			name: "findings and outcomes sections count as results",
			xml: `<article><body><sec><title>Background</title><p>Why.</p></sec>` +
				`<sec><title>Findings</title><p>What.</p></sec></body></article>`,
			want: "## Findings\nWhat.\n\n## Background\nWhy.",
		},
		{
			name: "the sec-type decides when the title is a generic one",
			xml: `<article><body><sec><title>Background</title><p>Why.</p></sec>` +
				`<sec sec-type="results"><title>Study</title><p>What.</p></sec></body></article>`,
			want: "## Study\nWhat.\n\n## Background\nWhy.",
		},
		{
			name: "text in the body without sections",
			xml:  `<article><body><p>A letter.</p><p>Second paragraph.</p></body></article>`,
			want: "A letter.\nSecond paragraph.",
		},
		{
			name: "a table without a caption, a spanning cell, an empty row",
			xml: `<article><body><sec><title>Results</title><table-wrap><table><tbody>` +
				`<tr><th colspan="2">Group</th><th>N</th></tr><tr><td/><td/></tr><tr><td>A</td><td>B</td><td>3</td></tr>` +
				`</tbody></table></table-wrap></sec></body></article>`,
			want: "## Tables\nGroup |  | N\nA | B | 3\n\n## Results",
		},
		{
			name: "table wrappers in a group, the label without a full stop",
			xml: `<article><body><sec><title>Methods</title><table-wrap-group>` +
				`<table-wrap><label>Table 2</label><caption><title>Doses</title></caption>` +
				`<table><tr><td>x</td></tr></table></table-wrap></table-wrap-group></sec></body></article>`,
			want: "## Tables\nTable 2. Doses\nx\n\n## Methods",
		},
		{
			name: "a table wrapper inside a paragraph is lifted out of the prose",
			xml: `<article><body><sec><title>Results</title><p>Before.<table-wrap><label>Table 1</label>` +
				`<caption><p>Cap</p></caption><table><tr><td>x</td><td>y</td></tr></table></table-wrap> After.</p>` +
				`</sec></body></article>`,
			want: "## Tables\nTable 1. Cap\nx | y\n\n## Results\nBefore. After.",
		},
		{
			name: "tables kept in a floats group outside the body",
			xml: `<article><body><sec><title>Results</title><p>See Table 4.</p></sec></body>` +
				`<floats-group><table-wrap><label>Table 4</label><caption><p>Effects</p></caption>` +
				`<table><tr><td>OR</td><td>1.06</td></tr></table></table-wrap></floats-group></article>`,
			want: "## Tables\nTable 4. Effects\nOR | 1.06\n\n## Results\nSee Table 4.",
		},
		{
			name: "numbers and decimals keep their spacing",
			xml:  `<article><body><sec><title>Results</title><p>P &lt; .001 and from .5 to 1.</p></sec></body></article>`,
			want: "## Results\nP < .001 and from .5 to 1.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Text([]byte(tt.xml))

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTextRejectsNonJATS(t *testing.T) {
	for name, in := range map[string]string{
		"an HTML error page": `<html><body>Not found</body></html>`,
		"not XML at all":     `{"status": 500}`,
		"a broken document":  `<article><body><sec>`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Text([]byte(in))
			require.Error(t, err)
		})
	}
}

// The cap counts characters, not bytes, and never splits one.
func TestClip(t *testing.T) {
	assert.Equal(t, "ab–", Clip("ab–cd", 3), "a multi-byte character is kept whole")
	assert.Equal(t, "short", Clip("short", 100))
	assert.Equal(t, "no cap", Clip("no cap", 0))
}

// Fuzz: the converter takes whatever the network gives and must not panic.
func FuzzText(f *testing.F) {
	f.Add([]byte(`<article><body><sec><title>Results</title><p>x</p></sec></body></article>`))
	f.Add([]byte(`<article><body><table-wrap><table><tr><td colspan="99999999999">`))
	f.Add([]byte(`<?xml version="1.0"?><!DOCTYPE a [<!ENTITY e "x">]><article>&e;&nbsp;</article>`))
	f.Add([]byte(`<pmc-articleset><article><body><p>x</p></body></article></pmc-articleset>`))

	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = Text(data) // only a panic is a failure here
	})
}
