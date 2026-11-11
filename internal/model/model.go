// Package model holds the pipeline's domain types. They are serialized into
// stage artifacts, so the fields are stable and self-contained: an artifact
// can be read without the code.
package model

import (
	"strings"
	"time"
)

// Paper is a study as the pipeline sees it after the discovery stage.
type Paper struct {
	DOI          string   `json:"doi,omitempty"`
	Title        string   `json:"title"`
	Year         int      `json:"year,omitempty"`
	Authors      []string `json:"authors,omitempty"`
	Venue        string   `json:"venue,omitempty"`
	Abstract     string   `json:"abstract,omitempty"`
	StudyDesign  string   `json:"study_design,omitempty"` // the discovery tag only
	SampleSize   int      `json:"sample_size,omitempty"`  // filled at the critique stage (from the abstract)
	OALink       string   `json:"oa_link,omitempty"`
	CitedByCount int      `json:"cited_by_count,omitempty"`
	// OALocations are all the places the metadata source lists for the work
	// (PDF and landing URLs with the host type): the full-text fetch tries
	// them in turn, where OALink is only the first one. Empty in artifacts
	// written before the field existed; OALink then stands alone.
	OALocations []Location `json:"oa_locations,omitempty"`
	// SJRQuartile is the journal's best SCImago quartile at discovery
	// ("Q1".."Q4", from Consensus) — context for the reader, not a quality
	// verdict.
	SJRQuartile string `json:"sjr_quartile,omitempty"`
	// Takeaway is Consensus's AI-generated one-line summary of the study. It
	// is for the human reading the artifacts ONLY — never fed to the critic
	// or the judge as evidence: the pipeline judges papers from abstracts
	// and full texts (critiquePaper sends an explicit field allowlist).
	Takeaway string `json:"takeaway,omitempty"`
	// Retracted is known already at discovery when the source was OpenAlex
	// (the enrichment stage then skips re-querying it).
	Retracted bool   `json:"retracted,omitempty"`
	Source    Source `json:"source"`
}

// Source is where a paper entered the corpus.
type Source string

const (
	SourceOpenAlex  Source = "openalex"  // keyword discovery (mode A)
	SourceConsensus Source = "consensus" // semantic discovery (mode B)
	SourceManual    Source = "manual"    // a user-supplied DOI list (mode C)
	SourcePubMed    Source = "pubmed"    // the PubMed search of mode B
	// SourceReviewRefs marks a paper found in the reference list of a
	// systematic review or meta-analysis of the discovery pool.
	SourceReviewRefs Source = "review_refs"
)

// Work is a bibliographic record as a metadata source (OpenAlex) returns it:
// richer than a Paper, because citation chaining needs the source's own IDs
// and reference lists.
type Work struct {
	ID           string // the source's own identifier (an OpenAlex "W..." ID)
	DOI          string // as the source gives it, usually URL-shaped; see NormalizeDOI
	Title        string
	Year         int
	Authors      []string
	Venue        string
	Abstract     string
	OAURL        string // open-access URL of the work
	PDFURL       string // PDF URL of the primary location, when there is one
	Locations    []Location
	CitedByCount int
	Retracted    bool
	References   []string // IDs of the works this one cites
}

// ToPaper converts a work into a corpus paper. The DOI is taken from the
// work itself; the parameter is a fallback for DOI-based lookups.
func (w *Work) ToPaper(fallbackDOI string) Paper {
	doi := w.DOI
	if doi == "" {
		doi = fallbackDOI
	}

	link := w.OAURL
	if link == "" {
		link = w.PDFURL
	}

	return Paper{
		DOI:          NormalizeDOI(doi),
		Title:        w.Title,
		Year:         w.Year,
		Authors:      w.Authors,
		Venue:        w.Venue,
		Abstract:     w.Abstract,
		OALink:       link,
		OALocations:  w.Locations,
		CitedByCount: w.CitedByCount,
		Retracted:    w.Retracted,
		Source:       SourceOpenAlex,
	}
}

