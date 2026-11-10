package pipeline

// The discovery channels of mode B and the switch that selects them: an audit
// of one channel's recall runs the others off.

import (
	"cmp"
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

// defaultChannels are the channels that always run when nothing selects
// others; PubMed joins them by its own mode (auto by default).
var defaultChannels = DiscoverySources{model.SourceConsensus, model.SourceReviewRefs}

// ParseDiscoverySources parses a comma-separated list of channels (consensus,
// pubmed, review_refs; any subset, any order). The empty string is valid and
// means "not set": the run falls back to the default channels, with PubMed
// decided by its mode.
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

// without returns the set minus one channel.
func (d DiscoverySources) without(src model.Source) DiscoverySources {
	return slices.DeleteFunc(slices.Clone(d), func(s model.Source) bool { return s == src })
}

// PubMedMode says whether the PubMed channel runs: always (on), never (off), or
// only when the Consensus pool is too thin in reviews for the reference mining
// to carry the recall (auto).
type PubMedMode string

const (
	// PubMedAuto runs PubMed when the Consensus pool holds fewer than
	// autoPubMedMinReviews systematic reviews or meta-analyses.
	PubMedAuto PubMedMode = "auto"
	// PubMedOn always runs PubMed.
	PubMedOn PubMedMode = "on"
	// PubMedOff never runs PubMed.
	PubMedOff PubMedMode = "off"
)

// autoPubMedMinReviews is how many reviews the Consensus pool needs for PubMed
// to stay off in auto mode: measured, the reference lists of the reviews plus
// Consensus find what PubMed adds, but with fewer reviews the mining has little
// to work with and PubMed is the fallback channel.
const autoPubMedMinReviews = 2

// ParsePubMedMode parses the --pubmed flag and the PUBMED_DISCOVERY variable.
// The empty string is valid and means "not set": the run falls back to the
// channel list, then to auto.
func ParsePubMedMode(s string) (PubMedMode, error) {
	m := PubMedMode(strings.ToLower(strings.TrimSpace(s)))

	switch m {
	case "", PubMedAuto, PubMedOn, PubMedOff:
		return m, nil
	}

	return "", &UserError{Msg: fmt.Sprintf("unknown PubMed mode %q (want auto | on | off)", s)}
}

// UnmarshalText lets the environment parser reject an unknown mode at startup.
func (m *PubMedMode) UnmarshalText(text []byte) error {
	parsed, err := ParsePubMedMode(string(text))
	if err != nil {
		return err
	}

	*m = parsed

	return nil
}

// PubMedDecision records how the PubMed channel was settled for a run.
type PubMedDecision struct {
	Mode PubMedMode `json:"mode"` // the mode the run resolved to: auto | on | off
	// ConsensusReviews is the number of systematic reviews and meta-analyses in
	// the Consensus pool the decision looked at; counted in every mode, acted
	// on only in auto.
	ConsensusReviews int  `json:"consensus_reviews"`
	Enabled          bool `json:"enabled"` // whether the PubMed channel ran
}

// channelPlan is the channel selection of a run before the Consensus walk:
// the channels that run for sure (PubMed only when forced on) and the PubMed
// mode, never empty.
type channelPlan struct {
	sources DiscoverySources
	pubmed  PubMedMode
}

// channelPlan resolves the channels of a run. The defaults are Consensus and
// the review references with PubMed on auto. A channel list (the run's flag,
// else the configured one) is taken literally: PubMed runs only when listed,
// so "--discovery-sources consensus" audits Consensus alone. The PubMed mode
// (flag, else configured) overrides that: on adds the channel, off removes it,
// auto leaves it to the Consensus pool.
func (p *Pipeline) channelPlan(opts Options) channelPlan {
	listed := opts.Sources
	if len(listed) == 0 {
		listed = p.set.DiscoverySources
	}

	mode := cmp.Or(opts.PubMed, p.set.PubMed)

	if len(listed) == 0 {
		return channelPlan{sources: defaultChannels, pubmed: cmp.Or(mode, PubMedAuto)}
	}

	if mode == "" {
		mode = PubMedOff
		if listed.Has(model.SourcePubMed) {
			mode = PubMedOn
		}
	}

	return channelPlan{sources: listed.without(model.SourcePubMed), pubmed: mode}
}

// decidePubMed settles the PubMed channel once the Consensus pool is walked:
// it records the decision and the review count in info and logs them. It
// reports whether PubMed runs.
func (p *Pipeline) decidePubMed(info *DiscoveryInfo, mode PubMedMode, pool []Candidate) bool {
	d := &PubMedDecision{Mode: mode, ConsensusReviews: countReviews(pool)}

	switch mode {
	case PubMedOn:
		d.Enabled = true
	case PubMedOff:
		d.Enabled = false
	case PubMedAuto:
		d.Enabled = d.ConsensusReviews < autoPubMedMinReviews
	}

	info.PubMedDecision = d
	p.log.Info("PubMed channel decided", "mode", mode, "consensus_reviews", d.ConsensusReviews,
		"min_reviews_for_off", autoPubMedMinReviews, "enabled", d.Enabled)

	return d.Enabled
}

// channelsRun is the final channel set in canonical order: the planned
// channels plus PubMed when it runs.
func channelsRun(planned DiscoverySources, pubmed bool) DiscoverySources {
	var out DiscoverySources

	for _, src := range channelOrder {
		if planned.Has(src) || (src == model.SourcePubMed && pubmed) {
			out = append(out, src)
		}
	}

	return out
}
