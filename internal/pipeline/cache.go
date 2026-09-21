package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

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
	OALink    string    `json:"oa_link,omitempty"`
	Retracted bool      `json:"retracted,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Notes     []string  `json:"notes,omitempty"`
}

// LoadEnrichCache reads the cache and drops entries older than ttl.
// A missing file is an empty cache, not an error.
func LoadEnrichCache(path string, ttl time.Duration) EnrichCache {
	out := EnrichCache{}
	if path == "" {
		return out
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var c EnrichCache
	if json.Unmarshal(b, &c) != nil {
		return out // a corrupt cache is a cold cache
	}
	cutoff := time.Now().Add(-ttl)
	for doi, e := range c {
		if e.CheckedAt.After(cutoff) {
			out[doi] = e
		}
	}
	return out
}

// Save writes the cache (creating the topic directory when needed).
func (c EnrichCache) Save(path string) error {
	if path == "" || len(c) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
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
