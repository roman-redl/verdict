package pipeline

// The PubMed topic query. The model writes the search terms of the question's
// concepts; Go validates them and joins them itself (OR inside a concept, AND
// between concepts), so a model's own join never reaches PubMed.

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	// queryMaxTokens fits a query of a few dozen terms.
	queryMaxTokens = 2000
	// maxTermsPerConcept bounds the terms of one concept: the query travels in
	// a URL and a longer list means the model has drifted off the topic.
	maxTermsPerConcept = 30
)

// ConceptRole is the part of the question a concept of the PubMed query
// covers. The outcome is never one: a query that names it misses the papers
// that measure it under another word.
type ConceptRole string

const (
	// RolePopulation is the patients or the condition.
	RolePopulation ConceptRole = "population"
	// RoleIntervention is the exposure, drug, procedure or therapy under test.
	RoleIntervention ConceptRole = "intervention"
	// RoleComparator is a specific named drug, procedure or therapy the
	// intervention is compared with.
	RoleComparator ConceptRole = "comparator"
)

// conceptOrder is the order the concepts are joined in.
var conceptOrder = []ConceptRole{RolePopulation, RoleIntervention, RoleComparator}

// ParseConceptRole parses the name the model gave a concept, ignoring case
// and reading the common synonyms; an outcome or an unknown name is an error.
func ParseConceptRole(s string) (ConceptRole, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "population", "condition", "patients", "disease":
		return RolePopulation, nil
	case "intervention", "exposure", "treatment":
		return RoleIntervention, nil
	case "comparator", "comparison", "control":
		return RoleComparator, nil
	}

	return "", fmt.Errorf("unknown concept %q (want population | intervention | comparator)", s)
}

// PubMedConcept is one concept of the topic query: its role and the PubMed
// terms the model gave it.
type PubMedConcept struct {
	Role  ConceptRole `json:"role"`
	Terms []string    `json:"terms"`
}

const pubmedQuerySystemPrompt = `You write the PubMed search query for a systematic evidence assessment. You get a ` +
	`research question. Break it into concepts and give the search terms of each; the program joins them (OR inside ` +
	`a concept, AND between concepts) and adds the study-design filter itself.

Concepts, each named by its role:
- population: the patients or the condition.
- intervention: the exposure, drug, procedure or therapy under test.
- comparator: only when it is a specific named drug, procedure or therapy. Leave it out for placebo, usual care, ` +
	`no treatment and any generic comparator.
Never make a concept of the outcome (what is measured: pain, mortality, response) and never add study-design words ` +
	`(randomized, trial, review): the outcome is worded in many ways, and a query that names it misses papers.

Terms of a concept: as many as the topic needs (5 to 25). Recall matters more than precision, because a later ` +
	`screening drops the off-topic papers.
- the MeSH heading with the Mesh tag, in its exact name: "Gastroesophageal Reflux"[Mesh]
- free-text synonyms with the tiab tag, phrases in double quotes: "reflux disease"[tiab], GERD[tiab]
- for a drug: the generic name and the brand names; for any concept: abbreviations, spelling variants (US and UK) ` +
	`and plurals
- truncation with * where it is safe, at least four letters before it: fundoplicat*[tiab]; never truncate a short stem
Each term is a single PubMed term with its field tag: no AND, OR or NOT and no parentheses outside quotes. Write the ` +
	`terms in English whatever the language of the question.

Answer with {"concepts":[{"name":"population","terms":["..."]},{"name":"intervention","terms":["..."]}],` +
	`"query":"the whole Boolean query you mean, for the log"}.`

// pubmedQueryAnswer is the model's reply.
type pubmedQueryAnswer struct {
	Concepts []struct {
		Name  string   `json:"name"`
		Terms []string `json:"terms"`
	} `json:"concepts"`
	Query string `json:"query"`
}

