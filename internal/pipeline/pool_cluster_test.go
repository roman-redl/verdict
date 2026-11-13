package pipeline

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/meta"
	"github.com/roman-redl/verdict/internal/model"
)

func TestDesignEffect(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cluster      *model.Cluster
		participants float64
		wantDE       float64
		wantICC      float64
		wantOK       bool
	}{
		{
			name:    "the reported ICC and the participants over the clusters",
			cluster: &model.Cluster{Clusters: new(10.0), ICC: new(0.02)}, participants: 200,
			wantDE: 1 + 19*0.02, wantICC: 0.02, wantOK: true,
		},
		{
			name:    "no ICC reported: the default",
			cluster: &model.Cluster{Clusters: new(10.0)}, participants: 200,
			wantDE: 1 + 19*defaultICC, wantICC: defaultICC, wantOK: true,
		},
		{
			name:    "the stated mean size wins over the participants per cluster",
			cluster: &model.Cluster{Clusters: new(10.0), MeanSize: new(30.0)}, participants: 200,
			wantDE: 1 + 29*defaultICC, wantICC: defaultICC, wantOK: true,
		},
		{
			name:    "a mean size without the cluster count",
			cluster: &model.Cluster{MeanSize: new(25.0), ICC: new(0.1)}, participants: 200,
			wantDE: 1 + 24*0.1, wantICC: 0.1, wantOK: true,
		},
		{
			name:    "an ICC of zero reported: no inflation",
			cluster: &model.Cluster{Clusters: new(10.0), ICC: new(0.0)}, participants: 200,
			wantDE: 1, wantICC: 0, wantOK: true,
		},
		{
			name:    "clusters of one person or fewer: no inflation",
			cluster: &model.Cluster{Clusters: new(400.0)}, participants: 200,
			wantDE: 1, wantICC: defaultICC, wantOK: true,
		},
		{name: "no cluster information", cluster: nil, participants: 200},
		{name: "a unit and nothing else", cluster: &model.Cluster{Unit: "village"}, participants: 200},
		{name: "a cluster count without the participants", cluster: &model.Cluster{Clusters: new(10.0)}},
		{name: "a cluster count of zero", cluster: &model.Cluster{Clusters: new(0.0)}, participants: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			de, icc, ok := designEffect(tc.cluster, tc.participants)

			assert.Equal(t, tc.wantOK, ok)
			assert.InDelta(t, tc.wantDE, de, 1e-9)
			assert.InDelta(t, tc.wantICC, icc, 1e-9)
		})
	}
}

// clusterTrial is a cluster-randomized trial with the given effect and cluster
// description.
func clusterTrial(ef *model.Effect, cl *model.Cluster) poolStudy {
	ef.Cluster = cl
	return poolStudy{doi: trialA, design: model.DesignClusterRCT, effect: ef}
}

// ofSize gives the study a size of its own, for an effect with no arm counts.
func ofSize(s poolStudy, n int) poolStudy {
	s.size = n
	return s
}

// asParallel makes the study a parallel-group trial.
func asParallel(s poolStudy) poolStudy {
	s.design = model.DesignRCT
	return s
}

