package pipeline

import (
	"context"
	"regexp"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// minTitleRunes is the shortest line worth looking up: anything shorter is
// junk from a clipboard dump, not a title.
const minTitleRunes = 12

var bulletRe = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.)])\s+`)

// ResolutionKind says how a line of a pasted list was handled.
type ResolutionKind string

const (
	// ResolvedPassthrough: the line already contains a DOI and is kept as is.
	ResolvedPassthrough ResolutionKind = "passthrough"
	// ResolvedExact: the title search found a work with the same title.
	ResolvedExact ResolutionKind = "exact"
	// ResolvedApproximate: the best match has a different title.
	ResolvedApproximate ResolutionKind = "approx"
	// Unresolved: nothing matched, or the lookup failed (Err is set then).
	Unresolved ResolutionKind = "unresolved"
)

// TitleResolution is the outcome for one line.
type TitleResolution struct {
	Line  string
	Kind  ResolutionKind
	DOI   string // normalized; set for exact and approximate matches
	Year  int
	Title string // the matched work's title
	Err   error  // the lookup failure of an unresolved line, if any
}

// ResolveTitles turns a paste that carries titles instead of DOIs into DOIs
// via a title search (mode C without the clipboard luck). Lines that already
// contain DOIs pass through; every match keeps its own title so the result is
// verifiable at a glance. Empty, repeated and too short lines are skipped.
func ResolveTitles(ctx context.Context, finder TitleFinder, text string) []TitleResolution {
	var out []TitleResolution

	seen := map[string]bool{}

	for line := range strings.SplitSeq(text, "\n") {
		if ctx.Err() != nil {
			break
		}

		line = stripBullet(line)
		if line == "" || seen[line] {
			continue
		}

		seen[line] = true

		if len(ExtractDOIs(line)) > 0 {
			out = append(out, TitleResolution{Line: line, Kind: ResolvedPassthrough})
			continue
		}

		if len([]rune(line)) < minTitleRunes {
			continue
		}

		out = append(out, resolveTitle(ctx, finder, line))
	}

	return out
}

func resolveTitle(ctx context.Context, finder TitleFinder, line string) TitleResolution {
	w, err := finder.SearchTitle(ctx, line)

	switch {
	case err != nil:
		return TitleResolution{Line: line, Kind: Unresolved, Err: err}
	case w == nil:
		return TitleResolution{Line: line, Kind: Unresolved}
	}

	kind := ResolvedApproximate
	if strings.EqualFold(w.Title, line) {
		kind = ResolvedExact
	}

	return TitleResolution{Line: line, Kind: kind, DOI: model.NormalizeDOI(w.DOI), Year: w.Year, Title: w.Title}
}

func stripBullet(line string) string {
	return strings.TrimSpace(bulletRe.ReplaceAllString(line, ""))
}
