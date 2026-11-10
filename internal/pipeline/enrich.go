package pipeline

// Stage 2: enrichment. Batched Scite tallies and a Semantic Scholar request,
// then per paper: OpenAlex (retractions, open-access links, abstracts) and
// Crossref update notices (the Retraction Watch signal, typically ahead of
// OpenAlex), full texts, PubPeer commentary and the Scite drill-down; at the
// end one Europe PMC request fills the abstracts still missing. One
// source failing for one paper does not kill the stage — it is recorded in
// Errors/Warnings and synthesis proceeds on partial data.

import (
	"cmp"
	"context"
	"fmt"
	"maps"

	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/internal/model"
)

// Reasons recorded on a retraction flag.
const (
	reasonOpenAlexRetracted = "OpenAlex: is_retracted"
	reasonCrossrefRetracted = "Crossref/retraction-watch: retraction notice "
)

// tallyBatchSize is how many DOIs one Scite tallies request carries (the API
// limit is 500).
const tallyBatchSize = 100

// enrichBatch is what the batched lookups produced for the whole corpus,
// shared read-only by the per-paper workers.
type enrichBatch struct {
	dir      string
	tallies  map[string]model.CitationTally
	scholar  map[string]model.ScholarRecord
	cache    EnrichCache
	download bool // OA PDFs are downloaded at this stage (full text mode only)
}

// cacheUpdate is the cache entry a worker produced for a DOI; the zero value
// means "nothing to store".
type cacheUpdate struct {
	doi   string
	entry CacheEntry
}

