package pipeline

// Excluded DOIs (run --exclude-dois): papers a run must not see. Measuring
// the search recall against a systematic review needs the review itself out of
// the pool — otherwise mining its reference list finds its own included
// studies and the recall is circular.

import (
	"context"
	"html"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/roman-redl/verdict/internal/model"
)

// minExcludedTitleWords is the fewest words an excluded paper's title may have
// to exclude other papers by title: a short generic title would match
// unrelated papers that merely contain it.
const minExcludedTitleWords = 4

// cochraneDOIRe matches a Cochrane review DOI in its normalized (lowercase)
// form: the CD number is group 1, the .pubN version suffix is optional.
var cochraneDOIRe = regexp.MustCompile(`^10\.1002/14651858\.(cd\d+)(?:\.pub\d+)?$`)

// doiExclusion is the set of papers a run drops. A paper matches by normalized
// DOI equality; a Cochrane review DOI excludes every version of its CD number
// (any .pubN, or none), because the pool may hold another version than the one
// the user named. A paper that reprints, summarizes or comments on an excluded
// paper matches too (matchesTitle): journals do that under other DOIs (the
// Evidence-Based Child Health reprints of a Cochrane review), and those carry
// the review's own results. It also records which candidates it
// dropped. A nil *doiExclusion excludes nothing; it is not safe for concurrent
// use (the pool is built on one goroutine).
type doiExclusion struct {
	listed   []string        // the requested DOIs, normalized, in the given order
	exact    map[string]bool // normalized DOIs
	cochrane map[string]bool // CD numbers, lowercase
	titles   []string        // normalized titles of the excluded papers (see normalizeTitle), distinct
	dropped  []string        // distinct DOIs of the dropped candidates, in the order dropped
	seen     map[string]bool
}

// newDOIExclusion builds the exclusion from flag values: each is one DOI
// (any prefix or URL form) or a comma-separated list of them. It returns nil
// when no usable value is given.
func newDOIExclusion(values []string) *doiExclusion {
	e := &doiExclusion{exact: map[string]bool{}, cochrane: map[string]bool{}, seen: map[string]bool{}}

	for _, v := range values {
		for part := range strings.SplitSeq(v, ",") {
			doi := model.NormalizeDOI(part)
			if doi == "" || e.exact[doi] {
				continue
			}

			e.exact[doi] = true
			e.listed = append(e.listed, doi)

			if m := cochraneDOIRe.FindStringSubmatch(doi); m != nil {
				e.cochrane[m[1]] = true
			}
		}
	}

	if len(e.listed) == 0 {
		return nil
	}

	return e
}

// matches reports whether the DOI is excluded.
func (e *doiExclusion) matches(doi string) bool {
	if e == nil {
		return false
	}

	doi = model.NormalizeDOI(doi)
	if doi == "" {
		return false
	}

	if e.exact[doi] {
		return true
	}

	if m := cochraneDOIRe.FindStringSubmatch(doi); m != nil {
		return e.cochrane[m[1]]
	}

	return false
}

// matchesTitle reports whether the title is a reprint, summary or commentary of
// an excluded paper: it contains the paper's title as whole words (a title of
// fewer than minExcludedTitleWords words is never stored, see addTitle) and
// carries a reprint marker (reprintMarked). The marker is what tells a copy
// from a trial that merely contains the title of a Cochrane review, which names
// an intervention for a condition ("Corticosteroids for acute bacterial
// meningitis" is part of "Adjunctive corticosteroids for acute bacterial
// meningitis in adults: a randomized controlled trial"). A title equal to the
// excluded one is a copy as well.
func (e *doiExclusion) matchesTitle(title string) bool {
	if e == nil || len(e.titles) == 0 {
		return false
	}

	clean := cleanTitle(title)
	plain := foldTitle(clean, false)

	return slices.ContainsFunc(e.titles, func(t string) bool {
		// the spaces around both make the match start and end at a word boundary
		return plain == t || (strings.Contains(" "+plain+" ", " "+t+" ") && reprintMarked(clean, t))
	})
}

// reprintMarked reports whether the candidate title (cleaned, see cleanTitle),
// which contains the excluded title t (normalized), shows that it reprints,
// summarizes or comments on the paper rather than reports its own study: the
// excluded title stands in quotes, or the title names Cochrane, or it is a
// "summary of" or a commentary.
func reprintMarked(clean, t string) bool {
	plain := " " + foldTitle(clean, false) + " "

	if strings.Contains(plain, " cochrane ") || strings.Contains(plain, " summary of ") ||
		strings.Contains(plain, " commentar") { //nolint:misspell // the stem of commentary, commentaries, commentaire
		return true
	}

	// the segments between the 1st and 2nd quote, the 3rd and 4th, and so on;
	// a quote that is never closed opens nothing
	parts := strings.Split(foldTitle(clean, true), quoteMark)
	for i := 1; i < len(parts)-1; i += 2 {
		if strings.Contains(" "+strings.Join(strings.Fields(parts[i]), " ")+" ", " "+t+" ") {
			return true
		}
	}

	return false
}

// htmlTagRe matches an HTML tag as OpenAlex titles carry them (<i>, </sup>,
// <scp>): a "<" that opens a letter, not a "less than" sign.
var htmlTagRe = regexp.MustCompile(`</?[A-Za-z][^>]*>`)

// cleanTitle strips HTML tags from a title and unescapes its entities
// (&amp;, &lt;): the markup of a source must not split or change words.
func cleanTitle(title string) string {
	return html.UnescapeString(htmlTagRe.ReplaceAllString(title, ""))
}

