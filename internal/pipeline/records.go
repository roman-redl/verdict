package pipeline

// The full PubMed record of a paper says two things the other enrichment
// sources do not: the trial registration numbers of its data banks and its
// publication types (an erratum or a comment is told from a study report by
// them). One efetch serves both.

import (
	"context"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// fillRecordDetails sets the trial registration numbers of every paper — from
// the abstract (a regular expression per registry) — and then looks the
// primary studies up in PubMed for what the abstract and the earlier sources
// left open: the data banks of a paper whose abstract names no registration
// number, and the publication types of a paper no source typed. A PubMed
// failure only costs the second source: the numbers and types found before
// stay. Reviews are not looked up — they never join a trial, and their design,
// not their type, keeps them out of a pool.
func (p *Pipeline) fillRecordDetails(ctx context.Context, enriched []model.EnrichedPaper) {
	byDOI := map[string][]int{} // normalized DOI -> indexes of the papers to look up

	var dois []string

	for i := range enriched {
		e := &enriched[i]
		e.TrialRegistrations = model.NormalizeRegistrations(
			append(e.TrialRegistrations, model.TrialRegistrations(e.Abstract)...))

		doi := model.NormalizeDOI(e.DOI)
		if doi == "" || p.clients.Records == nil || resolveDesign(e, model.Critique{}).IsSecondary() ||
			(len(e.TrialRegistrations) > 0 && len(e.PublicationTypes) > 0) {
			continue
		}

		if _, seen := byDOI[doi]; !seen {
			dois = append(dois, doi)
		}

		byDOI[doi] = append(byDOI[doi], i)
	}

	if len(dois) == 0 {
		return
	}

	found, err := p.clients.Records.Records(ctx, dois)
	if err != nil {
		p.log.Warn("pubmed unavailable: trial registrations taken from the abstracts, types from the other sources",
			"err", err)
	}

	filled, typed := 0, 0

	for doi, d := range found {
		ids := model.TrialRegistrations(strings.Join(d.Registrations, " "))

		for _, i := range byDOI[doi] {
			e := &enriched[i]
			had := len(e.TrialRegistrations)

			e.TrialRegistrations = model.NormalizeRegistrations(append(e.TrialRegistrations, ids...))
			e.PublicationTypes = model.MergePublicationTypes(e.PublicationTypes, d.PubTypes)

			if had == 0 && len(e.TrialRegistrations) > 0 {
				filled++
			}

			if len(d.PubTypes) > 0 {
				typed++
			}
		}
	}

	p.log.Info("pubmed records looked up", "papers", len(dois), "registrations_filled", filled, "typed", typed)
}