// The participants and the events of each arm are divided by the design effect:
// the ratio stays, the interval widens, and the participants the imprecision
// rule reads are the effective ones.
func TestPoolClusterTrialCountsAreDividedByTheDesignEffect(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cluster      *model.Cluster
		wantDE       float64
		wantParticip int
	}{
		{"the reported ICC", &model.Cluster{Clusters: new(10.0), ICC: new(0.02)}, 1 + 19*0.02, 145},
		{"the default ICC", &model.Cluster{Clusters: new(10.0)}, 1 + 19*defaultICC, 103},
		{"a stated mean cluster size", &model.Cluster{MeanSize: new(25.0)}, 1 + 24*defaultICC, 91},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, logs := poolPipeline(t)

			pe := p.poolEffects(poolCorpus(clusterTrial(counts(10, 100, 20, 100), tc.cluster)))

			require.NotNil(t, pe)
			assert.Equal(t, model.PoolSingleStudy, pe.Method)
			assert.Equal(t, tc.wantParticip, pe.Participants)
			assert.InDelta(t, 0.5, pe.Estimate, 1e-9, "the ratio does not change")

			want, err := meta.LogRR(10/tc.wantDE, 100/tc.wantDE, 20/tc.wantDE, 100/tc.wantDE)
			require.NoError(t, err)

			plain, err := meta.LogRR(10, 100, 20, 100)
			require.NoError(t, err)

			z, err := meta.ZForLevel(meta.DefaultLevel)
			require.NoError(t, err)

			assert.Greater(t, want.SE, plain.SE)
			assert.InDelta(t, 0.5*math.Exp(z*want.SE), pe.CIHigh, 1e-3, "the interval is that of the effective sample")
			assert.Contains(t, logs.String(), "participants divided by the design effect")
		})
	}
}

func TestPoolClusterTrialMeansAreDividedToo(t *testing.T) {
	p, _ := poolPipeline(t)

	// 2 x 20 participants in 4 clusters: m = 10, DE = 1.45, 27.6 effective ones; the SD stays
	pe := p.poolEffects(poolCorpus(clusterTrial(
		means(2, unitPoints), &model.Cluster{Clusters: new(4.0), ICC: new(0.05)})))

	require.NotNil(t, pe)
	assert.Equal(t, model.MeasureMD, pe.Measure)
	assert.Equal(t, 28, pe.Participants)
	require.NotNil(t, pe.SD)
	assert.InDelta(t, 2, *pe.SD, 1e-9)
	assert.InDelta(t, 2, pe.Estimate, 1e-9)
}

// When the design effect cannot be computed the trial stays out: unadjusted
// numbers overstate the precision.
func TestPoolClusterTrialWithoutClusterInformationIsLeftOut(t *testing.T) {
	for name, cl := range map[string]*model.Cluster{
		"no cluster object":        nil,
		"only the unit":            {Unit: "village"},
		"an ICC but no clusters":   {ICC: new(0.02)},
		"an unusable cluster size": {Clusters: new(0.0)},
	} {
		t.Run(name, func(t *testing.T) {
			p, logs := poolPipeline(t)

			res := p.pool(poolCorpus(
				clusterTrial(counts(10, 100, 20, 100), cl),
				poolStudy{doi: trialB, design: model.DesignRCT, effect: counts(11, 100, 21, 100)},
			))

			require.NotNil(t, res.est)
			assert.Equal(t, []string{trialB}, res.est.Studies)
			assert.Equal(t, []string{trialA + " (cluster trial not adjusted for clustering)"}, res.est.LeftOut)
			assert.Contains(t, logs.String(), `reason="cluster trial not adjusted for clustering" count=1`)
		})
	}

	p, _ := poolPipeline(t)
	assert.Nil(t, p.poolEffects(poolCorpus(clusterTrial(counts(10, 100, 20, 100), nil))),
		"the only study is a cluster trial: no pool")
}

// An estimate whose analysis accounted for clustering is used as reported, and
// so are the participants of the study; another design is never divided.
func TestPoolClusterAdjustedAndOtherDesignsPassThrough(t *testing.T) {
	adjusted := &model.Cluster{Clusters: new(10.0), AdjustedForClustering: true}
	unadjustedReported := &model.Cluster{Unit: "clinic"}

	for _, tc := range []struct {
		name         string
		study        poolStudy
		wantParticip int
		wantMeasure  model.Measure
	}{
		{
			"an adjusted hazard ratio",
			ofSize(clusterTrial(reported("HR", 0.8, 0.6, 1.1), adjusted), 300),
			300, model.MeasureHR,
		},
		{
			"counts the critic marked as accounting for clustering",
			clusterTrial(counts(10, 100, 20, 100), adjusted),
			200, model.MeasureRR,
		},
		{
			"a reported estimate with no ICC or cluster counts is taken as reported",
			ofSize(clusterTrial(reported("HR", 0.8, 0.6, 1.1), unadjustedReported), 300),
			300, model.MeasureHR,
		},
		{
			"a parallel trial that carries a cluster object is not divided",
			asParallel(clusterTrial(counts(10, 100, 20, 100), &model.Cluster{Clusters: new(10.0)})),
			200, model.MeasureRR,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, logs := poolPipeline(t)

			pe := p.poolEffects(poolCorpus(tc.study))

			require.NotNil(t, pe)
			assert.Equal(t, tc.wantParticip, pe.Participants)
			assert.Equal(t, tc.wantMeasure, pe.Measure)
			assert.NotContains(t, logs.String(), "design effect")
		})
	}
}

