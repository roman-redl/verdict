package europepmc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	doiOA = "10.1000/example.2020.1"
	pmcid = "PMC1234567"

	searchPath = "/search"
)

// fullHit is a search hit with the full-text fields; an empty pmc is a record
// without a PMCID.
func fullHit(doi, source, pmc, openAccess string) rec {
	return rec{"doi": doi, "source": source, "pmcid": pmc, "isOpenAccess": openAccess}
}

// oaHit is a search hit of an open-access article with a PMCID.
func oaHit(source string) rec { return fullHit(doiOA, source, pmcid, "Y") }

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	//nolint:gosec // a fixed file name inside testdata
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)

	return b
}

// fullTextServer answers the DOI search with the hits and the XML request
// with the given status and body; xmlCalls counts the XML requests.
func fullTextServer(t *testing.T, hits []rec, status int, body []byte, xmlCalls *atomic.Int32) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			assert.Equal(t, `DOI:"`+doiOA+`"`, r.URL.Query().Get("query"))
			reply(w, hits...)
		case "/" + pmcid + "/fullTextXML":
			xmlCalls.Add(1)
			w.WriteHeader(status)
			_, _ = w.Write(body) // a test server: a failed write fails the client's assertions
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
}

// The converter puts the tables first, then the results, then the methods,
// then the rest; the front matter, the references, the formulas and the
// citation markers are gone, the table keeps its rows and footnotes.
func TestFullTextFromXMLGolden(t *testing.T) {
	got, err := fullTextFromXML(readFixture(t, "article.xml"))

	require.NoError(t, err)

	want := strings.TrimSuffix(string(readFixture(t, "article.golden")), "\n")
	assert.Equal(t, want, got)

	for _, noise := range []string{"FRONT-MATTER", "REFERENCE ENTRY", "We thank", "mml", "RR RR"} {
		assert.NotContains(t, got, noise)
	}
}

func TestFullTextFromXMLShapes(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want string
	}{
		{
			name: "no body: nothing to read",
			xml:  `<article><front><article-meta/></front></article>`,
			want: "",
		},
		{
			name: "findings and outcomes sections count as results",
			xml: `<article><body><sec><title>Background</title><p>Why.</p></sec>` +
				`<sec><title>Findings</title><p>What.</p></sec></body></article>`,
			want: "## Findings\nWhat.\n\n## Background\nWhy.",
		},
		{
			name: "the sec-type decides when the title is a generic one",
			xml: `<article><body><sec><title>Background</title><p>Why.</p></sec>` +
				`<sec sec-type="results"><title>Study</title><p>What.</p></sec></body></article>`,
			want: "## Study\nWhat.\n\n## Background\nWhy.",
		},
		{
			name: "text in the body without sections",
			xml:  `<article><body><p>A letter.</p><p>Second paragraph.</p></body></article>`,
			want: "A letter.\nSecond paragraph.",
		},
		{
			name: "a table without a caption, a spanning cell, an empty row",
			xml: `<article><body><sec><title>Results</title><table-wrap><table><tbody>` +
				`<tr><th colspan="2">Group</th><th>N</th></tr><tr><td/><td/></tr><tr><td>A</td><td>B</td><td>3</td></tr>` +
				`</tbody></table></table-wrap></sec></body></article>`,
			want: "## Tables\nGroup |  | N\nA | B | 3\n\n## Results",
		},
		{
			name: "table wrappers in a group, the label without a full stop",
			xml: `<article><body><sec><title>Methods</title><table-wrap-group>` +
				`<table-wrap><label>Table 2</label><caption><title>Doses</title></caption>` +
				`<table><tr><td>x</td></tr></table></table-wrap></table-wrap-group></sec></body></article>`,
			want: "## Tables\nTable 2. Doses\nx\n\n## Methods",
		},
		{
			name: "a table wrapper inside a paragraph is lifted out of the prose",
			xml: `<article><body><sec><title>Results</title><p>Before.<table-wrap><label>Table 1</label>` +
				`<caption><p>Cap</p></caption><table><tr><td>x</td><td>y</td></tr></table></table-wrap> After.</p>` +
				`</sec></body></article>`,
			want: "## Tables\nTable 1. Cap\nx | y\n\n## Results\nBefore. After.",
		},
		{
			name: "tables kept in a floats group outside the body",
			xml: `<article><body><sec><title>Results</title><p>See Table 4.</p></sec></body>` +
				`<floats-group><table-wrap><label>Table 4</label><caption><p>Effects</p></caption>` +
				`<table><tr><td>OR</td><td>1.06</td></tr></table></table-wrap></floats-group></article>`,
			want: "## Tables\nTable 4. Effects\nOR | 1.06\n\n## Results\nSee Table 4.",
		},
		{
			name: "numbers and decimals keep their spacing",
			xml:  `<article><body><sec><title>Results</title><p>P &lt; .001 and from .5 to 1.</p></sec></body></article>`,
			want: "## Results\nP < .001 and from .5 to 1.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fullTextFromXML([]byte(tt.xml))

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFullTextFromXMLRejectsNonJATS(t *testing.T) {
	for name, in := range map[string]string{
		"an HTML error page": `<html><body>Not found</body></html>`,
		"not XML at all":     `{"status": 500}`,
		"a broken document":  `<article><body><sec>`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fullTextFromXML([]byte(in))
			require.Error(t, err)
		})
	}
}

