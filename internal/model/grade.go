package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// YesNo is a yes/no answer of the synthesis's GRADE checklist. The zero value
// is "not answered"; Normalize turns every answer into one of the two words.
type YesNo string

const (
	// Yes means the text states the fact the item asks about.
	Yes YesNo = "yes"
	// No means the text does not state it (an unreported detail is "no").
	No YesNo = "no"
)

// ParseYesNo parses an answer, ignoring case and surrounding spaces. The empty
// string is an error too: the caller decides what a missing answer means.
func ParseYesNo(s string) (YesNo, error) {
	switch y := YesNo(strings.ToLower(strings.TrimSpace(s))); y {
	case Yes, No:
		return y, nil
	}

	return "", fmt.Errorf("unknown answer %q (want yes | no)", s)
}

// IsYes reports whether the answer is yes.
func (y YesNo) IsYes() bool { return y == Yes }

// UnmarshalJSON reads any JSON value, so that a wrong type never fails the
// decode of the whole reply: a string as it is, a boolean as yes or no, null
// as not answered; anything else (a number, an object) is kept as its JSON
// text, which ParseYesNo then rejects by name.
func (y *YesNo) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*y = YesNo(s)

		return nil
	}

	switch raw := strings.TrimSpace(string(b)); raw {
	case "true":
		*y = Yes
	case "false":
		*y = No
	default:
		*y = YesNo(raw)
	}

	return nil
}

// RiskOfBiasStudy is the four factual risk-of-bias answers for one direct
// primary study. Lack of blinding is not among them: the pipeline computes it
// (BlindingBias).
type RiskOfBiasStudy struct {
	DOI string `json:"doi"`
	// Attrition is yes when loss to follow-up is above 20% in any arm or the
	// arms differ by more than 10 percentage points.
	Attrition YesNo `json:"attrition"`
	// AllocationFlaw is yes only when the text states that allocation was not
	// random or not concealed: quasi-random or alternating allocation,
	// allocation by date or record number, a concealment stated as broken or
	// absent. A baseline imbalance between the arms is not this item.
	AllocationFlaw YesNo `json:"allocation_flaw"`
	// SelectiveOutcome is yes when the outcome is a post hoc, non-prespecified
	// or subgroup-only analysis.
	SelectiveOutcome YesNo `json:"selective_outcome"`
	// StoppedEarlyForBenefit is yes for a trial stopped early for benefit.
	StoppedEarlyForBenefit YesNo `json:"stopped_early_for_benefit"`
}

// RiskOfBiasItems are the risk-of-bias answers. The model answers per study
// (PerStudy) and the pipeline aggregates them by weight; the four global
// answers are the shape of an older reply (and of a model that ignores the new
// shape), read as one answer for all the studies together.
type RiskOfBiasItems struct {
	// PerStudy is one entry for every direct primary study of the input. Nil
	// means "not answered" (the global answers apply); an empty, non-nil list
	// is an answer: no direct primary study, or none with a flaw to report.
	PerStudy []RiskOfBiasStudy `json:"per_study"`
	// The global answers, read only when PerStudy is nil; each is the same item
	// as in RiskOfBiasStudy, judged on the studies that carry most of the evidence.
	Attrition              YesNo  `json:"attrition,omitempty"`
	AllocationFlaw         YesNo  `json:"allocation_flaw,omitempty"`
	SelectiveOutcome       YesNo  `json:"selective_outcome,omitempty"`
	StoppedEarlyForBenefit YesNo  `json:"stopped_early_for_benefit,omitempty"`
	Note                   string `json:"note,omitempty"`
	// Repairs is what Normalize fixed in PerStudy instead of rejecting the reply;
	// the pipeline logs it. Not part of the artifact.
	Repairs PerStudyRepairs `json:"-"`
}

// PerStudyRepairs counts the repairs of the per-study risk-of-bias answers.
type PerStudyRepairs struct {
	// ItemsFilled is the number of items missing from the kept entries; each is
	// read as "no" (an unreported detail is "no").
	ItemsFilled int
	// EntriesDropped is the number of entries with an answer outside yes | no;
	// such a study counts as "no" on every item, like one the model left out.
	EntriesDropped int
}

