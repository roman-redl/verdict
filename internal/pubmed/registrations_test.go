package pubmed

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	doiRefluxA = "10.1136/bmj.a2664"
	doiRefluxB = "10.1136/bmj.f1908"
	doiNoBank  = "10.1016/s1473-3099(12)70081-6"

	// the E-utility paths the server answers
	pathSearch = "/esearch.fcgi"
	pathFetch  = "/efetch.fcgi"
)

// registryServer answers esearch with the PMIDs of the fixture and efetch with
// the fixture itself, and keeps the requests it saw.
type registryServer struct {
	*httptest.Server

	mu       sync.Mutex
	searches []url.Values
	fetches  []url.Values
}

func newRegistryServer(t *testing.T) *registryServer {
	t.Helper()

	rs := &registryServer{}
	xmlBody := fixture(t, "efetch.xml")

	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		defer rs.mu.Unlock()

		switch r.URL.Path {
		case pathSearch:
			rs.searches = append(rs.searches, r.URL.Query())
			_, _ = w.Write([]byte(`{"esearchresult":{"count":"3","idlist":["19074946","23599318","22682536","19074946"]}}`))
		case pathFetch:
			rs.fetches = append(rs.fetches, r.URL.Query())
			_, _ = w.Write([]byte(xmlBody))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(rs.Close)

	return rs
}

func TestRegistrations(t *testing.T) {
	rs := newRegistryServer(t)
	c := NewWithBase(rs.URL, testKey, "me@example.org")

	got, err := c.Registrations(t.Context(), []string{
		"https://doi.org/" + strings.ToUpper(doiRefluxA), doiRefluxB, doiNoBank, "10.1/not-in-pubmed",
	})
	require.NoError(t, err)

	assert.Equal(t, map[string][]string{
		doiRefluxA: {"ISRCTN15517081"},
		doiRefluxB: {"AB123456", "NCT01234567", "NCT01234568"},
	}, got, "keyed by normalized DOI; a record without data banks, an unknown DOI and an unrequested record are absent")

	require.Len(t, rs.searches, 1)
	term := rs.searches[0].Get("term")
	assert.Equal(t, `"10.1/not-in-pubmed"[AID] OR "10.1016/s1473-3099(12)70081-6"[AID] OR `+
		`"10.1136/bmj.a2664"[AID] OR "10.1136/bmj.f1908"[AID]`, term)
	assert.Equal(t, testKey, rs.searches[0].Get("api_key"))

	require.Len(t, rs.fetches, 1)
	f := rs.fetches[0]
	assert.Equal(t, "19074946,22682536,23599318", f.Get("id"), "each PMID once")
	assert.Equal(t, "xml", f.Get("retmode"))
	assert.Equal(t, "pubmed", f.Get("db"))
	assert.Equal(t, testKey, f.Get("api_key"))
}

func TestRegistrationsBatchesTheRequests(t *testing.T) {
	var (
		mu       sync.Mutex
		searches int
		fetched  []int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.URL.Path {
		case pathSearch:
			searches++
			// 20 DOIs make one query; every query finds 20 distinct records
			ids := make([]string, 0, doiBatch)
			for i := range doiBatch {
				ids = append(ids, fmt.Sprint(searches*100+i))
			}

			_, _ = w.Write([]byte(`{"esearchresult":{"count":"20","idlist":["` + strings.Join(ids, `","`) + `"]}}`))
		case pathFetch:
			fetched = append(fetched, len(strings.Split(r.URL.Query().Get("id"), ",")))
			_, _ = w.Write([]byte(`<PubmedArticleSet></PubmedArticleSet>`))
		}
	}))
	defer srv.Close()

	dois := make([]string, 0, 230)
	for i := range 230 {
		dois = append(dois, fmt.Sprintf("10.1/d%03d", i))
	}

	got, err := NewWithBase(srv.URL, "", "").Registrations(t.Context(), dois)
	require.NoError(t, err)

	assert.Empty(t, got)
	assert.Equal(t, 12, searches, "20 DOIs per esearch query")
	assert.Equal(t, []int{200, 40}, fetched, "at most 200 PMIDs per efetch")
}

func TestRegistrationsNoDOIsMakesNoRequest(t *testing.T) {
	rs := newRegistryServer(t)

	got, err := NewWithBase(rs.URL, "", "").Registrations(t.Context(), []string{"", `10.1/has"quote`})
	require.NoError(t, err)

	assert.Empty(t, got)
	assert.Empty(t, rs.searches)
	assert.Empty(t, rs.fetches)
}

func TestRegistrationsFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		search  string
		fetch   string
		wantErr string
	}{
		{"esearch rejects the query", `{"esearchresult":{"ERROR":"bad query"}}`, "", "esearch"},
		{
			"efetch answers something that is not XML", `{"esearchresult":{"count":"1","idlist":["1"]}}`,
			"not xml at all", "efetch: decoding",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == pathSearch {
					_, _ = w.Write([]byte(tc.search))
					return
				}

				_, _ = w.Write([]byte(tc.fetch))
			}))
			defer srv.Close()

			got, err := NewWithBase(srv.URL, "", "").Registrations(t.Context(), []string{doiRefluxA})

			require.ErrorContains(t, err, tc.wantErr)
			assert.Empty(t, got)
		})
	}
}