func TestFullText(t *testing.T) {
	var calls atomic.Int32

	srv := fullTextServer(t, []rec{oaHit(sourceMED)}, http.StatusOK, readFixture(t, "article.xml"), &calls)
	defer srv.Close()

	got, err := NewWithBase(srv.URL).FullText(t.Context(), "https://doi.org/"+strings.ToUpper(doiOA), 0)

	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())
	assert.True(t, strings.HasPrefix(got, "## Tables\n"), "tables come first: %q", got[:40])
	assert.Contains(t, got, "Major bleeding | 3 (2.5) | 11 (9.2)a | 0.27 (0.08–0.95)")
}

// The cap counts characters, not bytes, and never splits one.
func TestFullTextCap(t *testing.T) {
	var calls atomic.Int32

	srv := fullTextServer(t, []rec{oaHit(sourceMED)}, http.StatusOK, readFixture(t, "article.xml"), &calls)
	defer srv.Close()

	c := NewWithBase(srv.URL)
	full, err := c.FullText(t.Context(), doiOA, 0)
	require.NoError(t, err)

	got, err := c.FullText(t.Context(), doiOA, 120)

	require.NoError(t, err)
	assert.Len(t, []rune(got), 120)
	assert.True(t, strings.HasPrefix(full, got))

	assert.Equal(t, "ab–", clip("ab–cd", 3), "a multi-byte character is kept whole")
	assert.Equal(t, "short", clip("short", 100))
}

func TestFullTextUnavailable(t *testing.T) {
	tests := []struct {
		name string
		hits []rec
	}{
		{"unknown DOI", nil},
		{"no PMCID", []rec{fullHit(doiOA, sourceMED, "", "N")}},
		{"a PMCID outside the open-access subset", []rec{fullHit(doiOA, sourceMED, pmcid, "N")}},
		{"a record of another DOI", []rec{fullHit("10.1/other", sourceMED, pmcid, "Y")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32

			srv := fullTextServer(t, tt.hits, http.StatusOK, nil, &calls)
			defer srv.Close()

			got, err := NewWithBase(srv.URL).FullText(t.Context(), doiOA, 1000)

			require.NoError(t, err, "no full text is data, not an error")
			assert.Empty(t, got)
			assert.Zero(t, calls.Load(), "the XML is not requested without an open-access PMCID")
		})
	}
}

// A 404 for the XML means "not served" (data); any other HTTP status, a
// search failure and a response that is not JATS are errors.
func TestFullTextFailures(t *testing.T) {
	var calls atomic.Int32

	hits := []rec{oaHit(sourceMED)}

	srv := fullTextServer(t, hits, http.StatusNotFound, []byte("not found"), &calls)
	got, err := NewWithBase(srv.URL).FullText(t.Context(), doiOA, 1000)
	srv.Close()

	require.NoError(t, err)
	assert.Empty(t, got)

	srv = fullTextServer(t, hits, http.StatusForbidden, []byte("denied"), &calls)
	_, err = NewWithBase(srv.URL).FullText(t.Context(), doiOA, 1000)
	srv.Close()

	require.ErrorContains(t, err, "HTTP 403")

	srv = fullTextServer(t, hits, http.StatusOK, []byte("<html>maintenance</html>"), &calls)
	_, err = NewWithBase(srv.URL).FullText(t.Context(), doiOA, 1000)
	srv.Close()

	require.ErrorContains(t, err, "converting the "+pmcid+" full text")

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad query", http.StatusBadRequest)
	}))
	defer bad.Close()

	_, err = NewWithBase(bad.URL).FullText(t.Context(), doiOA, 1000)

	require.ErrorContains(t, err, "HTTP 400")
}