// InconsistencyItems are the factual inconsistency items; the pipeline reads
// them only with two or more comparable studies.
type InconsistencyItems struct {
	// DirectionsConflict is yes when the direct studies point in opposite
	// directions.
	DirectionsConflict YesNo `json:"directions_conflict"`
	// SizesDifferBeyondChance is yes when the effect sizes differ by more than
	// chance explains.
	SizesDifferBeyondChance YesNo  `json:"sizes_differ_beyond_chance"`
	Note                    string `json:"note,omitempty"`
}

// IndirectnessItems are the indirectness items: yes only when the difference
// from the question could plausibly change the effect.
type IndirectnessItems struct {
	Population   YesNo  `json:"population"`
	Intervention YesNo  `json:"intervention"`
	Comparator   YesNo  `json:"comparator"`
	Outcome      YesNo  `json:"outcome"`
	Note         string `json:"note,omitempty"`
}

// PublicationBiasItem is the publication-bias item.
type PublicationBiasItem struct {
	// Suspected is yes when the evidence is a few small, positive, commercially
	// funded studies.
	Suspected YesNo  `json:"suspected"`
	Note      string `json:"note,omitempty"`
}

// GradeChecklist is the synthesis's grade_checklist: factual yes/no answers
// from which the pipeline computes the domain levels and the certainty level.
// The model does not judge how serious a problem is; it rates imprecision only
// when the pipeline computed none (no imprecision field in the input), and then
// as a number.
type GradeChecklist struct {
	RiskOfBias      RiskOfBiasItems     `json:"risk_of_bias"`
	Inconsistency   InconsistencyItems  `json:"inconsistency"`
	Indirectness    IndirectnessItems   `json:"indirectness"`
	PublicationBias PublicationBiasItem `json:"publication_bias"`
	// LargeEffect is yes for a very large, consistent effect of non-randomized
	// evidence; an omitted answer reads as no.
	LargeEffect YesNo `json:"large_effect,omitempty"`
	// ImprecisionWithoutPool is the model's rating of imprecision in levels
	// (0, 1 or 2), required only when the pipeline computed no imprecision (the
	// input has no imprecision field); with one it is dropped.
	ImprecisionWithoutPool *int `json:"imprecision_without_pool,omitempty"`

	// What UnmarshalJSON could not read; Normalize reports it. Never serialized.
	typeErr         error  // the first value of a wrong JSON type
	imprecisionText string // an imprecision_without_pool that is no whole number
}

// maxImprecisionAnswer is the largest imprecision_without_pool (very serious).
const maxImprecisionAnswer = 2

// maxWholeAnswer bounds the numbers read as an imprecision answer, so that the
// conversion to int is exact.
const maxWholeAnswer = 1e9

// UnmarshalJSON decodes the checklist without letting a value of a wrong type
// (an object where a list is expected, a text where a number is) fail the
// decode of the whole reply: such a value is remembered and Normalize rejects
// the checklist with it, which the caller treats as an unusable checklist. The
// imprecision answer is read leniently: a whole number written as 1.0 or as
// the text "1" is that number.
func (c *GradeChecklist) UnmarshalJSON(b []byte) error {
	type plain GradeChecklist // no methods: the decode below does not recurse

	*c = GradeChecklist{}

	aux := struct {
		*plain
		Imprecision json.RawMessage `json:"imprecision_without_pool"`
	}{plain: (*plain)(c)}

	if err := json.Unmarshal(b, &aux); err != nil {
		// Unmarshal carries on after a wrong type and reports the first one
		if _, ok := errors.AsType[*json.UnmarshalTypeError](err); !ok {
			return fmt.Errorf("decoding grade_checklist: %w", err)
		}

		c.typeErr = err
	}

	c.ImprecisionWithoutPool, c.imprecisionText = readImprecision(aux.Imprecision)

	return nil
}

// readImprecision reads an imprecision answer: nil for none, the number for a
// whole number (also as a numeric text), else the text of what was given.
func readImprecision(raw json.RawMessage) (n *int, bad string) {
	text := strings.TrimSpace(string(raw))

	var s string
	if json.Unmarshal(raw, &s) == nil {
		text = strings.TrimSpace(s)
	}

	if text == "" || text == "null" {
		return nil, ""
	}

	f, err := strconv.ParseFloat(text, 64)
	if err != nil || f != math.Trunc(f) || math.Abs(f) > maxWholeAnswer {
		return nil, text
	}

	whole := int(f)

	return &whole, ""
}

