package eval

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/roman-redl/verdict/internal/model"
)

// Study is one study of the review a question is built from.
type Study struct {
	PMID string
	DOI  string
}

// Question is one benchmark question: a claim, the studies the review used and
// the expert conclusion.
type Question struct {
	ID        int
	Question  string
	Claim     string
	WorksIf   Change
	Answer    Answer
	Certainty Certainty
	Specialty string
	// ReviewPMID and ReviewDOI identify the Cochrane review.
	ReviewPMID string
	ReviewDOI  string
	Studies    []Study
}

// DOIs returns the normalized, deduplicated DOIs of the studies, in the order
// of the set; studies without a DOI cannot enter a DOI-list run and are skipped.
func (q *Question) DOIs() []string {
	out := make([]string, 0, len(q.Studies))

	for _, s := range q.Studies {
		d := model.NormalizeDOI(s.DOI)
		if d != "" && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}

	return out
}

// Expected is the outcome the expert answer means for the claim.
func (q *Question) Expected() Outcome { return ExpectedOutcome(q.WorksIf, q.Answer) }

// rawSet, rawQuestion and rawStudy mirror the YAML file; the enumerations are
// parsed into their types after decoding, so the errors name the question.
type rawSet struct {
	Questions []rawQuestion `yaml:"questions"`
}

type rawQuestion struct {
	ID         int        `yaml:"id"`
	Question   string     `yaml:"question"`
	Claim      string     `yaml:"claim"`
	WorksIf    string     `yaml:"works_if"`
	Answer     string     `yaml:"answer"`
	Certainty  string     `yaml:"certainty"`
	Specialty  string     `yaml:"specialty"`
	ReviewPMID string     `yaml:"review_pmid"`
	ReviewDOI  string     `yaml:"review_doi"`
	Studies    []rawStudy `yaml:"studies"`
}

type rawStudy struct {
	PMID string `yaml:"pmid"`
	DOI  string `yaml:"doi"`
}

// LoadSet reads and validates the input set file.
func LoadSet(path string) ([]Question, error) {
	//nolint:gosec // the path is the --set flag of the CLI user
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening the set: %w", err)
	}

	qs, parseErr := ParseSet(f)
	closeErr := f.Close()

	if parseErr != nil {
		return nil, fmt.Errorf("%s: %w", path, parseErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("closing the set: %w", closeErr)
	}

	return qs, nil
}

// ParseSet decodes the input set strictly (an unknown field is an error) and
// validates it: unique ids, a claim, at least one study with a DOI, valid
// enumerations.
func ParseSet(r io.Reader) ([]Question, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	var raw rawSet
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the set is empty")
		}

		return nil, fmt.Errorf("decoding the set: %w", err)
	}

	if len(raw.Questions) == 0 {
		return nil, errors.New("the set has no questions")
	}

	qs := make([]Question, 0, len(raw.Questions))
	seen := map[int]bool{}

	for i := range raw.Questions {
		q, err := raw.Questions[i].validate()
		if err != nil {
			return nil, fmt.Errorf("question #%d (id %d): %w", i+1, raw.Questions[i].ID, err)
		}

		if seen[q.ID] {
			return nil, fmt.Errorf("duplicate question id %d", q.ID)
		}

		seen[q.ID] = true
		qs = append(qs, q)
	}

	return qs, nil
}

func (r *rawQuestion) validate() (Question, error) {
	q := Question{
		ID: r.ID, Question: strings.TrimSpace(r.Question), Claim: strings.TrimSpace(r.Claim),
		Specialty: r.Specialty, ReviewPMID: r.ReviewPMID, ReviewDOI: r.ReviewDOI,
	}

	if q.Claim == "" {
		return Question{}, errors.New("empty claim")
	}

	var err error

	if q.WorksIf, err = ParseChange(r.WorksIf); err != nil {
		return Question{}, err
	}

	if q.Answer, err = ParseAnswer(r.Answer); err != nil {
		return Question{}, err
	}

	if q.Certainty, err = ParseCertainty(r.Certainty); err != nil {
		return Question{}, err
	}

	for _, s := range r.Studies {
		q.Studies = append(q.Studies, Study(s))
	}

	if len(q.DOIs()) == 0 {
		return Question{}, errors.New("no study with a DOI")
	}

	return q, nil
}

// Select returns the questions with the given ids, in the order of the set.
// No ids selects everything; an id that is not in the set is an error.
func Select(qs []Question, ids []int) ([]Question, error) {
	if len(ids) == 0 {
		return qs, nil
	}

	var missing []string

	for _, id := range ids {
		if !slices.ContainsFunc(qs, func(q Question) bool { return q.ID == id }) {
			missing = append(missing, strconv.Itoa(id))
		}
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("ids not in the set: %s", strings.Join(missing, ", "))
	}

	var out []Question

	for i := range qs {
		if slices.Contains(ids, qs[i].ID) {
			out = append(out, qs[i])
		}
	}

	return out, nil
}
