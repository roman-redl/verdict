package europepmc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	doiA = "10.1016/j.ejrad.2018.01.022"
	doiB = "10.1016/s1473-3099(12)70081-6"
)

// rec is a search hit as the API sends it.
type rec map[string]string

const sourcePPR = "PPR" // a preprint record

// hit builds a search hit.
func hit(doi, source, abstract string) rec {
	return rec{"doi": doi, "source": source, "abstractText": abstract}
}

// reply answers a search with the given hits.
func reply(w http.ResponseWriter, hits ...rec) {
	body := map[string]any{"hitCount": len(hits), "resultList": map[string]any{"result": hits}}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body) // a test server: a failed write fails the client's assertions
}

func TestAbstractsBuildsBatchQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/search", r.URL.Path)

		q := r.URL.Query()
		assert.Equal(t, `DOI:"10.1016/j.ejrad.2018.01.022" OR DOI:"10.1016/s1473-3099(12)70081-6"`, q.Get("query"))
		assert.Equal(t, "core", q.Get("resultType"))
		assert.Equal(t, "json", q.Get("format"))
		assert.Equal(t, "100", q.Get("pageSize"))

		reply(w)
	}))
	defer srv.Close()

	// Prefixed and upper-case input reaches the query normalized.
	got, err := NewWithBase(srv.URL).Abstracts(t.Context(),
		[]string{"https://doi.org/10.1016/J.EJRAD.2018.01.022", "doi:" + strings.ToUpper(doiB)})

	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestAbstractsMatchesDOIsByNormalizedForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reply(w,
			hit("10.1016/J.EJRAD.2018.01.022", sourceMED, "<h4>Aim</h4>First."),
			hit("https://doi.org/"+doiB, sourceMED, "Second."),
			hit("10.9/unrelated", sourceMED, "Not asked for."),
			hit("", sourceMED, "A record without a DOI."),
		)
	}))
	defer srv.Close()

	got, err := NewWithBase(srv.URL).Abstracts(t.Context(), []string{doiA, doiB})

	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		doiA:             "Aim\nFirst.",
		doiB:             "Second.",
		"10.9/unrelated": "Not asked for.",
	}, got, "records are keyed by normalized DOI; the caller picks the ones it asked for")
}

func TestAbstractsPicksOneRecordPerDOI(t *testing.T) {
	tests := []struct {
		name string
		hits []rec
		want string // "" = the DOI is absent from the result
	}{
		{
			name: "journal record beats a preprint",
			hits: []rec{hit(doiA, sourcePPR, "preprint"), hit(doiA, sourceMED, "journal")},
			want: "journal",
		},
		{
			name: "the record with an abstract beats an empty MED one",
			hits: []rec{hit(doiA, sourceMED, ""), hit(doiA, sourcePPR, "preprint")},
			want: "preprint",
		},
		{
			name: "PMC beats a preprint",
			hits: []rec{hit(doiA, sourcePPR, "preprint"), hit(doiA, sourcePMC, "archive")},
			want: "archive",
		},
		{
			name: "first of equals",
			hits: []rec{hit(doiA, sourcePPR, "one"), hit(doiA, sourcePPR, "two")},
			want: "one",
		},
		{
			name: "markup only is empty",
			hits: []rec{hit(doiA, sourceMED, "<h4> </h4>")},
			want: "",
		},
		{
			name: "no abstract at all",
			hits: []rec{hit(doiA, sourceMED, "")},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reply(w, tt.hits...)
			}))
			defer srv.Close()

			got, err := NewWithBase(srv.URL).Abstracts(t.Context(), []string{doiA})

			require.NoError(t, err)

			if tt.want == "" {
				assert.NotContains(t, got, doiA)
				return
			}

			assert.Equal(t, tt.want, got[doiA])
		})
	}
}

// More DOIs than one URL should carry go out in several requests.
func TestAbstractsSplitsIntoBatches(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		terms := strings.Split(r.URL.Query().Get("query"), " OR ")
		assert.LessOrEqual(t, len(terms), batchSize)

		hits := make([]rec, 0, len(terms))

		for _, term := range terms {
			doi := strings.TrimSuffix(strings.TrimPrefix(term, `DOI:"`), `"`)
			hits = append(hits, hit(doi, sourceMED, "abstract of "+doi))
		}

		reply(w, hits...)
	}))
	defer srv.Close()

	dois := make([]string, 45)
	for i := range dois {
		dois[i] = fmt.Sprintf("10.1/p%d", i)
	}

	got, err := NewWithBase(srv.URL).Abstracts(t.Context(), dois)

	require.NoError(t, err)
	assert.EqualValues(t, 3, calls.Load())
	assert.Len(t, got, 45)
	assert.Equal(t, "abstract of 10.1/p44", got["10.1/p44"])
}

func TestAbstractsWithoutDOIsMakesNoRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("no request expected")
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL)

	got, err := c.Abstracts(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)

	// DOIs that cannot be put into the query phrase leave nothing to ask.
	got, err = c.Abstracts(t.Context(), []string{`10.1/with"quote`, `10.1/back\slash`, " "})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestAbstractsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad query", http.StatusBadRequest)
	}))
	defer srv.Close()

	got, err := NewWithBase(srv.URL).Abstracts(t.Context(), []string{doiA})

	require.ErrorContains(t, err, "europepmc")
	require.ErrorContains(t, err, "400")
	assert.Empty(t, got)
}

// A failing batch does not take away the abstracts of the earlier ones.
func TestAbstractsKeepsEarlierBatchesOnError(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 2 {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}

		reply(w, hit("10.1/first", sourceMED, "kept"))
	}))
	defer srv.Close()

	dois := make([]string, batchSize+1)
	for i := range dois {
		dois[i] = fmt.Sprintf("10.1/p%d", i)
	}

	got, err := NewWithBase(srv.URL).Abstracts(t.Context(), dois)

	require.Error(t, err)
	assert.Equal(t, map[string]string{"10.1/first": "kept"}, got)
}

func TestAbstractsMalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"resultList":`)) // a test server: the client's decoding error is the assertion
	}))
	defer srv.Close()

	_, err := NewWithBase(srv.URL).Abstracts(t.Context(), []string{doiA})

	require.ErrorContains(t, err, "decoding the response")
}

func TestNewUsesTheProductionBase(t *testing.T) {
	assert.Equal(t, defaultBase, New().base)
}
