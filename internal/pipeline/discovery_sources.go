package pipeline

// The discovery channels of mode B and the switch that selects them: an audit
// of one channel's recall runs the others off.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// DiscoverySources is a set of discovery channels, each named by the
// model.Source it stamps on the candidates it finds.
type DiscoverySources []model.Source

// channelOrder is the canonical order of the channels, and the full set.
var channelOrder = DiscoverySources{model.SourceConsensus, model.SourcePubMed, model.SourceReviewRefs}

// ParseDiscoverySources parses a comma-separated list of channels (consensus,
// pubmed, review_refs; any subset, any order). The empty string is valid and
// means "not set": the run falls back to the configured default, all three.
func ParseDiscoverySources(s string) (DiscoverySources, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}

	want := map[model.Source]bool{}

	for part := range strings.SplitSeq(s, ",") {
		src := model.Source(strings.ToLower(strings.TrimSpace(part)))
		if !slices.Contains(channelOrder, src) {
			return nil, &UserError{Msg: fmt.Sprintf(
				"unknown discovery source %q (want any of consensus | pubmed | review_refs, comma-separated)", part)}
		}

		want[src] = true
	}

	var out DiscoverySources

	for _, src := range channelOrder {
		if want[src] {
			out = append(out, src)
		}
	}

	return out, nil
}

// UnmarshalText lets the environment parser reject an unknown channel at
// startup.
func (d *DiscoverySources) UnmarshalText(text []byte) error {
	parsed, err := ParseDiscoverySources(string(text))
	if err != nil {
		return err
	}

	*d = parsed

	return nil
}

// Has reports whether the channel is in the set.
func (d DiscoverySources) Has(src model.Source) bool { return slices.Contains(d, src) }

// discoverySources resolves the channels of a run: the run's flag, else the
// configured default, else all three.
func (p *Pipeline) discoverySources(opts Options) DiscoverySources {
	switch {
	case len(opts.Sources) > 0:
		return opts.Sources
	case len(p.set.DiscoverySources) > 0:
		return p.set.DiscoverySources
	}

	return channelOrder
}
