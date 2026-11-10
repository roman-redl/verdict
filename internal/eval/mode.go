package eval

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roman-redl/verdict/internal/pipeline"
)

// Mode is how an eval gives the pipeline its studies.
type Mode string

const (
	// ModeDOIs hands the pipeline exactly the studies of the review (a DOI
	// list, no search). It is the default; the zero Mode means it too.
	ModeDOIs Mode = "dois"
	// ModeSearch runs the normal discovery of mode B on the question, with
	// the source review excluded, and measures how much of the review's
	// studies the search finds.
	ModeSearch Mode = "search"
)

// ParseMode parses the mode name recorded in a run directory.
func ParseMode(s string) (Mode, error) {
	m := Mode(strings.TrimSpace(s))

	switch m {
	case ModeDOIs, ModeSearch:
		return m, nil
	}

	return "", fmt.Errorf("unknown eval mode %q (want %s | %s)", s, ModeDOIs, ModeSearch)
}

const (
	// modeFile is the marker in a question's run directory naming the mode
	// the run was made in.
	modeFile = "mode"
	// doiListFile is written next to the run only in the DOI-list mode; it
	// tells the mode of a directory made before the marker existed.
	doiListFile = "dois.txt"
)

// mode is the mode of the params: the zero value means the DOI-list mode.
func (p *Params) mode() Mode {
	if p.Mode == "" {
		return ModeDOIs
	}

	return p.Mode
}

// writeMode records the mode in a question's run directory.
func writeMode(base string, m Mode) error {
	if err := os.WriteFile(filepath.Join(base, modeFile), []byte(m+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing the eval mode marker: %w", err)
	}

	return nil
}

// recordedMode reads the mode a question's run directory was made in: the
// marker, or for a directory older than the marker the DOI list it holds.
// It is empty for a directory that records nothing (a new one).
func recordedMode(base string) (Mode, error) {
	//nolint:gosec // the path is the --out directory of the CLI user plus a fixed name
	b, err := os.ReadFile(filepath.Join(base, modeFile))

	switch {
	case err == nil:
		m, perr := ParseMode(string(b))
		if perr != nil {
			return "", fmt.Errorf("%s: %w", filepath.Join(base, modeFile), perr)
		}

		return m, nil
	case errors.Is(err, fs.ErrNotExist):
		if fileExists(filepath.Join(base, doiListFile)) {
			return ModeDOIs, nil
		}

		return "", nil
	default:
		return "", fmt.Errorf("reading the eval mode marker: %w", err)
	}
}

// checkMode fails with a user error when the run directory was made in
// another mode: a run of one mode must not be scored as the other.
func checkMode(base string, want Mode) error {
	have, err := recordedMode(base)
	if err != nil {
		return err
	}

	if have == "" || have == want {
		return nil
	}

	return &pipeline.UserError{Msg: fmt.Sprintf(
		"%s was made in the %s mode, this eval runs in the %s mode: rerun with the flags of the first run "+
			"or choose a new --out", base, have, want)}
}

// preflight checks every run directory of the eval and the questions the mode
// needs before anything runs, so a mistake costs no LLM call and leaves the
// summary of the earlier run untouched.
func preflight(qs []Question, p Params) error {
	for i := range qs {
		if p.mode() == ModeSearch && qs[i].ReviewDOI == "" {
			return &pipeline.UserError{Msg: fmt.Sprintf(
				"question %d has no review_doi: the search mode excludes the source review, so it needs one",
				qs[i].ID)}
		}

		for n := 1; n <= p.Repeat; n++ {
			if err := checkMode(filepath.Join(p.Out, runName(qs[i].ID, n)), p.mode()); err != nil {
				return err
			}
		}
	}

	return nil
}

// searchQueries are the semantic queries of a question in the search mode: the
// benchmark question and the claim, without repeats or blanks.
func searchQueries(q *Question) []string {
	var out []string

	for _, s := range []string{q.Question, q.Claim} {
		s = strings.TrimSpace(s)
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}

	return out
}
