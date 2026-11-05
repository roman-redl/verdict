package pipeline

import (
	"github.com/roman-redl/verdict/internal/model"
)

func chunks[T any](in []T, size int) [][]T {
	var out [][]T

	for i := 0; i < len(in); i += size {
		end := min(i+size, len(in))
		out = append(out, in[i:end])
	}

	return out
}

// orDefault returns v when set, else def — floored at 1: a zero limit would
// deadlock errgroup (SetLimit(0) yields an unbuffered semaphore).
func orDefault(v, def int) int {
	if v > 0 {
		return v
	}

	if def < 1 {
		return 1
	}

	return def
}

// persistedErr renders an error as text for the saved artifacts (the Errors
// and Warnings lists, Critique.Error). It is the single place where an error
// becomes a string for stored data; everywhere else errors travel as error.
func persistedErr(err error) string {
	return err.Error() //nolint:forbidigo // output boundary: the JSON artifacts store error text
}

// sourceFailed records a data-source failure for a paper: a warning in the
// log with the full error and a line in the paper's Errors (the artifact
// trail). A failed source never stops the stage — synthesis proceeds on
// partial data — so the log is the only place the failure is visible live.
func (p *Pipeline) sourceFailed(e *model.EnrichedPaper, source string, err error) {
	p.log.Warn("source failed", "source", source, "doi", e.DOI, "err", err)

	e.Errors = append(e.Errors, source+": "+persistedErr(err))
}
