package pipeline

import (
	"log/slog"
	"math"

	"github.com/roman-redl/verdict/internal/model"
)

// normalizeEffect makes the matching fields the critic wrote valid. A value
// outside the vocabulary or a number that cannot be one is dropped with a
// warning: a missing field reads as "unknown" later (the outcome as the same,
// the time point as unclear), which is not "different", so a garbled answer
// never excludes a study by itself.
func normalizeEffect(ef *model.Effect, doi string, log *slog.Logger) {
	if ef == nil {
		return
	}

	if ef.OutcomeMatch != "" {
		m, err := model.ParseOutcomeMatch(string(ef.OutcomeMatch))
		if err != nil {
			log.Warn("critic returned an unknown outcome match: treated as missing", "doi", doi, "err", err)
		}

		ef.OutcomeMatch = m
	}

	if ef.TimepointMatch != "" {
		m, err := model.ParseTimepointMatch(string(ef.TimepointMatch))
		if err != nil {
			log.Warn("critic returned an unknown timepoint match: treated as missing", "doi", doi, "err", err)
		}

		ef.TimepointMatch = m
	}

	if ef.Metric != "" {
		m, err := model.ParseScoreMetric(string(ef.Metric))
		if err != nil {
			log.Warn("critic returned an unknown score metric: treated as missing", "doi", doi, "err", err)
		}

		ef.Metric = m
	}

	if !positiveNumber(ef.TimepointWeeks) {
		ef.TimepointWeeks = nil
	}

	normalizeCluster(ef.Cluster, doi, log)
}

// normalizeCluster drops the cluster numbers that cannot be real ones: a
// count or a size at or below zero, an intracluster correlation outside 0-1.
func normalizeCluster(c *model.Cluster, doi string, log *slog.Logger) {
	if c == nil {
		return
	}

	if c.Clusters != nil && !positiveNumber(c.Clusters) {
		log.Warn("critic returned an unusable cluster count: ignored", "doi", doi, "clusters", *c.Clusters)
		c.Clusters = nil
	}

	if c.MeanSize != nil && !positiveNumber(c.MeanSize) {
		log.Warn("critic returned an unusable cluster size: ignored", "doi", doi, "mean_size", *c.MeanSize)
		c.MeanSize = nil
	}

	if c.ICC != nil && (math.IsNaN(*c.ICC) || *c.ICC < 0 || *c.ICC > 1) {
		log.Warn("critic returned an unusable intracluster correlation: ignored", "doi", doi, "icc", *c.ICC)
		c.ICC = nil
	}
}

// positiveNumber reports whether the pointer holds a finite number above zero.
func positiveNumber(v *float64) bool {
	return v != nil && *v > 0 && !math.IsInf(*v, 0)
}
