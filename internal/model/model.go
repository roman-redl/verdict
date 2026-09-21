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
	StudyDesign  string   `json:"study_design,omitempty"` // discovery tags or the critic's estimate
	SampleSize   int      `json:"sample_size,omitempty"`  // filled at the critique stage (from the abstract)
	OALink       string   `json:"oa_link,omitempty"`
	CitedByCount int      `json:"cited_by_count,omitempty"`
	// Retracted is known already at discovery when the source was OpenAlex
	// (the enrichment stage then skips re-querying it).
	Retracted bool   `json:"retracted,omitempty"`
	Source    string `json:"source"` // openalex | manual
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
	// From Semantic Scholar (second source): a one-sentence summary when the
	// abstract is missing, and how many citations S2 marks influential.
	TLDR                 string `json:"tldr,omitempty"`
	InfluentialCitations int    `json:"influential_citations,omitempty"`
	// ContradictingDOIs is the Scite drill-down (paid scope): who cited this
	// paper as contradicted — grounds "who exactly is against".
	ContradictingDOIs []string `json:"contradicting_dois,omitempty"`
	// FullText is the extracted text of the open-access PDF (--fulltext);
	// the critic reads it instead of the abstract when present. The PDF
	// itself sits in <run>/fulltext/<doi>.pdf for the vision stage.
	FullText string `json:"full_text,omitempty"`
}

// Critique is the per-paper methodology review (the map step of the LLM stage).
type Critique struct {
	DOI                string `json:"doi"`
	Title              string `json:"title,omitempty"`
	DesignQuality      string `json:"design_quality"`  // high | moderate | low | unknown
	SampleAdequacy     string `json:"sample_adequacy"` // adequate | small | very_small | unclear | na
	SampleSizeReported int    `json:"sample_size_reported,omitempty"`
	Relevance          string `json:"relevance"` // direct | indirect | tangential | unknown
	// ResultDirection is where the study's OWN findings point relative to the
	// question: positive | null | negative | mixed | unclear.
	ResultDirection string   `json:"result_direction,omitempty"`
	Strengths       []string `json:"strengths,omitempty"`
	Concerns        []string `json:"concerns,omitempty"`
	Error           string   `json:"error,omitempty"`
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
	ConsensusStatus string      `json:"consensus_status,omitempty"` // broad_agreement | contested | emerging | no_consensus | insufficient_data
	Consensus       string      `json:"consensus,omitempty"`        // what the majority claims, with DOIs
	Matrix          []MatrixRow `json:"evidence_matrix"`
	Contradictions  []string    `json:"contradictions,omitempty"`
	Gaps            []string    `json:"gaps,omitempty"`
	Warnings        []string    `json:"warnings,omitempty"`
}

// NormalizeDOI canonicalizes a DOI: lowercase, without URL prefixes.
func NormalizeDOI(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	for _, p := range []string{"https://doi.org/", "http://doi.org/", "https://dx.doi.org/", "doi.org/", "doi:"} {
		s = strings.TrimPrefix(s, p)
	}
	return s
}
