package pipeline

// The wording of the report in one language. The labels, the field names and
// the sentences Go writes into report.md live here, one lexicon per language
// (lexicon_en.go, lexicon_ru.go): no external i18n library, the table is small.
// What stays English in every language: the enum values and JSON keys, the
// reason texts Go generates inside the computed lines (imprecision, blinding),
// the data warnings, the critic's notes and the LLM usage footer.

import (
	"strconv"

	"github.com/roman-redl/verdict/internal/model"
)

// forms are the plural forms of a noun: for 1, for 2-4 (Russian only) and for
// the rest. English uses the first and the last.
type forms [3]string

// lexicon is the wording of one language. A zero value is not usable: take one
// from lexiconFor.
type lexicon struct {
	lang model.Language
	// formIndex picks the plural form for a count.
	formIndex func(n int) int

	// The skeleton of report.md: headings and field labels.
	reportTitle, question, verdict, effectDirection   string
	imprecision, blindingBias, comparable             string
	evidenceBase, consensus, directTally, generated   string
	rationale, matrix, contradictions, gaps, warnings string
	// matrixHeader is the whole header row of the evidence matrix.
	matrixHeader string
	// fullTexts and fullTextsIntro are the section of the studies whose full
	// text was not obtained.
	fullTexts, fullTextsIntro string

	// Names of enum values; a value outside a table is shown as it is.
	verdictNames   map[string]string // by the lower-case level
	consensusNames map[string]string // by the consensus status
	effectNames    map[model.EffectDirection]string
	tallyNames     map[string]string // by the critic's result_direction word
	// item writes a count with its name, "7 positive": the count is argument 1,
	// the name argument 2.
	item          string
	blindingNames map[model.Blinding]string

	// The computed lines: pooled estimate, imprecision, blinding.
	pooledSingle, pooledPlain, pooledConverted string
	ci, ciTo                                   string
	studies, participants, events, levels      forms
	design, outcome, notPooled, andMore        string // andMore takes the count
	strata                                     map[model.Stratum]string
	notRatedDown, ratedDown, serious           string

	sentence evidenceWords
	list     listWords
}

// evidenceWords are the parts of the evidence-base sentence.
type evidenceWords struct {
	// reviewed is the start of the sentence, without its separator.
	reviewed func(reviewed int) string
	// noneDirect closes the sentence when no study answers directly.
	noneDirect string
	// directHead says how many studies answer the question directly; repeat
	// says how many repeat reports were counted once.
	directHead func(direct int) string
	repeat     func(reports int) string
	// The nouns of the design groups.
	randomized, observational, reviews, other forms
	otherNote                                 string
	// The sizes of a group.
	sizeNone, sizeNoneAll, sizeNoneFor string // the last two take a count
	median, largest                    string
	// The pool.
	noPool, notPooledOne, pooledPrefix string
	studies, trials                    forms
	// The tail of the sentence; each takes a count.
	retracted, indirect, notAssessed string
}

// listWords are the parts of the list of direct studies.
type listWords struct {
	header string
	// moreFmt takes the number of rows left out.
	moreFmt string
	// review stands in the size column of a review.
	review      string
	notAssessed string
	pooled      string
	designs     map[model.StudyDesign]string
	directions  map[model.StudyDirection]string
}

// The keys of the enum tables: the verdict levels and the consensus statuses
// as the synthesis writes them.
const (
	verdictStrong       = "strong"
	verdictModerate     = "moderate"
	verdictWeak         = "weak"
	verdictInsufficient = "insufficient"

	consensusBroad        = "broad_agreement"
	consensusContested    = "contested"
	consensusEmerging     = "emerging"
	consensusNone         = "no_consensus"
	consensusInsufficient = "insufficient_data"
)

// cochraneName marks a Cochrane review; a proper noun, the same in every language.
const cochraneName = "Cochrane"

// lexiconFor returns the wording of a language. An empty language (a report
// written before the field existed) is English; so is any value this code does
// not know, which ParseLanguage keeps out at the boundary.
func lexiconFor(lang model.Language) lexicon {
	if lang == model.LangRU {
		return russian()
	}

	return english()
}

// count writes a number with the noun in the form it takes: "1 paper",
// "5 papers".
func (l lexicon) count(n int, f forms) string {
	return strconv.Itoa(n) + " " + f[l.formIndex(n)]
}

// two makes the forms of an English noun.
func two(one, many string) forms { return forms{one, many, many} }

// fixed makes the forms of a word that does not change with the count.
func fixed(word string) forms { return forms{word, word, word} }

// englishForm is the plural rule of English: 1 is singular, the rest plural.
func englishForm(n int) int {
	if n == 1 {
		return 0
	}

	return 2
}

// russianForm is the plural rule of Russian: 1, 21, 31 ... take the first
// form; 2-4, 22-24 ... the second; the rest, 11-14 included, the third.
func russianForm(n int) int {
	if n < 0 {
		n = -n
	}

	last, lastTwo := n%10, n%100

	switch {
	case last == 1 && lastTwo != 11:
		return 0
	case last >= 2 && last <= 4 && (lastTwo < 12 || lastTwo > 14):
		return 1
	}

	return 2
}
