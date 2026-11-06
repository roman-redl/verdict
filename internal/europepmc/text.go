package europepmc

import (
	"html"
	"regexp"
	"strings"
)

var (
	// blockTags separate parts of the text (section headings, paragraphs):
	// they become line breaks.
	blockTags = regexp.MustCompile(`(?i)</?(?:h[1-6]|p|br|div|li|ul|ol)(?:\s[^<>]*)?/?>`)
	// anyTag is a well-formed tag (name, quoted attributes). A bare '<' in
	// the text ("aged 1 to <10 y", "P<.001") is not a tag: Europe PMC sends
	// such characters unescaped, so a naive "<[^>]*>" would eat the text.
	anyTag = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9]*(?:\s+[\w:.-]+(?:="[^"]*"|='[^']*')?)*\s*/?>`)
)

// plainText turns the HTML of an abstractText into plain text: section
// headings and paragraphs sit on their own lines, inline markup (<i>, <sup>,
// <b>) disappears, entities are decoded and whitespace is collapsed.
func plainText(s string) string {
	s = blockTags.ReplaceAllString(s, "\n")
	s = anyTag.ReplaceAllString(s, "")
	// decoded last: an escaped "&lt;b&gt;" is text, not markup
	s = html.UnescapeString(s)

	lines := make([]string, 0, 8)

	for line := range strings.SplitSeq(s, "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			lines = append(lines, strings.Join(f, " "))
		}
	}

	return strings.Join(lines, "\n")
}