func (p *Pipeline) enrich(ctx context.Context, rp runParams, st *State) error {
	tallies, warns := p.fetchTallies(ctx, st.Papers)
	st.Warnings = append(st.Warnings, warns...)

	cachePath := enrichCachePath(rp.opts)
	batch := &enrichBatch{
		dir:     rp.dir,
		tallies: tallies,
		scholar: p.fetchScholar(ctx, st.Papers),
		cache:   LoadEnrichCache(cachePath, p.set.EnrichCacheTTL, p.now(), p.log),
		// OA downloads happen in full mode only (hybrid fetches its full texts
		// at the end of the critique stage, lean never); a pre-placed PDF is read in
		// every mode — see fetchFullText.
		download: rp.opts.TextMode == TextFull,
	}

	enriched := make([]model.EnrichedPaper, len(st.Papers))
	updates := make([]cacheUpdate, len(st.Papers))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(orDefault(rp.opts.Concurrency, p.set.Concurrency))

	for i := range st.Papers {
		g.Go(func() error {
			enriched[i], updates[i] = p.enrichPaper(gctx, batch, st.Papers[i])
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("enriching the papers: %w", err)
	}

	for i := range updates {
		if updates[i].doi != "" {
			batch.cache[updates[i].doi] = updates[i].entry
		}
	}

	// After the cache merge, so the filled abstracts reach the entries of
	// fresh papers and of cached ones alike.
	_, warns = p.fillAbstracts(ctx, enriched, batch.cache)
	st.Warnings = append(st.Warnings, warns...)

	if err := batch.cache.Save(cachePath); err != nil {
		p.log.Warn("enrichment cache not saved", "err", err)
	}

	st.Enriched = enriched

	return nil
}

// enrichPaper gathers everything the sources know about one paper. A fresh
// cached entry replaces the per-DOI OpenAlex + Crossref lookups entirely
// (challenge re-runs share the topic corpus).
func (p *Pipeline) enrichPaper(
	ctx context.Context, b *enrichBatch, pp model.Paper,
) (model.EnrichedPaper, cacheUpdate) {
	e := model.EnrichedPaper{Paper: pp}
	doi := model.NormalizeDOI(pp.DOI)

	if t, ok := b.tallies[doi]; ok {
		e.Tally = &t
	}

	if cached, ok := b.cache[doi]; ok {
		applyCached(&e, cached)
		// The full-text pickup must not depend on the cache: a pre-placed PDF
		// is always read, and full mode still downloads.
		p.attachFullText(ctx, b, &e)

		return e, cacheUpdate{}
	}

	p.checkIntegrity(ctx, &e)

	if r, ok := b.scholar[doi]; ok {
		applyScholar(&e, r)
	}

	p.attachFullText(ctx, b, &e)
	p.attachCommentary(ctx, &e)
	p.drillDownContradictions(ctx, &e)

	return e, cacheUpdate{doi: doi, entry: CacheEntry{
		CheckedAt:      p.now(),
		Abstract:       e.Abstract,
		AbstractSource: e.AbstractSource,
		OALink:         e.OALink,
		OALocations:    e.OALocations,
		Retracted:      e.Flags.Retracted,
		Reason:         e.Flags.Reason,
		Notes:          e.Flags.Notes,
	}}
}

func applyCached(e *model.EnrichedPaper, c CacheEntry) {
	if c.Retracted {
		e.Flags.Retracted = true
		e.Flags.Reason = c.Reason
	}

	if e.Abstract == "" {
		e.Abstract = c.Abstract
		e.AbstractSource = c.AbstractSource
	}

	e.OALink = cmp.Or(e.OALink, c.OALink)

	if len(e.OALocations) == 0 {
		e.OALocations = c.OALocations
	}

	e.Flags.Notes = append(e.Flags.Notes, c.Notes...)
}

// checkIntegrity runs the two retraction sources. Papers discovered through
// OpenAlex (keyword discovery or the reference lists of reviews) already carry
// everything a ByDOI lookup returns (abstract, OA link, retraction flag) —
// re-querying would be an exact duplicate. A manual stub deserves one lookup
// in case the discovery one failed transiently.
func (p *Pipeline) checkIntegrity(ctx context.Context, e *model.EnrichedPaper) {
	if e.Retracted {
		e.Flags.Retracted = true
		e.Flags.Reason = reasonOpenAlexRetracted
	}

	if e.DOI == "" {
		return
	}

	doi := model.NormalizeDOI(e.DOI)

	if p.clients.Works != nil && e.Source != model.SourceOpenAlex && e.Source != model.SourceReviewRefs {
		w, err := p.clients.Works.ByDOI(ctx, doi)

		switch {
		case err != nil:
			p.sourceFailed(e, "openalex", err)
		case w != nil:
			if w.Retracted {
				e.Flags.Retracted = true
				e.Flags.Reason = reasonOpenAlexRetracted
			}

			e.OALink = cmp.Or(e.OALink, w.OAURL)
			e.Abstract = cmp.Or(e.Abstract, w.Abstract)

			if len(e.OALocations) == 0 {
				e.OALocations = w.Locations
			}
		}
	}
	// Crossref is the second, independent integrity source: the Retraction
	// Watch Database lives there, with corrections and expressions of concern
	// alongside retractions.
	if p.clients.Notices != nil {
		notices, err := p.clients.Notices.Updates(ctx, doi)
		if err != nil {
			p.sourceFailed(e, "crossref", err)
			return
		}

		applyNotices(e, notices)
	}
}

// applyNotices turns update notices into flags and notes.
func applyNotices(e *model.EnrichedPaper, notices []model.Notice) {
	for _, n := range notices {
		switch {
		case n.IsRetraction():
			e.Flags.Retracted = true
			e.Flags.Reason = reasonCrossrefRetracted + n.DOI
		case n.Type == "correction":
			e.Flags.Notes = append(e.Flags.Notes, "correction notice "+n.DOI)
		default:
			e.Flags.Notes = append(e.Flags.Notes, cmp.Or(n.Label, n.Type)+" notice "+n.DOI)
		}
	}
}

// applyScholar adds what Semantic Scholar knows and OpenAlex missed:
// abstracts (the TLDR as a last resort) and OA PDF links; influential
// citations feed the judge.
func applyScholar(e *model.EnrichedPaper, r model.ScholarRecord) {
	e.Abstract = cmp.Or(e.Abstract, r.Abstract)
	e.OALink = cmp.Or(e.OALink, r.OAPDF)
	e.TLDR = r.TLDR
	e.InfluentialCitations = r.InfluentialCitations
}

// attachFullText supplies the paper's full text: a PDF pre-placed in the run
// directory is always used; in full mode the OA full text is fetched (the
// sources and their order: fetchFullText). The critic then reads the whole
// paper; a downloaded PDF stays for the vision stage.
func (p *Pipeline) attachFullText(ctx context.Context, b *enrichBatch, e *model.EnrichedPaper) {
	if txt, src := p.fetchFullText(ctx, b.dir, e, b.download); txt != "" {
		e.FullText, e.FullTextSource = txt, src
	}
}

// attachCommentary adds the PubPeer commentary as a soft signal (the API is
// behind a developer key).
func (p *Pipeline) attachCommentary(ctx context.Context, e *model.EnrichedPaper) {
	if p.clients.Commentary == nil || !p.clients.Commentary.HasKey() || e.DOI == "" {
		return
	}

	note, err := p.clients.Commentary.Note(ctx, model.NormalizeDOI(e.DOI))
	if err != nil {
		p.log.Warn("pubpeer check failed", "doi", e.DOI, "err", err)
		return
	}

	if note != "" {
		e.Flags.Notes = append(e.Flags.Notes, note)
	}
}

// drillDownContradictions asks Scite who exactly cites a contradicted paper
// as refuted (paid scope, best-effort).
func (p *Pipeline) drillDownContradictions(ctx context.Context, e *model.EnrichedPaper) {
	if p.clients.Citations == nil || !p.clients.Citations.HasKey() ||
		e.Tally == nil || e.Tally.Contradicting == 0 || e.DOI == "" {
		return
	}

	srcs, err := p.clients.Citations.ContradictingSources(ctx, model.NormalizeDOI(e.DOI))
	if err != nil {
		p.log.Warn("scite drill-down failed", "doi", e.DOI, "err", err)
		return
	}

	if len(srcs) > 0 {
		e.ContradictingDOIs = srcs
	}
}

// fetchTallies asks Scite for the corpus' citation tallies in batches; a
// Scite failure is not fatal.
func (p *Pipeline) fetchTallies(ctx context.Context, papers []model.Paper) (
	tallies map[string]model.CitationTally, warnings []string,
) {
	tallies = map[string]model.CitationTally{}
	if p.clients.Citations == nil {
		return tallies, []string{"Scite client is not initialized"}
	}

	for _, batch := range chunks(normalizedDOIs(papers), tallyBatchSize) {
		m, err := p.clients.Citations.Tallies(ctx, batch)
		if err != nil {
			p.log.Warn("scite unavailable: tallies skipped", "err", err)
			return tallies, []string{"Scite unavailable, citation analytics incomplete: " + persistedErr(err)}
		}

		maps.Copy(tallies, m)
	}

	return tallies, nil
}

// fetchScholar makes one batched Semantic Scholar request per run (the
// anonymous rate limits are tight); a failure is not fatal — OpenAlex
// remains the primary metadata source.
func (p *Pipeline) fetchScholar(ctx context.Context, papers []model.Paper) map[string]model.ScholarRecord {
	if p.clients.Scholar == nil {
		return map[string]model.ScholarRecord{}
	}

	records, err := p.clients.Scholar.Lookup(ctx, normalizedDOIs(papers))
	if err != nil {
		p.log.Warn("semantic scholar unavailable: enrichment continues without it", "err", err)
		return map[string]model.ScholarRecord{}
	}

	return records
}

// normalizedDOIs lists the papers' normalized DOIs, skipping papers without one.
func normalizedDOIs(papers []model.Paper) []string {
	var dois []string

	for i := range papers {
		if d := model.NormalizeDOI(papers[i].DOI); d != "" {
			dois = append(dois, d)
		}
	}

	return dois
}
