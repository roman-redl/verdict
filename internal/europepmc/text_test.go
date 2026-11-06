package europepmc

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlainText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain text stays", "No markup here.", "No markup here."},
		{
			"section headings on their own lines",
			"<h4>Objectives</h4>To compare A with B.<h4>Methods</h4>66,680 women.",
			"Objectives\nTo compare A with B.\nMethods\n66,680 women.",
		},
		{
			"inline markup is dropped",
			"(95% CI; <i>P</i> = 0.96) H<sub>2</sub>O m<sup>2</sup> <b>bold</b>",
			"(95% CI; P = 0.96) H2O m2 bold",
		},
		{"tag with attributes", `x <a href="https://e.org/?a=1&amp;b=2" class='k'>link</a> y`, "x link y"},
		{"bare less-than is text", "aged 1 to <10 y; P<.001; P < 0.001; a<b", "aged 1 to <10 y; P<.001; P < 0.001; a<b"},
		{
			"entities are decoded",
			"Fish &amp; chips, &lt;5 mm, &#946;-blocker, caf&eacute;",
			"Fish & chips, <5 mm, β-blocker, café",
		},
		{"escaped markup stays text", "&lt;b&gt;not bold&lt;/b&gt;", "<b>not bold</b>"},
		{"paragraphs and breaks", "<p>One.</p><p>Two.<br/>Three.<br>Four.</p>", "One.\nTwo.\nThree.\nFour."},
		{"whitespace is collapsed", "  A \t b c d  \n\n\n  e  ", "A b c d\ne"},
		{"markup only", "<h4> </h4>", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, plainText(tt.in))
		})
	}
}

func FuzzPlainText(f *testing.F) {
	f.Add("<h4>Aim</h4>x &amp; <i>y</i> <10")
	f.Add("<<>>&&;<a")

	f.Fuzz(func(t *testing.T, in string) {
		out := plainText(in)

		assert.Equal(t, strings.TrimSpace(out), out, "no leading or trailing whitespace")
		assert.NotContains(t, out, "\n\n", "no empty lines")
		assert.NotContains(t, out, "  ", "no double spaces")
	})
}