// Notice is an update notice a registry (Crossref) links to a work: a
// retraction, a correction, an expression of concern.
type Notice struct {
	DOI   string // the notice's own DOI
	Type  string // "retraction", "correction", ...
	Label string // human-readable type, e.g. "Retraction"
}

// IsRetraction reports whether the notice withdraws the work.
func (n Notice) IsRetraction() bool { return n.Type == "retraction" }

// ScholarRecord is what the second metadata source (Semantic Scholar) adds
// to a paper: summaries, an open-access PDF and influential-citation counts.
type ScholarRecord struct {
	Abstract             string
	TLDR                 string // one-sentence summary
	OAPDF                string // legal open-access PDF URL
	InfluentialCitations int
}

// SearchPage is one page of semantic search results.
type SearchPage struct {
	Papers []Paper
	IsEnd  bool // the ranking is exhausted
	// PageSize is the size the server applied: the plan may cap the size the
	// request asked for. Zero when the source did not say.
	PageSize int
	// PageSizeAsked is the size the request carried; zero when the source's
	// own default was used.
	PageSizeAsked int
}

// CitationTally is a paper's Scite smart-citation breakdown.
type CitationTally struct {
	DOI           string `json:"doi"`
	Total         int    `json:"total"`
	Supporting    int    `json:"supporting"`
	Contradicting int    `json:"contradicting"`
	Mentioning    int    `json:"mentioning"`
	Unclassified  int    `json:"unclassified"`
}

