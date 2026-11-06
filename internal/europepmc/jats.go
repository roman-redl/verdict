package europepmc

// JATS XML → plain text. Europe PMC serves the full text of an open-access
// article as JATS (the NLM journal archiving format). The critic reads a
// capped head of the text, and the outcomes a systematic review needs are
// often secondary ones, reported only in the tables and the results text — so
// the output is not in document order: every table first (a long Results
// section must not push them past the cap), then the results, then the
// methods, then the rest (introduction, discussion, appendices).

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// The JATS element names the converter reads or skips.
const (
	tagArticle    = "article"
	tagBody       = "body"
	tagBack       = "back"
	tagFloats     = "floats-group"
	tagSec        = "sec"
	tagTitle      = "title"
	tagLabel      = "label"
	tagCaption    = "caption"
	tagParagraph  = "p"
	tagList       = "list"
	tagTableWrap  = "table-wrap"
	tagTableGroup = "table-wrap-group"
	tagTableFoot  = "table-wrap-foot"
	tagRow        = "tr"
	tagCell       = "td"
	tagHeadCell   = "th"
	tagXref       = "xref"
	attrSecType   = "sec-type"
	attrRefType   = "ref-type"
	attrColspan   = "colspan"
	refTypeBibr   = "bibr"
)

// skipped elements carry no evidence for the critic: the reference list,
// MathML and TeX (formulas do not survive a text conversion), supplementary
// file links, identifiers and licences.
var skipped = map[string]bool{
	"ref-list": true, "math": true, "tex-math": true, "supplementary-material": true,
	"object-id": true, "permissions": true, "graphic": true, "inline-graphic": true,
	"media": true, "disp-formula": true, "sec-meta": true,
}

// spaced elements are blocks inside a run of inline text: a space on both
// sides keeps the neighbouring words apart.
var spaced = map[string]bool{
	tagParagraph: true, tagTitle: true, tagCaption: true, tagList: true, "list-item": true,
	"break": true, "fn": true, tagLabel: true,
}

// containers are elements that nest further blocks and may carry a title.
var containers = map[string]bool{"sec": true, "boxed-text": true, "app": true, "app-group": true}

// keptAttrs are the attributes the converter reads; the rest are dropped
// while parsing to keep the tree small.
var keptAttrs = map[string]bool{attrSecType: true, attrRefType: true, attrColspan: true}

var (
	resultsTitle = regexp.MustCompile(`(?i)\b(?:results?|findings?|outcomes?)\b`)
	methodsTitle = regexp.MustCompile(`(?i)method|material|procedure|design|statistic`)

	// What a dropped citation marker leaves in the prose: "( )", "[,]", a
	// space before a comma, doubled commas, ", ." at the end of a clause. A
	// full stop is not touched otherwise: " .001" is a number.
	emptyBrackets      = regexp.MustCompile(`\(\s*[,;\s]*\)|\[\s*[,;\s]*\]`)
	spaceBeforeComma   = regexp.MustCompile(`\s+([,;])`)
	repeatedDelimiters = regexp.MustCompile(`([,;])(?:\s*[,;])+`)
	commaBeforeStop    = regexp.MustCompile(`[,;]\s*\.(\s|$)`)
)

// part is where a section lands in the output.
type part int

const (
	partRest part = iota
	partResults
	partMethods
)

const (
	// maxHeading is the deepest heading level (the markdown limit).
	maxHeading = 6
	// maxColspan bounds the padding of a cell that spans columns.
	maxColspan = 24
)

// node is one element of the parsed document; a text node has an empty name.
type node struct {
	name  string
	attrs map[string]string
	text  string
	kids  []*node
}

// child returns the first child element with the given name, or nil.
func (n *node) child(name string) *node {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}

	return nil
}

// parseJATS builds the element tree. Namespace prefixes (mml:math) are
// reduced to the local name, entities are decoded leniently.
func parseJATS(data []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity

	root := &node{name: "#root"}
	stack := []*node{root}

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("decoding the XML: %w", err)
		}

		top := stack[len(stack)-1]

		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local, attrs: pickAttrs(t.Attr)}
			top.kids = append(top.kids, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			top.kids = append(top.kids, &node{text: string(t)})
		}
	}

	article := root.child(tagArticle)
	if article == nil {
		return nil, errors.New("no <article> element: not a JATS document")
	}

	return article, nil
}