// Normalize checks the answers and writes them in their canonical form. Every
// item is required except large_effect (omitted reads as no); the risk-of-bias
// answers are required per study (PerStudy), or, in an older reply, as the
// four global answers. imprecisionRequired says that the pipeline computed no
// imprecision, so imprecision_without_pool is required and must be 0, 1 or 2;
// otherwise it is dropped. The first problem is returned, named by its JSON
// path (a value of a wrong JSON type is named by the decoder). Repairs of the
// per-study answers are no problem: see RiskOfBiasItems.Repairs.
func (c *GradeChecklist) Normalize(imprecisionRequired bool) error {
	if c.typeErr != nil {
		return fmt.Errorf("wrong JSON type: %w", c.typeErr)
	}

	if err := c.RiskOfBias.normalize(); err != nil {
		return err
	}

	if err := c.normalizeImprecision(imprecisionRequired); err != nil {
		return err
	}

	items := []struct {
		path     string
		answer   *YesNo
		optional bool
	}{
		{"inconsistency.directions_conflict", &c.Inconsistency.DirectionsConflict, false},
		{"inconsistency.sizes_differ_beyond_chance", &c.Inconsistency.SizesDifferBeyondChance, false},
		{"indirectness.population", &c.Indirectness.Population, false},
		{"indirectness.intervention", &c.Indirectness.Intervention, false},
		{"indirectness.comparator", &c.Indirectness.Comparator, false},
		{"indirectness.outcome", &c.Indirectness.Outcome, false},
		{"publication_bias.suspected", &c.PublicationBias.Suspected, false},
		{"large_effect", &c.LargeEffect, true},
	}

	for _, it := range items {
		if it.optional && strings.TrimSpace(string(*it.answer)) == "" {
			*it.answer = No

			continue
		}

		y, err := ParseYesNo(string(*it.answer))
		if err != nil {
			return fmt.Errorf("%s: %w", it.path, err)
		}

		*it.answer = y
	}

	return nil
}

// normalizeImprecision checks imprecision_without_pool: required and 0..2 when
// the pipeline computed no imprecision, dropped otherwise.
func (c *GradeChecklist) normalizeImprecision(required bool) error {
	if !required {
		c.ImprecisionWithoutPool, c.imprecisionText = nil, ""

		return nil
	}

	if c.imprecisionText != "" {
		return fmt.Errorf("imprecision_without_pool: unknown answer %q (want 0, 1 or 2)", c.imprecisionText)
	}

	if c.ImprecisionWithoutPool == nil {
		return errors.New("imprecision_without_pool: missing (required when the input has no imprecision: 0, 1 or 2)")
	}

	if n := *c.ImprecisionWithoutPool; n < 0 || n > maxImprecisionAnswer {
		return fmt.Errorf("imprecision_without_pool: unknown answer %d (want 0, 1 or 2)", n)
	}

	return nil
}

// answer is one risk-of-bias answer with its JSON key.
type answer struct {
	key string
	val *YesNo
}

// normalize checks the risk-of-bias answers. With PerStudy (even an empty one)
// an item missing from an entry is read as "no" and an entry with an answer
// outside yes | no is dropped (both counted in Repairs; a study without an
// entry counts as "no" everywhere), and the global answers are dropped; without
// PerStudy the four global answers of an older reply are required.
func (r *RiskOfBiasItems) normalize() error {
	if r.PerStudy != nil {
		r.Repairs = PerStudyRepairs{}
		kept := make([]RiskOfBiasStudy, 0, len(r.PerStudy))

		for _, s := range r.PerStudy {
			s.DOI = strings.TrimSpace(s.DOI)

			filled, ok := s.normalize()
			if !ok {
				r.Repairs.EntriesDropped++

				continue
			}

			r.Repairs.ItemsFilled += filled

			kept = append(kept, s)
		}

		r.PerStudy = kept
		r.Attrition, r.AllocationFlaw, r.SelectiveOutcome, r.StoppedEarlyForBenefit = "", "", "", ""

		return nil
	}

	if r.Attrition == "" && r.AllocationFlaw == "" && r.SelectiveOutcome == "" && r.StoppedEarlyForBenefit == "" {
		return errors.New("risk_of_bias.per_study: missing (one entry per direct primary study, [] when there is none)")
	}

	return normalizeAnswers("risk_of_bias.", riskAnswers(&r.Attrition, &r.AllocationFlaw,
		&r.SelectiveOutcome, &r.StoppedEarlyForBenefit))
}