// A transient failure of the search or of the XML request (503, 429) is
// retried by the shared HTTP layer and the full text still arrives.
func TestFullTextRetriesTransientFailures(t *testing.T) {
	var searchCalls, xmlCalls atomic.Int32

	xml := readFixture(t, "article.xml")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			if searchCalls.Add(1) == 1 {
				http.Error(w, "Search service is temporarily unavailable", http.StatusServiceUnavailable)
				return
			}

			reply(w, oaHit(sourcePMC))
		case "/" + pmcid + "/fullTextXML":
			if xmlCalls.Add(1) == 1 {
				http.Error(w, "slow down", http.StatusTooManyRequests)
				return
			}

			_, _ = w.Write(xml) // a test server: a failed write fails the client's assertions
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	got, err := NewWithBase(srv.URL).FullText(t.Context(), doiOA, 0)

	require.NoError(t, err)
	assert.Contains(t, got, "Major bleeding | 3 (2.5)")
	assert.Equal(t, int32(2), searchCalls.Load())
	assert.Equal(t, int32(2), xmlCalls.Load())
}

// A client error other than 429 is final: one request, no retries.
func TestFullTextDoesNotRetryClientErrors(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var searchCalls, xmlCalls atomic.Int32

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == searchPath {
					searchCalls.Add(1)
					reply(w, oaHit(sourceMED))

					return
				}

				xmlCalls.Add(1)
				http.Error(w, "no", status)
			}))
			defer srv.Close()

			_, _ = NewWithBase(srv.URL).FullText(t.Context(), doiOA, 0) // the outcome is covered by TestFullTextFailures

			assert.Equal(t, int32(1), searchCalls.Load())
			assert.Equal(t, int32(1), xmlCalls.Load())
		})
	}

	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "bad query", http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := NewWithBase(srv.URL).FullText(t.Context(), doiOA, 0)

	require.ErrorContains(t, err, "HTTP 400")
	assert.Equal(t, int32(1), calls.Load(), "a bad search request is not repeated")
}

// Among several records of one DOI the open-access one wins, the journal
// record before the PMC one.
func TestOpenAccessPMCID(t *testing.T) {
	rich := func(source, id, oa string) record {
		return record{DOI: doiOA, Source: source, PMCID: id, IsOpenAccess: oa}
	}

	tests := []struct {
		name    string
		records []record
		want    string
	}{
		{"none", nil, ""},
		{"journal record first", []record{rich(sourcePMC, "PMC2", "Y"), rich(sourceMED, "PMC1", "Y")}, "PMC1"},
		{
			"a preprint after the journal record",
			[]record{rich(sourcePPR, "PMC3", "Y"), rich(sourceMED, "PMC1", "Y")},
			"PMC1",
		},
		{
			"the journal record is not open access",
			[]record{rich(sourceMED, "PMC1", "N"), rich(sourcePMC, "PMC2", "Y")},
			"PMC2",
		},
		{"a preprint alone", []record{rich(sourcePPR, "PMC3", "Y")}, "PMC3"},
		{
			"the DOI is compared normalized",
			[]record{{DOI: "10.1000/EXAMPLE.2020.1", PMCID: "PMC4", IsOpenAccess: "Y"}},
			"PMC4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, openAccessPMCID(tt.records, "doi:"+doiOA))
		})
	}
}

// Fuzz: the converter takes whatever the network gives and must not panic.
func FuzzFullTextFromXML(f *testing.F) {
	f.Add([]byte(`<article><body><sec><title>Results</title><p>x</p></sec></body></article>`))
	f.Add([]byte(`<article><body><table-wrap><table><tr><td colspan="99999999999">`))
	f.Add([]byte(`<?xml version="1.0"?><!DOCTYPE a [<!ENTITY e "x">]><article>&e;&nbsp;</article>`))

	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = fullTextFromXML(data) // only a panic is a failure here
	})
}