func pickAttrs(attrs []xml.Attr) map[string]string {
	var out map[string]string

	for _, a := range attrs {
		if !keptAttrs[a.Name.Local] {
			continue
		}

		if out == nil {
			out = map[string]string{}
		}

		out[a.Name.Local] = a.Value
	}

	return out
}

// fullTextFromXML converts a JATS article to plain text in the order
// described above; "" when the article has no body (a record without the
// full text).
func fullTextFromXML(data []byte) (string, error) {
	article, err := parseJATS(data)
	if err != nil {
		return "", err
	}

	c := &converter{}

	if body := article.child(tagBody); body != nil {
		c.takeTables(body)
		c.body(body)
	}

	if back := article.child(tagBack); back != nil {
		c.takeTables(back)

		for _, k := range back.kids {
			if k.name == "app-group" || k.name == "app" {
				c.add(partRest, c.section(k, 2))
			}
		}
	}

	// some publishers (Dove Press, for one) keep the tables out of the body,
	// in a floats group at the article level: the body only cites them
	if floats := article.child(tagFloats); floats != nil {
		c.takeTables(floats)
	}

	return c.render(), nil
}

// converter collects the output blocks per part and the tables apart.
type converter struct {
	parts  [3][]string // by part: one block per top-level section
	tables []string
}

func (c *converter) add(p part, block []string) {
	if len(block) > 0 {
		c.parts[p] = append(c.parts[p], strings.Join(block, "\n"))
	}
}

// body sorts the top-level sections into the parts; text sitting directly in
// the body (a short article without sections) goes to the rest.
func (c *converter) body(body *node) {
	var loose []string

	for _, k := range body.kids {
		if k.name != tagSec {
			loose = append(loose, c.block(k, 1)...)
			continue
		}

		title := ""
		if t := k.child(tagTitle); t != nil {
			title = inline(t)
		}

		c.add(classify(title, k.attrs[attrSecType]), c.section(k, 2))
	}

	c.add(partRest, loose)
}

func classify(title, secType string) part {
	key := title + " " + secType

	switch {
	case resultsTitle.MatchString(key):
		return partResults
	case methodsTitle.MatchString(key):
		return partMethods
	default:
		return partRest
	}
}

// render joins the parts: tables, results, methods, the rest.
func (c *converter) render() string {
	var out []string

	if len(c.tables) > 0 {
		out = append(out, "## Tables\n"+strings.Join(c.tables, "\n\n"))
	}

	out = append(out, c.parts[partResults]...)
	out = append(out, c.parts[partMethods]...)
	out = append(out, c.parts[partRest]...)

	return strings.Join(out, "\n\n")
}

// section renders a container: its title as a heading of the given level,
// then its blocks; nested sections go one level deeper.
func (c *converter) section(n *node, level int) []string {
	var lines []string

	if t := n.child(tagTitle); t != nil {
		if title := inline(t); title != "" {
			lines = append(lines, strings.Repeat("#", min(level, maxHeading))+" "+title)
		}
	}

	for _, k := range n.kids {
		lines = append(lines, c.block(k, level+1)...)
	}

	return lines
}

// takeTables detaches every table wrapper from the subtree and renders it
// into the tables part, in document order. It runs before the text is
// rendered: publishers put a table wrapper not only between paragraphs but
// also inside one, where it would otherwise be flattened into the prose.
func (c *converter) takeTables(n *node) {
	kept := n.kids[:0]

	for _, k := range n.kids {
		if k.name == tagTableWrap || k.name == tagTableGroup {
			c.tables = append(c.tables, c.tablesOf(k)...)
			continue
		}

		c.takeTables(k)

		kept = append(kept, k)
	}

	n.kids = kept
}

// block renders one child of a container. The title is the caller's
// business; tables are already taken apart.
func (c *converter) block(n *node, level int) []string {
	switch {
	case n.name == "" || n.name == tagTitle || n.name == tagLabel || skipped[n.name]:
		return nil
	case containers[n.name]:
		return c.section(n, level)
	case n.name == tagList:
		return listItems(n)
	case n.name == "fig" || n.name == "fig-group":
		return nonEmpty(captioned("Figure", n))
	default:
		return nonEmpty(inline(n))
	}
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}

	return []string{s}
}

