package model

// EvidenceBase is the summary of what the verdict rests on, computed by the
// pipeline from the stage data (never by the synthesis): how many papers were
// reviewed, how many answer the question directly and what kind of studies
// they are, how large, and what was pooled. A reader sees at a glance whether
// a strong or a weak verdict stands on one small trial or on several large ones.
type EvidenceBase struct {
	// Reviewed is the number of papers the critic assessed. It is the sum of
	// Direct, RepeatReports, Retracted and Indirect; the papers whose critique
	// failed are not in it (NotAssessed).
	Reviewed int `json:"reviewed"`
	// Direct is the number of studies that answer the question directly, one
	// per trial: the reports of one trial (a shared registration number) count
	// once. It is the sum of the groups below.
	Direct int `json:"direct"`
	// RepeatReports is how many direct reports were folded into a trial
	// already counted.
	RepeatReports int `json:"repeat_reports,omitempty"`
	// Retracted is how many direct papers are retracted and so left out.
	Retracted int `json:"retracted,omitempty"`
	// Indirect is the number of papers that do not answer the question
	// directly (indirect, tangential, unknown) or are not study reports (an
	// erratum, a comment).
	Indirect int `json:"indirect"`
	// NotAssessed is the number of papers with no usable critique (the call
	// failed or no critique was found): nothing is known about them, so they
	// are neither direct nor indirect.
	NotAssessed int `json:"not_assessed,omitempty"`
	// Randomized, Observational, Reviews are the groups of the direct studies;
	// nil when the group is empty. Observational includes non-randomized
	// trials, which GRADE starts at the same level.
	Randomized    *EvidenceGroup `json:"randomized,omitempty"`
	Observational *EvidenceGroup `json:"observational,omitempty"`
	Reviews       *EvidenceGroup `json:"reviews,omitempty"`
	// Other is the number of direct publications of another or unclear design
	// (narrative reviews, guidelines, laboratory studies).
	Other int `json:"other,omitempty"`
	// Summary is the rendered paragraph of the report, without its label, in
	// the report's language.
	Summary string `json:"summary"`
	// Studies are the direct studies one by one, the data behind the report's
	// list: a trial with all its reports is one entry, retracted and
	// not-assessed papers are not in it. The reviews come first, then the
	// trials, each group in corpus order (a trial at its first report); the
	// report sorts them.
	// Empty in artifacts written before the list existed.
	Studies []EvidenceStudy `json:"studies,omitempty"`
}

// StudyDirection is where the findings of one study point relative to the
// claim of the question. The values are the critic's result_direction words
// read against the claim: positive is "supports", negative is "opposite".
type StudyDirection string

const (
	// DirectionSupports means the study's result supports the claim.
	DirectionSupports StudyDirection = "supports"
	// DirectionNull means the study found no effect.
	DirectionNull StudyDirection = "null"
	// DirectionOpposite means the study's result runs against the claim.
	DirectionOpposite StudyDirection = "opposite"
	// DirectionMixed means the study's outcomes point both ways.
	DirectionMixed StudyDirection = "mixed"
	// DirectionUnclear means the direction cannot be stated.
	DirectionUnclear StudyDirection = "unclear"
)

// EvidenceStudy is one direct study of the evidence base. For a trial reported
// several times it describes the report that best matches the question (DOI,
// title, year, direction) with the size and design folded from all its reports.
type EvidenceStudy struct {
	DOI   string `json:"doi,omitempty"`
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
	// Design is the study's design; Blinding is set for the randomized ones.
	Design   StudyDesign `json:"design"`
	Blinding Blinding    `json:"blinding,omitempty"`
	// Cochrane marks a Cochrane review (by DOI prefix).
	Cochrane bool `json:"cochrane,omitempty"`
	// Participants is zero when the size is not stated; a review carries none.
	Participants int            `json:"participants,omitempty"`
	Direction    StudyDirection `json:"direction"`
	// Pooled is true when the study is in the pooled estimate.
	Pooled bool `json:"pooled,omitempty"`
}

// EvidenceGroup describes the direct studies of one design group.
type EvidenceGroup struct {
	Studies int `json:"studies"`
	// The blinding of a randomized group, one counter per value of Blinding;
	// BlindingNotStated is the critic's "unclear"; BlindingNotAssessed is a
	// missing value (a critique written before the field existed, e.g. from the
	// critique cache), which says nothing about the paper.
	DoubleBlind         int `json:"double_blind,omitempty"`
	SingleBlind         int `json:"single_blind,omitempty"`
	Open                int `json:"open,omitempty"`
	BlindingNotStated   int `json:"blinding_not_stated,omitempty"`
	BlindingNotAssessed int `json:"blinding_not_assessed,omitempty"`
	// Cochrane is how many reviews are Cochrane reviews (by DOI prefix).
	Cochrane int `json:"cochrane,omitempty"`
	// Participants is the sum of the known sizes; reviews carry none, as they
	// recount the participants of the trials they cover.
	Participants  int `json:"participants,omitempty"`
	SizeNotStated int `json:"size_not_stated,omitempty"`
	MedianSize    int `json:"median_size,omitempty"`
	Largest       int `json:"largest,omitempty"`
	// LargestStudy names the largest study (a short title).
	LargestStudy string `json:"largest_study,omitempty"`
}
