package pubmed

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	testKey  = "secret-key-123"
	queryAny = "a[tiab]"
	journal  = "Journal Article"
)

func fixture(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile("testdata/" + name) //nolint:gosec // a fixed testdata file name chosen by the test
	require.NoError(t, err)

	return string(data)
}

// server answers every request with the body the function builds from the
// query, and records the queries.
func server(t *testing.T, body func(q url.Values) string) (*httptest.Server, *[]url.Values) {
	t.Helper()

	var seen []url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Query())
		_, _ = w.Write([]byte(body(r.URL.Query()))) // a test server: a failed write fails the client's assertions
	}))
	t.Cleanup(srv.Close)

	return srv, &seen
}

// constant is a body that does not depend on the query.
func constant(body string) func(url.Values) string { return func(url.Values) string { return body } }

func TestSearch(t *testing.T) {
	srv, seen := server(t, constant(fixture(t, "esearch.json")))
	c := NewWithBase(srv.URL, testKey, "me@example.org")

	hits, err := c.Search(t.Context(), `(a[tiab] OR "b c"[Mesh]) AND d*[tiab]`, 3)
	require.NoError(t, err)

	assert.Equal(t, model.PubMedHits{PMIDs: []string{"28827081", "37308760", "8805734"}, Total: 1533}, hits)

	require.Len(t, *seen, 1)
	q := (*seen)[0]
	assert.Equal(t, `(a[tiab] OR "b c"[Mesh]) AND d*[tiab]`, q.Get("term"), "the query survives the encoding")
	assert.Equal(t, "pubmed", q.Get("db"))
	assert.Equal(t, "3", q.Get("retmax"))
	assert.Equal(t, "relevance", q.Get("sort"))
	assert.Equal(t, "json", q.Get("retmode"))
	assert.Equal(t, testKey, q.Get("api_key"))
	assert.Equal(t, "me@example.org", q.Get("email"))
	assert.Equal(t, toolName, q.Get("tool"))
}

func TestSearchWithoutKeyOrEmailSendsNeither(t *testing.T) {
	srv, seen := server(t, constant(fixture(t, "esearch.json")))

	_, err := NewWithBase(srv.URL, "", "").Search(t.Context(), queryAny, 3)
	require.NoError(t, err)

	q := (*seen)[0]
	assert.False(t, q.Has("api_key"))
	assert.False(t, q.Has("email"))
}

func TestSearchErrors(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		limit   int
		body    string
		wantErr string
	}{
		{name: "empty query", query: " ", limit: 5, wantErr: "empty PubMed query"},
		{name: "no limit", query: queryAny, limit: 0, wantErr: "must be positive"},
		{
			name: "an error in a 200 answer", query: "(((", limit: 5,
			body:    `{"esearchresult":{"ERROR":"Search Backend failed: Empty Term in the request"}}`,
			wantErr: "Empty Term",
		},
		{
			name: "an unreadable count", query: queryAny, limit: 5,
			body:    `{"esearchresult":{"count":"many","idlist":[]}}`,
			wantErr: "hit count",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := server(t, constant(tt.body))

			_, err := NewWithBase(srv.URL, "", "").Search(t.Context(), tt.query, tt.limit)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestSearchZeroHits(t *testing.T) {
	srv, _ := server(t, constant(`{"esearchresult":{"count":"0","retmax":"0","retstart":"0","idlist":[]}}`))

	hits, err := NewWithBase(srv.URL, "", "").Search(t.Context(), queryAny, 5)
	require.NoError(t, err)
	assert.Zero(t, hits.Total)
	assert.Empty(t, hits.PMIDs)
}

func TestSummaries(t *testing.T) {
	srv, seen := server(t, constant(fixture(t, "esummary.json")))

	got, err := NewWithBase(srv.URL, "", "").Summaries(t.Context(), []string{"28827081", "8805734", "99999999"})
	require.NoError(t, err)

	want := []model.PubMedRecord{
		{
			PMID: "28827081", Title: "Management of Gastroesophageal Reflux Disease", Year: 2018,
			DOI: "10.1053/j.gastro.2017.07.049", PMCID: "PMC5775064",
			PubTypes: []string{journal, "Research Support, Non-U.S. Gov't", "Review"},
		},
		{PMID: "8805734", Title: "Gastroesophageal reflux disease", Year: 1996, PubTypes: []string{journal, "Review"}},
	}
	assert.Equal(t, want, got, "the DOI is normalized, the title loses its period, a PMID with an error is absent")

	require.Len(t, *seen, 1)
	assert.Equal(t, "28827081,8805734,99999999", (*seen)[0].Get("id"))
}

func TestSummariesBatches(t *testing.T) {
	var sizes []int

	srv, _ := server(t, func(q url.Values) string {
		ids := strings.Split(q.Get("id"), ",")
		sizes = append(sizes, len(ids))

		return `{"result":{"uids":["` + ids[0] + `"],"` + ids[0] + `":{"uid":"` + ids[0] + `","title":"T","pubdate":"2020"}}}`
	})

	pmids := make([]string, 450)
	for i := range pmids {
		pmids[i] = fmt.Sprint(1000 + i)
	}

	got, err := NewWithBase(srv.URL, "", "").Summaries(t.Context(), pmids)
	require.NoError(t, err)

	assert.Equal(t, []int{200, 200, 50}, sizes, "at most 200 PMIDs per request")
	assert.Len(t, got, 3)
	assert.Equal(t, 2020, got[0].Year)
}

func TestSummariesNoPMIDsMakesNoRequest(t *testing.T) {
	srv, seen := server(t, constant(""))

	got, err := NewWithBase(srv.URL, "", "").Summaries(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, *seen)
}

func TestSummariesTopLevelError(t *testing.T) {
	srv, _ := server(t, constant(`{"error":"API rate limit exceeded"}`))

	_, err := NewWithBase(srv.URL, "", "").Summaries(t.Context(), []string{"1"})
	require.ErrorContains(t, err, "rate limit")
}

func TestYear(t *testing.T) {
	tests := []struct {
		name  string
		dates []string
		want  int
	}{
		{"month name", []string{"2018 Jan"}, 2018},
		{"a season", []string{"2019 Spring"}, 2019},
		{"a range", []string{"2019 Jan-Feb"}, 2019},
		{"falls back to the sort date", []string{"", "2001/05/01 00:00"}, 2001},
		{"not a year", []string{"Spring 2019"}, 0},
		{"none", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, year(tt.dates...))
		})
	}
}

// A transport error quotes the request URL; the key must not travel with it.
func TestRedactStripsTheKeyFromTransportErrors(t *testing.T) {
	c := NewWithBase("http://example.invalid", testKey, "")
	cause := errors.New("connection refused")
	err := fmt.Errorf("requesting pubmed: %w", &url.Error{
		Op: "Get", URL: "http://example.invalid/esearch.fcgi?api_key=" + testKey + "&db=pubmed", Err: cause,
	})

	got := c.redact(err)

	assert.NotContains(t, fmt.Sprint(got), testKey)
	assert.Contains(t, fmt.Sprint(got), "api_key="+redacted)
	require.ErrorIs(t, got, cause, "the error chain stays intact")

	plain := errors.New("no URL here")
	assert.Equal(t, plain, c.redact(plain))
}
