package pipeline

// The wording of the evidence-base summary (evidence_base.go): one paragraph
// with the numbers written by Go, in the language of the lexicon.

import (
	"fmt"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// evidenceText renders the summary paragraph: the papers reviewed and the
// direct studies by design group, then the pool, the retracted papers left out,
// the number of indirect or off-topic papers and the papers not assessed.
func (l lexicon) evidenceText(eb *model.EvidenceBase, pe *model.PooledEstimate) string {
	w := l.sentence
	s := w.reviewed(eb.Reviewed) + "; " + l.directText(eb) + " " + l.pooledSentence(pe)

	if eb.Retracted > 0 {
		s += fmt.Sprintf(" %s: %d.", w.retracted, eb.Retracted)
	}

	s += fmt.Sprintf(" %s: %d.", w.indirect, eb.Indirect)

	if eb.NotAssessed > 0 {
		s += fmt.Sprintf(" %s: %d.", w.notAssessed, eb.NotAssessed)
	}

	return s
}

// directText is the sentence about the direct studies, with its final period.
func (l lexicon) directText(eb *model.EvidenceBase) string {
	w := l.sentence

	if eb.Direct == 0 {
		return w.noneDirect
	}

	head := w.directHead(eb.Direct)
	if n := eb.RepeatReports; n > 0 {
		head += w.repeat(n)
	}

	var parts []string

	if g := eb.Randomized; g != nil {
		parts = append(parts, l.groupText(g, w.randomized, true))
	}

	if g := eb.Observational; g != nil {
		parts = append(parts, l.groupText(g, w.observational, false))
	}

	if g := eb.Reviews; g != nil {
		s := l.count(g.Studies, w.reviews)
		if g.Cochrane > 0 {
			s += fmt.Sprintf(" (%d %s)", g.Cochrane, cochraneName)
		}

		parts = append(parts, s)
	}

	if n := eb.Other; n > 0 {
		parts = append(parts, l.count(n, w.other)+w.otherNote)
	}

	return head + ": " + strings.Join(parts, ", ") + "."
}

// groupText is one group of the sentence: the count and the noun, then the
// blinding (randomized trials only) and the sizes in brackets.
func (l lexicon) groupText(g *model.EvidenceGroup, noun forms, withBlinding bool) string {
	var details []string

	if withBlinding {
		if b := l.blindingText(g); b != "" {
			details = append(details, b)
		}
	}

	details = append(details, l.sizeText(g)...)

	return l.count(g.Studies, noun) + " (" + strings.Join(details, "; ") + ")"
}

// blindingText counts the group's trials by masking.
func (l lexicon) blindingText(g *model.EvidenceGroup) string {
	var parts []string

	for _, p := range []struct {
		n int
		b model.Blinding
	}{
		{g.DoubleBlind, model.BlindingDouble},
		{g.SingleBlind, model.BlindingSingle},
		{g.Open, model.BlindingOpen},
		{g.BlindingNotStated, model.BlindingUnclear},
		{g.BlindingNotAssessed, ""},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf(l.item, p.n, l.blindingNames[p.b]))
		}
	}

	return strings.Join(parts, ", ")
}

// sizeText gives the participants of the studies whose size is known and says
// how many have none; with one known size there is no median or largest to show.
func (l lexicon) sizeText(g *model.EvidenceGroup) []string {
	w := l.sentence
	known := g.Studies - g.SizeNotStated

	var out []string

	switch known {
	case 0:
		if g.Studies == 1 {
			return []string{w.sizeNone}
		}

		return []string{fmt.Sprintf(w.sizeNoneAll, g.Studies)}
	case 1:
		out = append(out, l.count(g.Participants, l.participants)+" — "+g.LargestStudy)
	default:
		out = append(out, fmt.Sprintf("%s, %s %d, %s %d — %s", l.count(g.Participants, l.participants),
			w.median, g.MedianSize, w.largest, g.Largest, g.LargestStudy))
	}

	if g.SizeNotStated > 0 {
		out = append(out, fmt.Sprintf(w.sizeNoneFor, g.SizeNotStated))
	}

	return out
}

// pooledSentence is the pool in one sentence: the studies, participants and
// events of the pooled estimate, or why nothing was pooled. A single study is
// worded as the report's pooled line is (pooledLabel).
func (l lexicon) pooledSentence(pe *model.PooledEstimate) string {
	w := l.sentence

	if pe == nil {
		return w.noPool
	}

	var counts []string

	if pe.Participants > 0 {
		counts = append(counts, l.count(pe.Participants, l.participants))
	}

	if pe.Events > 0 {
		counts = append(counts, l.count(pe.Events, l.events))
	}

	if pe.Method == model.PoolSingleStudy {
		s := w.notPooledOne
		if len(counts) > 0 {
			s += " (" + strings.Join(counts, ", ") + ")"
		}

		return s + "."
	}

	noun := w.studies
	if pe.Stratum == model.StratumRandomized {
		noun = w.trials
	}

	return w.pooledPrefix + strings.Join(append([]string{l.count(pe.K, noun)}, counts...), ", ") + "."
}
