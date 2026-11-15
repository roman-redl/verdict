package pipeline

// The list of the direct studies under the evidence-base line of the report: a
// code block with one entry per study, the largest first, each with a bar whose
// length follows the logarithm of the number of participants. It makes the
// size of the evidence base visible at a glance: a verdict that stands on one
// small trial looks different from one that stands on several large ones.

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	// maxListRows caps the studies the list shows; the rest are counted.
	maxListRows = 30
	// listBarBlocks is the bar of the largest study.
	listBarBlocks = 16
	// listTitleChars cuts the title under a study.
	listTitleChars = 70
	listBlock      = "█"
	listSep        = " · "
	// listYearWidth is the width of the year column.
	listYearWidth = 4
	codeFence     = "```"
)

// listRow is one study of the list, every field already worded.
type listRow struct {
	size, bar, design, year, direction, pooled, title string
}

// listWidths are the widths of the padded columns.
type listWidths struct {
	size, bar, design, direction int
}

// studyList renders the list of the direct studies as a header line and a code
// block, with the trailing blank line; empty for an evidence base without
// studies (a report written before the list existed, or no direct study).
func (l lexicon) studyList(eb *model.EvidenceBase) string {
	if len(eb.Studies) == 0 {
		return ""
	}

	primary, reviews := splitReviews(eb.Studies)
	slices.SortStableFunc(primary, func(a, b model.EvidenceStudy) int {
		return cmp.Compare(b.Participants, a.Participants) // unknown sizes are zero: last
	})

	shown := append(slices.Clone(primary), reviews...)
	hidden := 0

	if len(shown) > maxListRows {
		hidden = len(shown) - maxListRows
		shown = shown[:maxListRows]
	}

	largest := 0
	if len(primary) > 0 {
		largest = primary[0].Participants
	}

	rows := make([]listRow, len(shown))
	for i, s := range shown {
		rows[i] = l.listRowOf(s, largest)
	}

	w := columnWidths(rows)
	indent := strings.Repeat(" ", w.size+1) // the title starts where the bar does

	var lines []string

	for i, r := range rows {
		if i > 0 && i == len(primary) {
			lines = append(lines, "") // the reviews are a group of their own
		}

		lines = append(lines, r.line(w), indent+r.title)
	}

	if hidden > 0 {
		lines = append(lines, fmt.Sprintf(l.list.moreFmt, hidden))
	}

	lines = append(lines, "", fmt.Sprintf("%s: %d%s%s: %d",
		l.sentence.indirect, eb.Indirect, listSep, l.list.notAssessed, eb.NotAssessed))

	return l.list.header + "\n\n" + codeFence + "\n" + strings.Join(lines, "\n") + "\n" + codeFence + "\n\n"
}

// splitReviews separates the systematic reviews and meta-analyses, which are
// listed after the primary studies, from the other direct studies.
func splitReviews(studies []model.EvidenceStudy) (primary, reviews []model.EvidenceStudy) {
	for _, s := range studies {
		if evidenceKindOf(s.Design) == evidenceReview {
			reviews = append(reviews, s)
		} else {
			primary = append(primary, s)
		}
	}

	return primary, reviews
}

// listRowOf words one study. largest is the size of the largest primary study,
// which the bars are scaled to.
func (l lexicon) listRowOf(s model.EvidenceStudy, largest int) listRow {
	r := listRow{
		size:      "—",
		design:    l.designLabel(s),
		year:      "—",
		direction: l.directionWord(s.Direction),
		title:     shortTitle(strings.ReplaceAll(s.Title, "`", "'"), listTitleChars), // a fence must not end early
	}

	switch {
	case evidenceKindOf(s.Design) == evidenceReview:
		r.size = l.list.review
	case s.Participants > 0:
		r.size = strconv.Itoa(s.Participants)
		r.bar = strings.Repeat(listBlock, barBlocks(s.Participants, largest))
	}

	if s.Year > 0 {
		r.year = strconv.Itoa(s.Year)
	}

	if s.Pooled {
		r.pooled = l.list.pooled
	}

	return r
}

// designLabel is the design of a study with, for a randomized trial, its
// blinding, and the mark of a Cochrane review: "RCT, double-blind".
func (l lexicon) designLabel(s model.EvidenceStudy) string {
	label, ok := l.list.designs[s.Design]
	if !ok {
		label = orDash(string(s.Design))
	}

	if s.Design.IsRandomized() && s.Blinding != "" {
		if name, known := l.blindingNames[s.Blinding]; known {
			label += ", " + name
		}
	}

	if s.Cochrane {
		label += " (" + cochraneName + ")"
	}

	return label
}

// directionWord names where a study's result points relative to the claim.
func (l lexicon) directionWord(d model.StudyDirection) string {
	if word, ok := l.list.directions[d]; ok {
		return word
	}

	return l.list.directions[model.DirectionUnclear]
}

// barBlocks is the length of the bar of a study: proportional to the logarithm
// of its size, so that the largest study gets listBarBlocks and any study of a
// known size at least one block.
func barBlocks(n, largest int) int {
	if n <= 0 {
		return 0
	}

	if largest <= 1 { // log10 of 1 is zero: the sizes cannot be told apart
		return listBarBlocks
	}

	blocks := int(math.Round(listBarBlocks * math.Log10(float64(n)) / math.Log10(float64(largest))))

	return max(1, min(blocks, listBarBlocks))
}

// columnWidths measures the padded columns of the rows in characters.
func columnWidths(rows []listRow) listWidths {
	var w listWidths

	for _, r := range rows {
		w.size = max(w.size, len([]rune(r.size)))
		w.bar = max(w.bar, len([]rune(r.bar)))
		w.design = max(w.design, len([]rune(r.design)))
		w.direction = max(w.direction, len([]rune(r.direction)))
	}

	if w.bar > 0 {
		w.bar = listBarBlocks // the bars start at the same place whatever the largest shown
	}

	return w
}

// line is the first line of a study: size and bar, then the design, the year,
// the direction and the pooled mark, in columns. fmt pads by characters, not
// bytes, so the Russian words and the bars align.
func (r listRow) line(w listWidths) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%*s", w.size, r.size)

	if w.bar > 0 {
		fmt.Fprintf(&b, " %-*s", w.bar, r.bar)
	}

	fmt.Fprintf(&b, "%s%-*s%s%-*s%s%-*s", listSep, w.design, r.design, listSep, listYearWidth, r.year,
		listSep, w.direction, r.direction)

	if r.pooled != "" {
		b.WriteString(listSep + r.pooled)
	}

	return strings.TrimRight(b.String(), " ")
}
