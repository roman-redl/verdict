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

	// the article fixture lives with the converter that reads it
	//nolint:gosec // a fixed file name inside the jats package's testdata
	b, err := os.ReadFile("../jats/testdata/" + name)
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

func TestFullText(t *testing.T) {
	var calls atomic.Int32

	srv := fullTextServer(t, []rec{oaHit(sourceMED)}, http.StatusOK, readFixture(t, "article.xml"), &calls)
	defer srv.Close()

	got, err := NewWithBase(srv.URL).FullText(t.Context(), "https://doi.org/"+strings.ToUpper(doiOA), 0)

	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, pmcid, got.PMCID)
	assert.Empty(t, got.Reason)
	assert.True(t, strings.HasPrefix(got.Text, "## Tables\n"), "tables come first: %q", got.Text[:40])
	assert.Contains(t, got.Text, "Major bleeding | 3 (2.5) | 11 (9.2)a | 0.27 (0.08–0.95)")
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
	assert.Len(t, []rune(got.Text), 120)
	assert.True(t, strings.HasPrefix(full.Text, got.Text))
}

func TestFullTextUnavailable(t *testing.T) {
	tests := []struct {
		name       string
		hits       []rec
		wantPMCID  string
		wantReason string
	}{
		{"unknown DOI", nil, "", "DOI not indexed"},
		{"no PMCID", []rec{fullHit(doiOA, sourceMED, "", "N")}, "", "no PMCID"},
		{
			name: "a PMCID outside the open-access subset", hits: []rec{fullHit(doiOA, sourceMED, pmcid, "N")},
			wantPMCID: pmcid, wantReason: pmcid + " is not in the open-access subset",
		},
		{"a record of another DOI", []rec{fullHit("10.1/other", sourceMED, pmcid, "Y")}, "", "DOI not indexed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32

			srv := fullTextServer(t, tt.hits, http.StatusOK, nil, &calls)
			defer srv.Close()

			got, err := NewWithBase(srv.URL).FullText(t.Context(), doiOA, 1000)

			require.NoError(t, err, "no full text is data, not an error")
			assert.Empty(t, got.Text)
			assert.Equal(t, tt.wantPMCID, got.PMCID, "the PMCID is passed on even without a text")
			assert.Equal(t, tt.wantReason, got.Reason)
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
	assert.Empty(t, got.Text)
	assert.Equal(t, pmcid, got.PMCID)
	assert.Contains(t, got.Reason, "not served")

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

// A served article without a body is data: no text, and the reason says so.
func TestFullTextWithoutBody(t *testing.T) {
	var calls atomic.Int32

	srv := fullTextServer(t, []rec{oaHit(sourceMED)}, http.StatusOK,
		[]byte(`<article><front><article-meta/></front></article>`), &calls)
	defer srv.Close()

	got, err := NewWithBase(srv.URL).FullText(t.Context(), doiOA, 1000)

	require.NoError(t, err)
	assert.Empty(t, got.Text)
	assert.Equal(t, pmcid, got.PMCID)
	assert.Contains(t, got.Reason, "no body")
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
	assert.Contains(t, got.Text, "Major bleeding | 3 (2.5)")
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