// The effective sample size is what the imprecision rule reads: a cluster trial
// of 40 people with a design effect of 2 is a trial of 20.
func TestPoolClusterEffectiveSampleReachesTheImprecisionRule(t *testing.T) {
	p, _ := poolPipeline(t)

	// m = 20 and an ICC of 1/19 give a design effect of exactly 2
	st := poolCorpus(clusterTrial(means(2, unitPoints), &model.Cluster{MeanSize: new(20.0), ICC: new(1.0 / 19)}))

	pe := p.poolEffects(st)
	require.NotNil(t, pe)
	assert.Equal(t, 20, pe.Participants, "40 participants over a design effect of 2")

	in, err := gradeInput(pe)
	require.NoError(t, err)
	assert.Equal(t, 20, in.Participants)
}

// The events of a cluster trial are divided by the design effect like its
// participants (Handbook 23.1.4: both by the same one): 350 events in 2000
// people with a design effect of 3.45 are about 101 effective events.
func TestPoolClusterEventsAreDividedByTheDesignEffect(t *testing.T) {
	p, _ := poolPipeline(t)

	// m = 50 and the default ICC give a design effect of 1 + 49 x 0.05 = 3.45
	pe := p.poolEffects(poolCorpus(clusterTrial(counts(100, 1000, 250, 1000), &model.Cluster{MeanSize: new(50.0)})))

	require.NotNil(t, pe)
	assert.Equal(t, 580, pe.Participants)
	assert.Equal(t, 101, pe.Events, "350 events over 3.45")

	// the same trial analysed as individuals keeps its 350 events
	plain := p.poolEffects(poolCorpus(asParallel(clusterTrial(counts(100, 1000, 250, 1000),
		&model.Cluster{MeanSize: new(50.0)}))))
	require.NotNil(t, plain)
	assert.Equal(t, 350, plain.Events)

	// an analysis that accounts for clustering is taken as reported, events too
	adj := p.poolEffects(poolCorpus(clusterTrial(counts(100, 1000, 250, 1000),
		&model.Cluster{MeanSize: new(50.0), AdjustedForClustering: true})))
	require.NotNil(t, adj)
	assert.Equal(t, 350, adj.Events)
}

// A cluster trial that gives only a reported ratio has its participants taken
// as reported (clusterAdjusted leaves a reported estimate alone), so the events
// total the critic copied is taken as reported too, whatever the cluster data:
// both stay on the same scale.
func TestPoolClusterEventsTotalOfAReportedRatio(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cluster *model.Cluster
	}{
		{"design effect computable", &model.Cluster{MeanSize: new(50.0)}},
		{"the reported ICC", &model.Cluster{MeanSize: new(50.0), ICC: new(0.02)}},
		{"the analysis accounts for clustering", &model.Cluster{AdjustedForClustering: true}},
		{"no design effect", &model.Cluster{Unit: "school"}},
		{"no cluster object", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			trial := clusterTrial(withEvents(reported("HR", 0.8, 0.6, 1.1), 600), tc.cluster)

			pe := p.poolEffects(poolCorpus(ofSize(trial, 2000)))

			require.NotNil(t, pe)
			assert.Equal(t, 600, pe.Events, "as reported")
			assert.Equal(t, 2000, pe.Participants, "the participants are as reported too")
		})
	}
}
