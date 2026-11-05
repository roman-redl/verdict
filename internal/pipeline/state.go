package pipeline

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/roman-redl/verdict/internal/model"
)

// State is the full checkpoint; it is serialized whole after every stage.
type State struct {
	Question      string                `json:"question"`
	ChallengeNote string                `json:"challenge_note,omitempty"`
	Discovery     *DiscoveryInfo        `json:"discovery,omitempty"`
	Warnings      []string              `json:"warnings,omitempty"`
	Papers        []model.Paper         `json:"papers"`
	Enriched      []model.EnrichedPaper `json:"enriched,omitempty"`
	Critiques     []model.Critique      `json:"critiques,omitempty"`
	VisionNotes   []model.VisionNote    `json:"vision_notes,omitempty"`
	Report        *model.Report         `json:"report,omitempty"`
	// HybridDone records that the hybrid deepening pass has already run for
	// this state (resume must not repeat it).
	HybridDone bool `json:"hybrid_done,omitempty"`
	// Cost is the run's LLM token accounting, as reported by the backend —
	// the audit trail for quota-economy decisions. Nil on backends that
	// report no usage and on pre-measurement artifacts.
	Cost *CostReport `json:"cost,omitempty"`
}

// CostReport splits the run's LLM usage by stage.
type CostReport struct {
	Critique  *StageCost `json:"critique,omitempty"`
	Synthesis *StageCost `json:"synthesis,omitempty"`
}

