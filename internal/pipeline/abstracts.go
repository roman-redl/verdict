package pipeline

// The abstract fallback of the enrichment stage. OpenAlex withholds the
// abstracts of publisher-restricted works (every Elsevier DOI, for one), and
// a critic that sees only a title judges nothing; Europe PMC has them for the
// biomedical literature. The pass runs once per stage over the whole corpus —
// after the primary sources and the cache merge — so cached entries with an
// empty abstract get the fallback too and the result lands in the cache.

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/roman-redl/verdict/internal/model"
)

// abstractSourceEuropePMC is recorded as the source of a filled abstract.
const abstractSourceEuropePMC = "europepmc"

// fillAbstracts requests, in one call, the abstracts of the papers that still
// have none, and writes them into the papers and into the cache entries of
// their DOIs. A failure is not fatal: the abstracts received before it are
// kept and the failure comes back as a run warning. It returns how many
// papers got an abstract.
func (p *Pipeline) fillAbstracts(
	ctx context.Context, enriched []model.EnrichedPaper, cache EnrichCache,
) (filled int, warnings []string) {
	if p.clients.Abstracts == nil {
		return 0, nil
	}

	byDOI := map[string][]int{} // normalized DOI -> indexes of the papers without an abstract

	var dois []string

	for i := range enriched {
		doi := model.NormalizeDOI(enriched[i].DOI)
		if enriched[i].Abstract != "" || doi == "" {
			continue
		}

		if _, seen := byDOI[doi]; !seen {
			dois = append(dois, doi)
		}

		byDOI[doi] = append(byDOI[doi], i)
	}

	if len(dois) == 0 {
		return 0, nil
	}

	found, err := p.clients.Abstracts.Abstracts(ctx, dois)
	if err != nil {
		p.log.Warn("europe pmc unavailable: missing abstracts not filled", "err", err)

		warnings = []string{"Europe PMC unavailable, some abstracts are missing: " + persistedErr(err)}
	}

	for _, doi := range dois {
		text := found[doi]
		if text == "" {
			continue
		}

		for _, i := range byDOI[doi] {
			enriched[i].Abstract = text
			enriched[i].AbstractSource = abstractSourceEuropePMC
			filled++
		}

		if entry, ok := cache[doi]; ok {
			entry.Abstract = text
			entry.AbstractSource = abstractSourceEuropePMC
			cache[doi] = entry
		}
	}

	p.log.Info("abstracts filled from europe pmc", "missing", len(dois), "filled", filled)

	return filled, warnings
}

// refillLoadedAbstracts gives an enrichment artifact loaded by --resume the
// fallback it may lack (an artifact written before the fallback existed, or
// after a Europe PMC outage). When the critique artifact is on disk the run
// is left as it is: those critiques were made on the old abstracts, and the
// loaded state is replaced by the critique artifact anyway. The artifact and
// the cache are saved again when something was filled.
func (p *Pipeline) refillLoadedAbstracts(ctx context.Context, rp runParams, st *State, artifact string) error {
	if fileExists(filepath.Join(rp.dir, stageCritique+".json")) {
		return nil
	}

	cachePath := enrichCachePath(rp.opts)
	cache := LoadEnrichCache(cachePath, p.set.EnrichCacheTTL, p.now(), p.log)

	filled, warns := p.fillAbstracts(ctx, st.Enriched, cache)
	st.Warnings = append(st.Warnings, warns...)

	if filled == 0 {
		return nil
	}

	if err := cache.Save(cachePath); err != nil {
		p.log.Warn("enrichment cache not saved", "err", err)
	}

	if err := saveJSON(artifact, st); err != nil {
		return fmt.Errorf("saving %s: %w", stageEnrichment, err)
	}

	return nil
}
