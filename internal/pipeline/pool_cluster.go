package pipeline

// Cluster-randomized trials. The participants of a cluster trial are not
// independent, so counts or means analysed as if individuals were randomized
// overstate the precision. The remedy (Cochrane Handbook 23.1.4) is the design
// effect DE = 1 + (m - 1) x ICC, m the mean cluster size: divide the number of
// participants (and of events) of each arm by it. An estimate whose own
// analysis accounted for clustering is used as reported.

import (
	"github.com/roman-redl/verdict/internal/model"
)

// defaultICC is the intracluster correlation assumed when the trial reports
// none. Handbook 23.1.4: typical values are below 0.05, so 0.05 is the upper end
// of the usual range — the cautious choice, as it shrinks the sample the most.
const defaultICC = 0.05

// designEffect is the factor by which clustering inflates the variance of a
// cluster trial with the given number of participants. The mean cluster size is
// the stated one, else the participants over the clusters; the intracluster
// correlation is the reported one, else defaultICC. ok is false when no cluster
// size can be had (no stated size and no cluster count).
func designEffect(cl *model.Cluster, participants float64) (de, icc float64, ok bool) {
	if cl == nil {
		return 0, 0, false
	}

	var m float64

	switch {
	case positiveNumber(cl.MeanSize):
		m = *cl.MeanSize
	case positiveNumber(cl.Clusters) && participants > 0:
		m = participants / *cl.Clusters
	default:
		return 0, 0, false
	}

	icc = defaultICC
	if cl.ICC != nil {
		icc = *cl.ICC
	}

	// a cluster of one person has no clustering to correct for
	return 1 + (max(m, 1)-1)*icc, icc, true
}

// accountsForClustering reports whether the effect's own analysis already did.
func accountsForClustering(ef *model.Effect) bool {
	return ef.Cluster != nil && ef.Cluster.AdjustedForClustering
}

// divideArm is a copy of the arm with the number of participants and of events
// divided by the design effect; means, SDs and percentages stay as they are.
func divideArm(a *model.Arm, de float64) *model.Arm {
	if a == nil {
		return nil
	}

	div := func(v *float64) *float64 {
		if v == nil {
			return nil
		}

		return new(*v / de)
	}

	out := *a
	out.Events, out.Total, out.N = div(a.Events), div(a.Total), div(a.N)

	return &out
}

// effectiveEffect is a copy of the effect with both arms divided by the design
// effect: the numbers as if the trial had randomized that many independent
// participants.
func effectiveEffect(ef *model.Effect, de float64) *model.Effect {
	out := *ef
	out.Intervention = divideArm(ef.Intervention, de)
	out.Control = divideArm(ef.Control, de)

	return &out
}

// clusterAdjusted returns the effect a cluster-randomized trial is pooled
// with. Counts or means that do not account for clustering are divided by the
// design effect, so that the participants the imprecision rule reads are the
// effective ones; ok is false when the design effect cannot be computed (the
// trial stays out: its precision would be overstated). Every other effect —
// another design, a reported estimate, an effect that accounts for clustering —
// is returned as it is.
func clusterAdjusted(
	d model.StudyDesign, ef *model.Effect, ps *pooledStudy,
) (adjusted *model.Effect, de, icc float64, ok bool) {
	armBased := ps.fromCounts || ps.continuous
	if d != model.DesignClusterRCT || !armBased || accountsForClustering(ef) {
		return ef, 1, 0, true
	}

	de, icc, ok = designEffect(ef.Cluster, float64(ps.n))
	if !ok {
		return nil, 0, 0, false
	}

	return effectiveEffect(ef, de), de, icc, true
}
