package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/roman-redl/verdict/internal/model"
)

// readCache decodes a cache file into v. It reports false when there is
// nothing usable: a missing file is a normal cold start, an unreadable or
// corrupt one is logged (a cold cache costs API calls, so it must not be
// silent).
func readCache(path string, v any, log *slog.Logger) bool {
	//nolint:gosec // the cache path is built inside the package from runs/<topic>, not from external input
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Warn("cache unreadable, starting cold", "path", path, "err", err)
		}

		return false
	}

	if err := json.Unmarshal(b, v); err != nil {
		log.Warn("cache corrupt, starting cold", "path", path, "err", err)
		return false
	}

	return true
}

// EnrichCache memoizes per-DOI deterministic lookups (OpenAlex metadata,
// Crossref integrity notices) across runs of one topic: a challenge re-run
// re-checks the same corpus, and retraction flags rarely flip within days.
// Scite tallies and Semantic Scholar stay live — they are one batched
// request per run and change with time. The cache lives in the topic
// directory (runs/<topic>/cache.json) next to the runs themselves.
type EnrichCache map[string]CacheEntry

// CacheEntry is the cached state of one DOI's deterministic enrichment.
type CacheEntry struct {
	CheckedAt time.Time `json:"checked_at"`
	Abstract  string    `json:"abstract,omitempty"`
	// AbstractSource is set when the abstract came from the fallback source.
	AbstractSource string   `json:"abstract_source,omitempty"`
	OALink         string   `json:"oa_link,omitempty"`
	Retracted      bool     `json:"retracted,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	Notes          []string `json:"notes,omitempty"`
	// OALocations are the work's open-access locations (an entry written
	// before they were kept has none; the full-text fetch then has OALink).
	OALocations []model.Location `json:"oa_locations,omitempty"`
	// Types are the publication types the metadata sources gave the paper (an
	// entry written before they were kept has none; PubMed is asked then).
	Types []string `json:"types,omitempty"`
}

// LoadEnrichCache reads the cache and drops entries older than ttl as of now.
// A missing file is an empty cache, not an error.
func LoadEnrichCache(path string, ttl time.Duration, now time.Time, log *slog.Logger) EnrichCache {
	out := EnrichCache{}
	if path == "" {
		return out
	}

	var c EnrichCache
	if !readCache(path, &c, log) {
		return out
	}

	cutoff := now.Add(-ttl)
	for doi := range c {
		if c[doi].CheckedAt.After(cutoff) {
			out[doi] = c[doi]
		}
	}

	return out
}

// Save writes the cache (creating the topic directory when needed).
func (c EnrichCache) Save(path string) error {
	if path == "" || len(c) == 0 {
		return nil
	}

	return writeCache(path, c)
}

// writeCache stores a cache file, creating its directory.
func writeCache(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating the cache directory: %w", err)
	}

	return saveJSON(path, v)
}

// enrichCachePath places the cache next to the runs of a topic. No topic
// grouping (--topic/--run-dir absent) → no cache: runs would not share it.
func enrichCachePath(opts Options) string {
	switch {
	case opts.RunDir != "":
		return filepath.Join(filepath.Dir(opts.RunDir), "cache.json")
	case opts.Topic != "":
		return filepath.Join("runs", topicSlug(opts.Topic), "cache.json")
	}

	return ""
}

// CritiqueCache memoizes per-study critiques across runs of one topic: a
// critique is a pure function of (study data, question), so challenge
// re-runs (same corpus, same question verbatim) and crash resumes reuse it
// instead of paying the LLM again. Key: normalized DOI + "\x00" + question.
// The tradeoff is bounded by the TTL: citation tallies drift over time, and
// a months-old critique may lag the current enrichment.
type CritiqueCache map[string]CritiqueCacheEntry

// CritiqueCacheEntry is the cached critique of one (study, question) pair.
type CritiqueCacheEntry struct {
	CheckedAt time.Time      `json:"checked_at"`
	Critique  model.Critique `json:"critique"`
}

// LoadCritiqueCache reads the cache and drops entries older than ttl as of
// now (ttl <= 0 disables the cache entirely). A missing file is an empty cache.
func LoadCritiqueCache(path string, ttl time.Duration, now time.Time, log *slog.Logger) CritiqueCache {
	out := CritiqueCache{}
	if path == "" || ttl <= 0 {
		return out
	}

	var c CritiqueCache
	if !readCache(path, &c, log) {
		return out
	}

	cutoff := now.Add(-ttl)
	for k := range c {
		if c[k].CheckedAt.After(cutoff) {
			out[k] = c[k]
		}
	}

	return out
}

// Save writes the cache (creating the topic directory when needed).
func (c CritiqueCache) Save(path string) error {
	if path == "" || len(c) == 0 {
		return nil
	}

	return writeCache(path, c)
}

// critiqueCachePath places the cache next to the runs of a topic, mirroring
// enrichCachePath.
func critiqueCachePath(opts Options) string {
	switch {
	case opts.RunDir != "":
		return filepath.Join(filepath.Dir(opts.RunDir), "critique-cache.json")
	case opts.Topic != "":
		return filepath.Join("runs", topicSlug(opts.Topic), "critique-cache.json")
	}

	return ""
}