// quoteMark stands for a quotation mark in the title of foldTitle with quotes.
const quoteMark = "'"

// isQuote reports whether the rune is a straight or typographic quotation mark
// (or an apostrophe, see foldTitle).
func isQuote(r rune) bool { return strings.ContainsRune(`'"‘’“”`, r) }

// foldTitle puts a cleaned title in the form titles are compared in: lower
// case, every character that is not a letter or a digit (punctuation, Unicode
// quotes and dashes) a space, runs of spaces one. With quotes, a quotation mark
// stays as quoteMark between spaces, except an apostrophe inside a word
// ("children's"), which is a space like in the form without quotes.
func foldTitle(clean string, quotes bool) string {
	runes := []rune(clean)
	isWord := func(i int) bool {
		return i >= 0 && i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]))
	}

	var b strings.Builder

	for i, r := range runes {
		switch {
		case isWord(i):
			b.WriteRune(unicode.ToLower(r))
		case quotes && isQuote(r) && (!isWord(i-1) || !isWord(i+1)):
			b.WriteString(" " + quoteMark + " ")
		default:
			b.WriteByte(' ')
		}
	}

	if !quotes {
		return strings.Join(strings.Fields(b.String()), " ")
	}

	return b.String()
}

// addTitle makes the paper's title exclude by title too; a title too short to
// tell one paper from another is not used.
func (e *doiExclusion) addTitle(title string) {
	t := normalizeTitle(title)
	if len(strings.Fields(t)) < minExcludedTitleWords || slices.Contains(e.titles, t) {
		return
	}

	e.titles = append(e.titles, t)
}

// normalizeTitle puts a title in the form titles are compared in: HTML tags
// stripped and entities unescaped (cleanTitle), then lower case, every
// character that is not a letter or a digit (punctuation, Unicode quotes and
// dashes) a space, runs of spaces one.
func normalizeTitle(title string) string { return foldTitle(cleanTitle(title), false) }

// drop reports whether the candidate with this DOI and title is excluded and,
// when it is, records it (once per DOI) for the discovery artifact. A
// candidate without a DOI is never dropped: it cannot be selected anyway, and
// the artifact lists DOIs.
func (e *doiExclusion) drop(doi, title string) bool {
	doi = model.NormalizeDOI(doi)
	if doi == "" {
		return false
	}

	if !e.matches(doi) && !e.matchesTitle(title) {
		return false
	}

	if !e.seen[doi] {
		e.seen[doi] = true
		e.dropped = append(e.dropped, doi)
	}

	return true
}

// loadTitles looks up the title of every excluded DOI once, through the
// metadata port, so that reprints under other DOIs can be excluded by title. A
// lookup that fails or finds nothing only costs that DOI its title rule: the
// DOI exclusion itself needs no lookup, so the run goes on with a warning.
func (p *Pipeline) loadTitles(ctx context.Context, e *doiExclusion) {
	if p.clients.Works == nil {
		p.log.Warn("no metadata source: --exclude-dois works by DOI only, not by title")
		return
	}

	for _, doi := range e.listed {
		w, err := p.clients.Works.ByDOI(ctx, doi)

		switch {
		case err != nil:
			p.log.Warn("title of an excluded DOI not looked up: excluding it by DOI only", "doi", doi, "err", err)
		case w == nil || w.Title == "":
			p.log.Warn("the source has no title for an excluded DOI: excluding it by DOI only", "doi", doi)
		default:
			e.addTitle(w.Title)
		}
	}

	p.log.Info("excluded titles", "dois", len(e.listed), "titles", len(e.titles))
}

// newExclusion is newDOIExclusion plus the titles of the excluded DOIs; it
// returns nil when no usable value is given. The lookup lives here, not in
// newDOIExclusion, so that building the exclusion from flags stays free of
// I/O.
func (p *Pipeline) newExclusion(ctx context.Context, values []string) *doiExclusion {
	e := newDOIExclusion(values)
	if e == nil {
		return nil
	}

	p.loadTitles(ctx, e)

	return e
}

// recordExclusion writes the exclusion into the discovery artifact and logs
// what it dropped; a run without --exclude-dois leaves both untouched.
func (p *Pipeline) recordExclusion(info *DiscoveryInfo, excl *doiExclusion) {
	if excl == nil {
		return
	}

	excl.record(info)
	p.log.Info("excluded DOIs dropped from the discovery", "requested", len(info.ExcludeDOIs),
		"dropped", info.ExcludedCount, "dois", info.ExcludedDOIs)
}

// dropExcludedDOIs removes the excluded DOIs from a user-supplied list. A DOI
// both given and excluded is excluded, with a warning: the user most likely
// pasted the excluded review into the list by mistake.
func (p *Pipeline) dropExcludedDOIs(dois []string, excl *doiExclusion) []string {
	return slices.DeleteFunc(dois, func(doi string) bool {
		// the user's own list has no titles to compare
		if !excl.drop(doi, "") {
			return false
		}

		p.log.Warn("a DOI of the input list is also excluded by --exclude-dois: excluding it", "doi", doi)

		return true
	})
}

// record writes the requested list, the dropped DOIs and their count into the
// discovery artifact; it writes nothing for a nil exclusion.
func (e *doiExclusion) record(info *DiscoveryInfo) {
	if e == nil {
		return
	}

	info.ExcludeDOIs = e.listed
	info.ExcludedDOIs = e.dropped
	info.ExcludedCount = len(e.dropped)
}
