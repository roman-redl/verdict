package pipeline

// Handwritten fakes of the pipeline's ports. Each is a struct of optional
// function fields: a nil function is the quiet default ("the source knows
// nothing"), a set one scripts the scenario. Calls are counted by key so a
// test can assert what was (not) asked.

import (
	"context"
	"sync"

	"github.com/roman-redl/verdict/internal/model"
)

// Values shared by the tests of this package.
const (
	designRCT = "rct"
	criticDOI = "10.9/critic"
)

// counter counts calls by key; safe for the parallel stage workers.
type counter struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *counter) add(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.n == nil {
		c.n = map[string]int{}
	}

	c.n[key]++
}

func (c *counter) get(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.n[key]
}

func (c *counter) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	sum := 0
	for _, v := range c.n {
		sum += v
	}

	return sum
}

type fakeWorks struct {
	calls  counter
	byDOI  func(doi string) (*model.Work, error)
	search func(query string, limit int) ([]model.Paper, error)
	cites  func(id string, limit int) ([]model.Work, error)
	byIDs  func(ids []string) ([]model.Work, error)
}

var _ Works = (*fakeWorks)(nil)

func (f *fakeWorks) ByDOI(_ context.Context, doi string) (*model.Work, error) {
	f.calls.add("ByDOI:" + doi)

	if f.byDOI == nil {
		return nil, nil //nolint:nilnil // the fake mirrors the port: an unknown DOI is (nil, nil)
	}

	return f.byDOI(doi)
}

func (f *fakeWorks) Search(_ context.Context, query string, limit int) ([]model.Paper, error) {
	f.calls.add("Search")

	if f.search == nil {
		return nil, nil
	}

	return f.search(query, limit)
}

func (f *fakeWorks) CitesWork(_ context.Context, id string, limit int) ([]model.Work, error) {
	f.calls.add("CitesWork:" + id)

	if f.cites == nil {
		return nil, nil
	}

	return f.cites(id, limit)
}

func (f *fakeWorks) ByIDs(_ context.Context, ids []string) ([]model.Work, error) {
	f.calls.add("ByIDs")

	if f.byIDs == nil {
		return nil, nil
	}

	return f.byIDs(ids)
}

type fakeCitations struct {
	calls         counter
	key           bool
	tallies       func(dois []string) (map[string]model.CitationTally, error)
	contradicting func(doi string) ([]string, error)
}

var _ Citations = (*fakeCitations)(nil)

func (f *fakeCitations) HasKey() bool { return f.key }

func (f *fakeCitations) Tallies(_ context.Context, dois []string) (map[string]model.CitationTally, error) {
	f.calls.add("Tallies")

	if f.tallies == nil {
		return map[string]model.CitationTally{}, nil
	}

	return f.tallies(dois)
}

func (f *fakeCitations) ContradictingSources(_ context.Context, doi string) ([]string, error) {
	f.calls.add("ContradictingSources:" + doi)

	if f.contradicting == nil {
		return nil, nil
	}

	return f.contradicting(doi)
}

type fakeNotices struct {
	calls   counter
	updates func(doi string) ([]model.Notice, error)
}

var _ Notices = (*fakeNotices)(nil)

func (f *fakeNotices) Updates(_ context.Context, doi string) ([]model.Notice, error) {
	f.calls.add("Updates:" + doi)

	if f.updates == nil {
		return nil, nil
	}

	return f.updates(doi)
}

type fakeScholar struct {
	calls  counter
	lookup func(dois []string) (map[string]model.ScholarRecord, error)
}

var _ Scholar = (*fakeScholar)(nil)

func (f *fakeScholar) Lookup(_ context.Context, dois []string) (map[string]model.ScholarRecord, error) {
	f.calls.add("Lookup")

	if f.lookup == nil {
		return map[string]model.ScholarRecord{}, nil
	}

	return f.lookup(dois)
}

type fakeAbstracts struct {
	calls     counter
	asked     [][]string // the DOIs of each call
	abstracts func(dois []string) (map[string]string, error)
}

var _ Abstracts = (*fakeAbstracts)(nil)

func (f *fakeAbstracts) Abstracts(_ context.Context, dois []string) (map[string]string, error) {
	f.calls.add("Abstracts")
	f.asked = append(f.asked, dois)

	if f.abstracts == nil {
		return map[string]string{}, nil
	}

	return f.abstracts(dois)
}

// fakeFullTexts serves the Europe PMC full texts: a canned text per DOI
// (a DOI absent from the map has none), or one error for every call.
type fakeFullTexts struct {
	calls counter
	texts map[string]string
	err   error
	maxes []int // the cap each call carried
}

var _ FullTexts = (*fakeFullTexts)(nil)

func (f *fakeFullTexts) FullText(_ context.Context, doi string, maxChars int) (string, error) {
	f.calls.add("FullText:" + doi)
	f.maxes = append(f.maxes, maxChars)

	return f.texts[doi], f.err
}

type fakeCommentary struct {
	calls counter
	key   bool
	note  func(doi string) (string, error)
}

var _ Commentary = (*fakeCommentary)(nil)

func (f *fakeCommentary) HasKey() bool { return f.key }

func (f *fakeCommentary) Note(_ context.Context, doi string) (string, error) {
	f.calls.add("Note:" + doi)

	if f.note == nil {
		return "", nil
	}

	return f.note(doi)
}

type fakeSearch struct {
	calls counter
	key   bool
	page  func(query string, page int) (model.SearchPage, error)
}

var _ SemanticSearch = (*fakeSearch)(nil)

func (f *fakeSearch) HasKey() bool { return f.key }

func (f *fakeSearch) Page(_ context.Context, query string, page int) (model.SearchPage, error) {
	f.calls.add("Page")

	if f.page == nil {
		return model.SearchPage{IsEnd: true}, nil
	}

	return f.page(query, page)
}

type fakePDFs struct {
	calls counter
	fetch func(url string) ([]byte, error)
}

var _ PDFSource = (*fakePDFs)(nil)

func (f *fakePDFs) Fetch(_ context.Context, url string, _ int64) ([]byte, error) {
	f.calls.add("Fetch:" + url)

	return f.fetch(url)
}

// fakePDFText returns a canned text for every document, or fails.
type fakePDFText struct {
	text string
	err  error
}

var _ PDFText = fakePDFText{}

func (f fakePDFText) Text([]byte, int) (string, error) { return f.text, f.err }

// scriptCompleter replies from a script and records the prompts it got.
type scriptCompleter struct {
	replies []string
	prompts []string
	calls   int
}

var _ Completer = (*scriptCompleter)(nil)

func (s *scriptCompleter) Name() string { return "script" }

func (s *scriptCompleter) Complete(_ context.Context, _, user string, _ CompleteOptions) (string, Usage, error) {
	s.prompts = append(s.prompts, user)
	s.calls++

	if s.calls > len(s.replies) {
		return "", Usage{}, nil // script exhausted: an empty answer
	}

	return s.replies[s.calls-1], Usage{InputTokens: 100, OutputTokens: 50}, nil
}

// work builds an OpenAlex-style record for a DOI.
func work(doi, title string) *model.Work {
	return &model.Work{DOI: "https://doi.org/" + doi, Title: title}
}