// writePubMedQuery asks the plan-role model for the concepts of the question
// (one call) and returns them validated, with the model's own query for the
// log. The call's cost is recorded in the state.
func (p *Pipeline) writePubMedQuery(ctx context.Context, st *State, question string) ([]PubMedConcept, string, error) {
	if p.clients.LLMPlan == nil {
		return nil, "", &UserError{Msg: "writing the PubMed query needs the plan-role model " +
			"(or pass a ready query with --pubmed-query)"}
	}

	var ans pubmedQueryAnswer

	user := "Question: " + truncate(question, 2000)
	usage, err := AskJSON(ctx, p.clients.LLMPlan, pubmedQuerySystemPrompt, user, &ans, queryMaxTokens)

	st.queryWritingCost().add(usage)

	if err != nil {
		return nil, "", fmt.Errorf("writing the PubMed query: %w", err)
	}

	concepts, err := p.validConcepts(&ans)
	if err != nil {
		return nil, "", err
	}

	return concepts, ans.Query, nil
}

// validConcepts turns the model's concepts into the ones the query is built
// from: unknown names (an outcome) dropped with a warning, the terms of two
// concepts of one role merged, every term checked. It fails when no concept
// besides a comparator is left.
func (p *Pipeline) validConcepts(ans *pubmedQueryAnswer) ([]PubMedConcept, error) {
	byRole := map[ConceptRole][]string{}

	for _, c := range ans.Concepts {
		role, err := ParseConceptRole(c.Name)
		if err != nil {
			p.log.Warn("PubMed query writer: a concept was dropped", "name", c.Name, "err", err)
			continue
		}

		terms, err := mergeTerms(byRole[role], c.Terms)
		if err != nil {
			return nil, err
		}

		byRole[role] = terms
	}

	var out []PubMedConcept

	for _, role := range conceptOrder {
		terms := byRole[role]
		if len(terms) > maxTermsPerConcept {
			p.log.Warn("PubMed query writer: too many terms, keeping the first ones",
				"role", role, "terms", len(terms), "keep", maxTermsPerConcept)

			terms = terms[:maxTermsPerConcept]
		}

		if len(terms) > 0 {
			out = append(out, PubMedConcept{Role: role, Terms: terms})
		}
	}

	if len(out) == 0 || (len(out) == 1 && out[0].Role == RoleComparator) {
		return nil, fmt.Errorf("the PubMed query writer gave no population or intervention terms (query %q)", ans.Query)
	}

	return out, nil
}

// mergeTerms adds the new terms to the ones a concept has: trimmed, blanks and
// case-insensitive repeats dropped, every term checked.
func mergeTerms(have, add []string) ([]string, error) {
	for _, t := range add {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}

		if !balanced(t) {
			return nil, fmt.Errorf("the PubMed query writer gave a term with unbalanced parentheses or quotes: %q", t)
		}

		if !slices.ContainsFunc(have, func(h string) bool { return strings.EqualFold(h, t) }) {
			have = append(have, t)
		}
	}

	return have, nil
}

// operatorRe finds a Boolean operator in a term.
var operatorRe = regexp.MustCompile(`\s(?:AND|OR|NOT)\s`)

// topicQuery joins the concepts: the terms of a concept with OR, the concepts
// with AND. A comparator is left out when withComparator is false. An empty
// string means no concept is left.
func topicQuery(concepts []PubMedConcept, withComparator bool) string {
	var parts []string

	for _, c := range concepts {
		if c.Role == RoleComparator && !withComparator {
			continue
		}

		terms := make([]string, len(c.Terms))

		for i, t := range c.Terms {
			terms[i] = t
			if operatorRe.MatchString(t) {
				terms[i] = "(" + t + ")" // a term the model made compound stays one unit
			}
		}

		parts = append(parts, "("+strings.Join(terms, " OR ")+")")
	}

	return strings.Join(parts, " AND ")
}

// hasComparator reports whether the concepts include a comparator.
func hasComparator(concepts []PubMedConcept) bool {
	return slices.ContainsFunc(concepts, func(c PubMedConcept) bool { return c.Role == RoleComparator })
}

// balanced reports whether the parentheses outside double quotes pair up and
// the quotes close; a query that fails it is rejected before PubMed sees it.
func balanced(s string) bool {
	depth, inQuote := 0, false

	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case inQuote:
			continue
		case r == '(':
			depth++
		case r == ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}

	return depth == 0 && !inQuote
}
