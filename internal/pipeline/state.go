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
	// Pooled is the pooled effect of the direct studies, computed from the
	// critiques' extracted numbers right before the synthesis; nil when no
	// direct study reported usable numbers.
	Pooled *model.PooledEstimate `json:"pooled_estimate,omitempty"`
	// Imprecision is the GRADE rating computed from Pooled in the same step;
	// nil when there is no pooled estimate and in artifacts written before it.
	Imprecision *model.Imprecision `json:"imprecision,omitempty"`
	Report      *model.Report      `json:"report,omitempty"`
	// TextMode is the text mode of the run that produced the report; the
	// report lists the full texts not obtained only where full texts were
	// asked for. Empty in artifacts written before the field existed.
	TextMode TextMode `json:"text_mode,omitempty"`
	// HybridDone records that the hybrid deepening pass (the tail of the
	// critique stage) has already run for this state, even if it found no full
	// text to deepen; resume must not repeat it. False in a critique artifact
	// written by older code or by a run stopped before the pass: --resume then
	// deepens before the synthesis.
	HybridDone bool `json:"hybrid_done,omitempty"`
	// Cost is the run's LLM token accounting, as reported by the backend —
	// the audit trail for quota-economy decisions. Nil on backends that
	// report no usage and on pre-measurement artifacts.
	Cost *CostReport `json:"cost,omitempty"`
}

// CostReport splits the run's LLM usage by stage.
type CostReport struct {
	// QueryWriting is the discovery stage's one call that writes the PubMed
	// topic query; Screening its LLM screening of the candidate pool.
	QueryWriting *StageCost `json:"query_writing,omitempty"`
	Screening    *StageCost `json:"screening,omitempty"`
	Critique     *StageCost `json:"critique,omitempty"`
	Synthesis    *StageCost `json:"synthesis,omitempty"`
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
	if c == nil || (c.QueryWriting == nil && c.Screening == nil && c.Critique == nil && c.Synthesis == nil) {
		return ""
	}

	var parts []string

	// a fixed order: the footer must not change between renders of one state
	for _, stage := range []struct {
		name string
		cost *StageCost
	}{
		{"pubmed query", c.QueryWriting}, {"screening", c.Screening}, {"critique", c.Critique}, {"synthesis", c.Synthesis},
	} {
		if s := stage.cost; s != nil && (s.Calls > 0 || s.InputTokens > 0 || s.OutputTokens > 0) {
			parts = append(parts, fmt.Sprintf("%s: %d calls, %d in / %d out tokens",
				stage.name, s.Calls, s.InputTokens, s.OutputTokens))
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
// the hybrid deepening pass re-enters the critique stage (a resumed run adds
// its calls to the figures already saved).
func (s *State) critiqueCost() *StageCost {
	if s.Cost == nil {
		s.Cost = &CostReport{}
	}

	if s.Cost.Critique == nil {
		s.Cost.Critique = &StageCost{}
	}

	return s.Cost.Critique
}

// queryWritingCost is the PubMed query writer's counterpart of critiqueCost.
func (s *State) queryWritingCost() *StageCost {
	if s.Cost == nil {
		s.Cost = &CostReport{}
	}

	if s.Cost.QueryWriting == nil {
		s.Cost.QueryWriting = &StageCost{}
	}

	return s.Cost.QueryWriting
}

// screeningCost is the discovery stage's counterpart of critiqueCost.
func (s *State) screeningCost() *StageCost {
	if s.Cost == nil {
		s.Cost = &CostReport{}
	}

	if s.Cost.Screening == nil {
		s.Cost.Screening = &StageCost{}
	}

	return s.Cost.Screening
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
	Queries []string `json:"queries,omitempty"` // the Consensus queries as actually run
	// Sources are the channels the run used (consensus | pubmed | review_refs);
	// empty in artifacts written before the channels were selectable.
	Sources []model.Source `json:"sources,omitempty"`
	// NoScreen records that the LLM screening was off: every candidate counts
	// as included.
	NoScreen bool `json:"no_screen,omitempty"`
	// PubMedDecision records how the PubMed channel was settled (the mode and,
	// for auto, the review count of the Consensus pool); nil before it existed.
	PubMedDecision *PubMedDecision `json:"pubmed_decision,omitempty"`
	// PubMed records the PubMed channel's queries and counts; nil when the
	// channel did not run.
	PubMed *PubMedInfo `json:"pubmed,omitempty"`
	// PagesFetched counts successful Consensus pages.
	PagesFetched int `json:"pages_fetched,omitempty"`
	// APICalls is the Consensus quota spent: one call per 100 papers a page
	// returned, rounded up (NOT "uses billed": retries and rejected pages
	// differ). It is the figure the cost note quotes, not PagesFetched.
	APICalls int `json:"api_calls,omitempty"`
	// PageSize is the page size the server applied (the plan may cap the
	// requested one).
	PageSize     int  `json:"page_size,omitempty"`
	PapersFound  int  `json:"papers_found,omitempty"` // Consensus papers after DOI-drop + dedup, before screening
	DroppedNoDOI int  `json:"dropped_no_doi,omitempty"`
	Truncated    bool `json:"truncated,omitempty"` // page cap (or the plan) stopped the walk before the ranking ended
	// ReviewsMined is how many reviews of the pool had their reference list
	// read; RefsAdded how many referenced works entered the pool as new
	// candidates.
	ReviewsMined int `json:"reviews_mined,omitempty"`
	RefsAdded    int `json:"refs_added,omitempty"`
	// ExcludeDOIs is the --exclude-dois list as the run used it (normalized);
	// ExcludedDOIs are the DOIs of the candidates actually dropped by it (a
	// Cochrane version other than the one listed shows up here), ExcludedCount
	// their number.
	ExcludeDOIs   []string `json:"exclude_dois,omitempty"`
	ExcludedDOIs  []string `json:"excluded_dois,omitempty"`
	ExcludedCount int      `json:"excluded_count,omitempty"`
	// Pool is every candidate discovery considered, with the screening
	// decisions and whether it entered the corpus (Papers is the corpus).
	// Empty in artifacts written before the pool was kept and for DOI-list
	// and keyword runs.
	Pool []Candidate `json:"pool,omitempty"`
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