// StageCost is the accumulated usage of one LLM stage.
type StageCost struct {
	Calls        int `json:"calls"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (s *StageCost) add(u Usage) {
	calls := u.Calls
	if calls == 0 {
		calls = 1 // a backend that reports no usage still made this one call
	}

	s.Calls += calls
	s.InputTokens += u.InputTokens
	s.OutputTokens += u.OutputTokens
}

// Markdown renders the usage footer for report.md.
func (c *CostReport) Markdown() string {
	if c == nil || (c.Critique == nil && c.Synthesis == nil) {
		return ""
	}

	var parts []string

	for name, s := range map[string]*StageCost{"critique": c.Critique, "synthesis": c.Synthesis} {
		if s != nil && (s.Calls > 0 || s.InputTokens > 0 || s.OutputTokens > 0) {
			parts = append(parts, fmt.Sprintf("%s: %d calls, %d in / %d out tokens",
				name, s.Calls, s.InputTokens, s.OutputTokens))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return "LLM usage — " + strings.Join(parts, "; ")
}

// PaperCount is the corpus size without double counting: Enriched and Papers
// hold the same studies after the enrichment stage.
func (s *State) PaperCount() int {
	if n := len(s.Enriched); n > 0 {
		return n
	}

	return len(s.Papers)
}

// critiqueCost and synthesisCost return the accumulated usage of a stage,
// creating the entry on first use. Accumulating (not overwriting) matters:
// the hybrid deepening pass re-enters both stages.
func (s *State) critiqueCost() *StageCost {
	if s.Cost == nil {
		s.Cost = &CostReport{}
	}

	if s.Cost.Critique == nil {
		s.Cost.Critique = &StageCost{}
	}

	return s.Cost.Critique
}

func (s *State) synthesisCost() *StageCost {
	if s.Cost == nil {
		s.Cost = &CostReport{}
	}

	if s.Cost.Synthesis == nil {
		s.Cost.Synthesis = &StageCost{}
	}

	return s.Cost.Synthesis
}

// critiquesFor pairs each enriched paper with its critique, index-aligned.
// Critiques are created one per paper in the same order, so the index zip is
// exact; the DOI fallback covers hand-edited artifacts, and papers without a
// DOI never collide on an empty key.
func critiquesFor(st *State) []model.Critique {
	out := make([]model.Critique, len(st.Enriched))
	if len(st.Critiques) == len(st.Enriched) {
		copy(out, st.Critiques)
		return out
	}

	byDOI := map[string]model.Critique{}

	for i := range st.Critiques {
		c := st.Critiques[i]
		if d := model.NormalizeDOI(c.DOI); d != "" {
			byDOI[d] = c
		}
	}

	for i := range st.Enriched {
		if d := model.NormalizeDOI(st.Enriched[i].DOI); d != "" {
			out[i] = byDOI[d]
		}
	}

	return out
}

// DiscoveryInfo records where the corpus came from — the audit trail for
// "which engine, which queries, at what cost" (nil on pre-mode-B artifacts).
type DiscoveryInfo struct {
	Engine  Engine   `json:"engine"`            // consensus | openalex | dois
	Queries []string `json:"queries,omitempty"` // the queries as actually run
	// PagesFetched counts successful Consensus pages — each plausibly one
	// API use spent (NOT "uses billed": retries and rejected pages differ).
	PagesFetched int  `json:"pages_fetched,omitempty"`
	PapersFound  int  `json:"papers_found,omitempty"` // after DOI-drop + dedup, before the --max-papers cut
	DroppedNoDOI int  `json:"dropped_no_doi,omitempty"`
	Truncated    bool `json:"truncated,omitempty"` // page cap hit before the ranking ended
}

// RunState pairs a run directory name with its loaded state.
type RunState struct {
	Name  string
	State *State
}

// Stage artifacts, newest stage first: every artifact is a full snapshot, so
// the freshest one is the run's state.
var stageFiles = []string{"4-synthesis.json", "3-critique.json", "2-enrichment.json", "1-discovery.json"}

var errNoArtifacts = errors.New("no stage artifacts")

// LoadState restores the state from the freshest artifact in a run directory.
func LoadState(dir string) (*State, error) {
	for _, name := range stageFiles {
		p := filepath.Join(dir, name)
		if fileExists(p) {
			var st State
			if err := loadJSON(p, &st); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}

			return &st, nil
		}
	}

	return nil, fmt.Errorf("%w in %s", errNoArtifacts, dir)
}

// ListRuns returns the run directories under dir, newest first, skipping
// "archive" and dot-directories. Runs live grouped by topic: runs/<topic>/<run>.
func ListRuns(dir string, log *slog.Logger) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	type item struct {
		name    string
		modTime time.Time
	}

	var items []item

	for _, e := range entries {
		if !e.IsDir() || e.Name() == "archive" || strings.HasPrefix(e.Name(), ".") {
			continue
		}

		info, err := e.Info()
		if err != nil {
			log.Warn("run directory skipped: cannot stat it", "dir", filepath.Join(dir, e.Name()), "err", err)
			continue
		}

		items = append(items, item{e.Name(), info.ModTime()})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].modTime.After(items[j].modTime) })

	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.name
	}

	return names, nil
}

// LoadRuns loads a topic directory (all runs inside, newest first) or a
// single run directory. A directory holding stage artifacts directly is a
// single run; otherwise every run subdirectory is loaded, broken ones skipped.
func LoadRuns(dir string, log *slog.Logger) ([]RunState, error) {
	if fileExists(filepath.Join(dir, "1-discovery.json")) {
		st, err := LoadState(dir)
		if err != nil {
			return nil, err
		}

		return []RunState{{Name: filepath.Base(dir), State: st}}, nil
	}

	names, err := ListRuns(dir, log)
	if err != nil {
		return nil, fmt.Errorf("%s is neither a run nor a topic directory: %w", dir, err)
	}

	var out []RunState

	for _, n := range names {
		st, err := LoadState(filepath.Join(dir, n))
		if err != nil {
			// a subdirectory without artifacts is not a run; a corrupt run is worth a warning
			if !errors.Is(err, errNoArtifacts) {
				log.Warn("run skipped: unreadable artifacts", "run", n, "err", err)
			}

			continue
		}

		out = append(out, RunState{Name: n, State: st})
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no loadable runs under %s", dir)
	}

	return out, nil
}