// normalize writes the four answers of the entry in their canonical form: a
// missing one is "no" (filled counts them). ok is false when an answer is
// outside yes | no, and the entry is unusable.
func (s *RiskOfBiasStudy) normalize() (filled int, ok bool) {
	for _, a := range riskAnswers(&s.Attrition, &s.AllocationFlaw, &s.SelectiveOutcome, &s.StoppedEarlyForBenefit) {
		if strings.TrimSpace(string(*a.val)) == "" {
			*a.val = No
			filled++

			continue
		}

		y, err := ParseYesNo(string(*a.val))
		if err != nil {
			return 0, false
		}

		*a.val = y
	}

	return filled, true
}

// riskAnswers names the four risk-of-bias answers by their JSON keys.
func riskAnswers(attrition, allocation, selective, stopped *YesNo) []answer {
	return []answer{
		{"attrition", attrition},
		{"allocation_flaw", allocation},
		{"selective_outcome", selective},
		{"stopped_early_for_benefit", stopped},
	}
}

// normalizeAnswers parses every answer in place; a bad one is named by prefix+key.
func normalizeAnswers(prefix string, answers []answer) error {
	for _, a := range answers {
		y, err := ParseYesNo(string(*a.val))
		if err != nil {
			return fmt.Errorf("%s%s: %w", prefix, a.key, err)
		}

		*a.val = y
	}

	return nil
}

// The causes a GradeDomain names: the checklist items (and the computed
// blinding rule) that lowered it. The values are what the artifact stores.
const (
	CauseBlinding               = "blinding"
	CauseAttrition              = "attrition"
	CauseAllocationFlaw         = "allocation_flaw"
	CauseSelectiveOutcome       = "selective_outcome"
	CauseStoppedEarlyForBenefit = "stopped_early_for_benefit"
	CauseDirectionsConflict     = "directions_conflict"
	CauseSizesDiffer            = "sizes_differ_beyond_chance"
	CausePopulation             = "population"
	CauseIntervention           = "intervention"
	CauseComparator             = "comparator"
	CauseOutcome                = "outcome"
)

// GradeDomain is the computed rating of one GRADE domain: how many levels it
// lowers the certainty and which items did it.
type GradeDomain struct {
	Levels int      `json:"levels"`
	Causes []string `json:"causes,omitempty"`
	// Shares is, for the risk-of-bias items answered per study, the share (0 to
	// 1) of the studies' weight that answered yes, by cause; every item is in
	// it, the counting ones (share above one half) also in Causes. Nil for the
	// other domains and for the global answers of an older reply.
	Shares map[string]float64 `json:"shares,omitempty"`
}

// GradeAssessment is the certainty level computed by the pipeline from the
// checklist and the computed ratings: the starting level, the five domains,
// the upgrade and the resulting level. It is stored with the checklist it was
// computed from, so the report can show and a reader can audit every step.
type GradeAssessment struct {
	// Start is the starting level: strong for randomized evidence, weak
	// otherwise.
	Start           string      `json:"start"`
	RiskOfBias      GradeDomain `json:"risk_of_bias"`
	Inconsistency   GradeDomain `json:"inconsistency"`
	Indirectness    GradeDomain `json:"indirectness"`
	Imprecision     GradeDomain `json:"imprecision"`
	PublicationBias GradeDomain `json:"publication_bias"`
	// Upgrade is 1 for a large effect of non-randomized evidence.
	Upgrade int `json:"upgrade,omitempty"`
	// NoDirectStudy is true when no direct primary study report (not a review,
	// not retracted, not a non-study paper) is in the evidence: the level is
	// insufficient whatever the arithmetic gives. Computed in code only.
	NoDirectStudy bool `json:"no_direct_study,omitempty"`
	// Level is the computed certainty: strong | moderate | weak | insufficient.
	Level     string         `json:"level"`
	Checklist GradeChecklist `json:"checklist"`
}