// RedFlags holds deterministic integrity checks (no LLM involved).
type RedFlags struct {
	Retracted bool     `json:"retracted,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Notes     []string `json:"notes,omitempty"`
}

// EnrichedPaper is everything collected about a paper at the enrichment stage.
// Tally == nil means "Scite knows nothing about this paper" — that is a signal
// (young or very niche study), not an error.
type EnrichedPaper struct {
	Paper
	Tally  *CitationTally `json:"tally,omitempty"`
	Flags  RedFlags       `json:"flags"`
	Errors []string       `json:"errors,omitempty"` // which sources failed for this paper
	// AbstractSource names the fallback source the abstract came from; empty
	// when the primary sources (OpenAlex, Semantic Scholar, discovery) had it.
	AbstractSource string `json:"abstract_source,omitempty"`
	// From Semantic Scholar (second source): a one-sentence summary when the
	// abstract is missing, and how many citations S2 marks influential.
	TLDR                 string `json:"tldr,omitempty"`
	InfluentialCitations int    `json:"influential_citations,omitempty"`
	// ContradictingDOIs is the Scite drill-down (paid scope): who cited this
	// paper as contradicted — grounds "who exactly is against".
	ContradictingDOIs []string `json:"contradicting_dois,omitempty"`
	// FullText is the text of the paper's open-access full text (Europe PMC
	// XML or an extracted PDF) in full text mode, the hybrid deepening pass,
	// or from a file dropped into fulltext/. The critic reads it instead of
	// the abstract when present. A PDF itself sits in <run>/fulltext/<doi>.pdf
	// for the vision stage.
	FullText string `json:"full_text,omitempty"`
	// FullTextSource names where FullText came from: "europepmc" (JATS XML),
	// "pmc" (NCBI efetch XML), "pdf" or "jina:<host>" (a page read by the
	// Jina Reader). Empty in artifacts written before the field existed.
	FullTextSource string `json:"full_text_source,omitempty"`
	// TrialRegistrations are the trial registration numbers of the paper
	// (NCT..., ISRCTN..., normalized), from its abstract and from the PubMed
	// record's data banks. Papers that share one report the same trial; use
	// TrialIDs to read them, which also covers artifacts written before the
	// field existed.
	TrialRegistrations []string `json:"trial_registrations,omitempty"`
}

// Critique is the per-paper methodology review (the map step of the LLM stage).
type Critique struct {
	DOI                string `json:"doi"`
	Title              string `json:"title,omitempty"`
	DesignQuality      string `json:"design_quality"`  // high | moderate | low | unknown
	SampleAdequacy     string `json:"sample_adequacy"` // adequate | small | very_small | unclear | na
	SampleSizeReported int    `json:"sample_size_reported,omitempty"`
	Relevance          string `json:"relevance"` // direct | indirect | tangential | unknown
	// StudyDesign is the design of THIS paper as its text states it. Empty in
	// critiques written before the field existed; the pipeline then falls back
	// to the discovery tag and the title (pipeline.resolveDesign).
	StudyDesign StudyDesign `json:"study_design,omitempty"`
	// ResultDirection is where the study's OWN findings point relative to the
	// question: positive | null | negative | mixed | unclear.
	ResultDirection string   `json:"result_direction,omitempty"`
	Strengths       []string `json:"strengths,omitempty"`
	Concerns        []string `json:"concerns,omitempty"`
	// Effect holds the numbers the text reports for the question's outcome,
	// extracted by the critic and pooled by the pipeline; nil when the text
	// has none.
	Effect *Effect `json:"effect,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// MatrixRow is one row of the evidence matrix in the final report. All
// numeric and enum fields are assembled deterministically by the pipeline
// from the stage data; only Note comes from the synthesis LLM.
type MatrixRow struct {
	DOI            string `json:"doi"`
	Title          string `json:"title"`
	Year           int    `json:"year"`
	Design         string `json:"design"`
	SampleSize     int    `json:"sample_size"`
	Supporting     int    `json:"supporting"`
	Contradicting  int    `json:"contradicting"`
	TotalCitations int    `json:"total_citations"`     // Scite total when known, else OpenAlex cited_by
	Direction      string `json:"direction,omitempty"` // positive | null | negative | mixed | unclear
	DesignQuality  string `json:"design_quality"`
	Relevance      string `json:"relevance"`
	Note           string `json:"note"`
}

// VisionNote is an agent-produced chart critique for one paper: figure
// manipulations (truncated axes, label mismatches, cherry-picked panels)
// are invisible to text models, so the agent renders the PDF pages and
// analyzes them through the vision MCP, writing 3b-vision.json; synthesis
// consumes the notes as data.
type VisionNote struct {
	DOI      string   `json:"doi"`
	Findings []string `json:"findings"`
}

// Report is the final artifact of the reduce step.
type Report struct {
	Question         string    `json:"question"`
	GeneratedAt      time.Time `json:"generated_at"`
	Verdict          string    `json:"verdict"` // strong | moderate | weak | insufficient
	VerdictRationale string    `json:"verdict_rationale"`
	// Consensus is about the DIRECTION the field leans (the verdict is about
	// the strength of evidence).
	// broad_agreement | contested | emerging | no_consensus | insufficient_data
	ConsensusStatus string `json:"consensus_status,omitempty"`
	Consensus       string `json:"consensus,omitempty"` // what the majority claims, with DOIs
	// EffectDirection is the direction of the effect the evidence states
	// relative to the claim; empty in reports written before the field existed.
	EffectDirection EffectDirection `json:"effect_direction,omitempty"`
	// PooledEstimate is the pipeline's pooled effect of the direct studies;
	// nil when no direct study reported usable numbers.
	PooledEstimate *PooledEstimate `json:"pooled_estimate,omitempty"`
	// Imprecision is the GRADE imprecision rating computed from the pooled
	// estimate; nil when there is none or in reports written before the field.
	Imprecision    *Imprecision `json:"imprecision,omitempty"`
	Matrix         []MatrixRow  `json:"evidence_matrix"`
	Contradictions []string     `json:"contradictions,omitempty"`
	Gaps           []string     `json:"gaps,omitempty"`
	Warnings       []string     `json:"warnings,omitempty"`
}

var doiPrefixes = [...]string{"https://doi.org/", "http://doi.org/", "https://dx.doi.org/", "doi.org/", "doi:"}

// NormalizeDOI canonicalizes a DOI: lowercase, trimmed, without URL or "doi:"
// prefixes (however many are stacked). Normalizing twice changes nothing.
func NormalizeDOI(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))

	for {
		stripped := s
		for _, p := range doiPrefixes {
			stripped = strings.TrimPrefix(stripped, p)
		}

		stripped = strings.TrimSpace(stripped)
		if stripped == s {
			return s
		}

		s = stripped
	}
}
