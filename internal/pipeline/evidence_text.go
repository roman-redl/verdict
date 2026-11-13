package pipeline

// The wording of the evidence-base summary (evidence_base.go): one paragraph,
// English, with the numbers written by Go. The research skill translates it for
// the user.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// noPoolText is the summary's line when the report has no pooled estimate.
const noPoolText = "Not pooled: no direct study with usable numbers on the outcome."

// evidenceText renders the summary paragraph: the papers reviewed and the
// direct studies by design group, then the pool, the retracted papers left out,
// the number of indirect or off-topic papers and the papers not assessed.
func evidenceText(eb *model.EvidenceBase, pe *model.PooledEstimate) string {
	s := fmt.Sprintf("%d %s reviewed; %s %s", eb.Reviewed, plural(eb.Reviewed, "paper", "papers"),
		directText(eb), pooledSentence(pe))

	if eb.Retracted > 0 {
		s += fmt.Sprintf(" Retracted, left out: %d.", eb.Retracted)
	}

	s += fmt.Sprintf(" Indirect or off-topic: %d.", eb.Indirect)

	if eb.NotAssessed > 0 {
		s += fmt.Sprintf(" Not assessed (critique failed): %d.", eb.NotAssessed)
	}

	return s
}

// directText is the sentence about the direct studies, with its final period.
func directText(eb *model.EvidenceBase) string {
	if eb.Direct == 0 {
		return "none answer the question directly."
	}

	head := fmt.Sprintf("%d %s the question directly", eb.Direct, plural(eb.Direct, "answers", "answer"))
	if n := eb.RepeatReports; n > 0 {
		head += fmt.Sprintf(" (%d %s of the same trials counted once)", n, plural(n, "repeat report", "repeat reports"))
	}

	var parts []string

	if g := eb.Randomized; g != nil {
		parts = append(parts, groupText(g, "randomized trial", "randomized trials", true))
	}

	if g := eb.Observational; g != nil {
		parts = append(parts, groupText(g, "observational study", "observational studies", false))
	}

	if g := eb.Reviews; g != nil {
		s := fmt.Sprintf("%d %s", g.Studies,
			plural(g.Studies, "systematic review or meta-analysis", "systematic reviews or meta-analyses"))
		if g.Cochrane > 0 {
			s += fmt.Sprintf(" (%d Cochrane)", g.Cochrane)
		}

		parts = append(parts, s)
	}

	if n := eb.Other; n > 0 {
		parts = append(parts, fmt.Sprintf("%d other %s (narrative reviews, guidelines, other or unclear designs)",
			n, plural(n, "publication", "publications")))
	}

	return head + ": " + strings.Join(parts, ", ") + "."
}

// groupText is one group of the sentence: the count and the noun, then the
// blinding (randomized trials only) and the sizes in brackets.
func groupText(g *model.EvidenceGroup, one, many string, withBlinding bool) string {
	s := fmt.Sprintf("%d %s", g.Studies, plural(g.Studies, one, many))

	var details []string

	if withBlinding {
		if b := blindingText(g); b != "" {
			details = append(details, b)
		}
	}

	details = append(details, sizeText(g)...)

	return s + " (" + strings.Join(details, "; ") + ")"
}

// blindingText counts the group's trials by masking.
func blindingText(g *model.EvidenceGroup) string {
	var parts []string

	for _, p := range []struct {
		n    int
		name string
	}{
		{g.DoubleBlind, "double-blind"},
		{g.SingleBlind, "single-blind"},
		{g.Open, "open-label"},
		{g.BlindingNotStated, "blinding not stated"},
		{g.BlindingNotAssessed, "blinding not assessed"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.name))
		}
	}

	return strings.Join(parts, ", ")
}

// sizeText gives the participants of the studies whose size is known and says
// how many have none; with one known size there is no median or largest to show.
func sizeText(g *model.EvidenceGroup) []string {
	known := g.Studies - g.SizeNotStated

	var out []string

	switch known {
	case 0:
		if g.Studies == 1 {
			return []string{"size not stated"}
		}

		return []string{fmt.Sprintf("size not stated for all %d", g.Studies)}
	case 1:
		out = append(out, fmt.Sprintf("%d participants — %s", g.Participants, g.LargestStudy))
	default:
		out = append(out, fmt.Sprintf("%d participants, median %d, largest %d — %s",
			g.Participants, g.MedianSize, g.Largest, g.LargestStudy))
	}

	if g.SizeNotStated > 0 {
		out = append(out, fmt.Sprintf("size not stated for %d", g.SizeNotStated))
	}

	return out
}

// pooledSentence is the pool in one sentence: the studies, participants and
// events of the pooled estimate, or why nothing was pooled. A single study is
// worded as the report's pooled line is (pooledLabel).
func pooledSentence(pe *model.PooledEstimate) string {
	if pe == nil {
		return noPoolText
	}

	var counts []string

	if pe.Participants > 0 {
		counts = append(counts, strconv.Itoa(pe.Participants)+" participants")
	}

	if pe.Events > 0 {
		counts = append(counts, strconv.Itoa(pe.Events)+" events")
	}

	if pe.Method == model.PoolSingleStudy {
		s := "Not pooled: one study with numbers"
		if len(counts) > 0 {
			s += " (" + strings.Join(counts, ", ") + ")"
		}

		return s + "."
	}

	noun := "studies"
	if pe.Stratum == model.StratumRandomized {
		noun = "trials"
	}

	return "Pooled: " + strings.Join(append([]string{fmt.Sprintf("%d %s", pe.K, noun)}, counts...), ", ") + "."
}