// listItems renders a list, one item per line.
func listItems(n *node) []string {
	var out []string

	for _, k := range n.kids {
		switch k.name {
		case "list-item":
			if s := inline(k); s != "" {
				out = append(out, "- "+s)
			}
		case tagList:
			out = append(out, listItems(k)...)
		}
	}

	return out
}

// captioned renders "<label> <caption>" of a figure or table; fallback names
// the kind when the label is missing.
func captioned(fallback string, n *node) string {
	label, caption := "", ""

	if l := n.child(tagLabel); l != nil {
		label = inline(l)
	}

	if cp := n.child(tagCaption); cp != nil {
		caption = inline(cp)
	}

	switch {
	case label == "" && caption == "":
		return ""
	case label == "":
		label = fallback + "."
	case caption != "" && !strings.HasSuffix(label, ".") && !strings.HasSuffix(label, ":"):
		label += "."
	}

	return strings.TrimSpace(label + " " + caption)
}

// tablesOf renders a table wrapper (or a group of them): caption, one line
// per row with the cells separated by " | ", then the footnotes.
func (c *converter) tablesOf(n *node) []string {
	if n.name == tagTableGroup {
		var out []string

		for _, k := range n.kids {
			if k.name == tagTableWrap {
				out = append(out, c.tablesOf(k)...)
			}
		}

		return out
	}

	lines := nonEmpty(captioned("Table", n))

	var rows []*node

	collectRows(n, &rows)

	for _, r := range rows {
		if line := rowLine(r); line != "" {
			lines = append(lines, line)
		}
	}

	if foot := n.child(tagTableFoot); foot != nil {
		for _, k := range foot.kids {
			lines = append(lines, nonEmpty(inline(k))...)
		}
	}

	if len(lines) == 0 {
		return nil
	}

	return []string{strings.Join(lines, "\n")}
}

// collectRows gathers the table rows in document order; footnotes and
// nested wrappers are not searched.
func collectRows(n *node, out *[]*node) {
	for _, k := range n.kids {
		switch k.name {
		case tagRow:
			*out = append(*out, k)
		case tagTableFoot, tagCaption, tagLabel, "":
		default:
			collectRows(k, out)
		}
	}
}

// rowLine joins the cells of a row with " | ". A cell spanning several
// columns is followed by empty cells so the columns stay aligned; trailing
// empty cells and rows with no text at all are dropped.
func rowLine(row *node) string {
	var cells []string

	for _, k := range row.kids {
		if k.name != tagCell && k.name != tagHeadCell {
			continue
		}

		cells = append(cells, inline(k))

		if span, err := strconv.Atoi(k.attrs[attrColspan]); err == nil {
			for range min(span, maxColspan) - 1 {
				cells = append(cells, "")
			}
		}
	}

	for len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}

	return strings.Join(cells, " | ")
}

// inline returns the text of an element with the whitespace collapsed:
// inline markup disappears, citation markers of the reference list ([12])
// and skipped elements are dropped, and the punctuation they leave behind
// ("common , .") is tidied.
func inline(n *node) string {
	var sb strings.Builder

	writeInline(&sb, n)

	s := strings.Join(strings.Fields(sb.String()), " ")
	s = emptyBrackets.ReplaceAllString(s, "")
	s = spaceBeforeComma.ReplaceAllString(s, "$1")
	s = repeatedDelimiters.ReplaceAllString(s, "$1")
	s = commaBeforeStop.ReplaceAllString(s, ".$1")

	return strings.Join(strings.Fields(s), " ")
}

func writeInline(sb *strings.Builder, n *node) {
	if n.name == "" {
		sb.WriteString(n.text)
		return
	}

	if skipped[n.name] || (n.name == tagXref && n.attrs[attrRefType] == refTypeBibr) {
		return
	}

	pad := spaced[n.name]
	if pad {
		sb.WriteByte(' ')
	}

	for _, k := range n.kids {
		writeInline(sb, k)
	}

	if pad {
		sb.WriteByte(' ')
	}
}
