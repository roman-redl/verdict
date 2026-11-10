package pmc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testDOI  = "10.1000/example.2020.1"
	testPMC  = "PMC7745181"
	idPath   = "/articles"
	fetchURI = "/efetch.fcgi"

	// frontMatterOnly is what efetch returns for an article whose publisher
	// does not allow downloading the full text: the metadata and the abstract,
	// no body.
	frontMatterOnly = `<?xml version="1.0" ?><pmc-articleset><article>` +
		`<!--The publisher of this article does not allow downloading of the full text in XML form.-->` +
		`<front><article-meta><abstract><p>Abstract only.</p></abstract></article-meta></front></article></pmc-articleset>`
)

// bodyXML is an article with a body long enough to count as a full text.
func bodyXML(paragraph string) string {
	return `<?xml version="1.0" ?><pmc-articleset><article><front><article-meta/></front><body>` +
		`<sec><title>Results</title><p>` + strings.Repeat(paragraph, 40) + `</p></sec></body></article></pmc-articleset>`
}

func newServer(t *testing.T, idconv, efetch func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case idPath:
			idconv(w, r)
		case fetchURI:
			efetch(w, r)
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
}

func TestPMCID(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "found",
			body: `{"status":"ok","records":[{"doi":"` + testDOI + `","pmcid":"PMC7745181","pmid":33301246}]}`,
			want: testPMC,
		},
		{
			name: "the prefix is normalized",
			body: `{"records":[{"doi":"` + testDOI + `","pmcid":"pmc7745181"}]}`,
			want: testPMC,
		},
		{
			name: "an unknown DOI is a record with an error status and no PMCID",
			body: `{"records":[{"doi":"` + testDOI + `","status":"error","errmsg":"Identifier not found in PMC"}]}`,
			want: "",
		},
		{name: "no records", body: `{"records":[]}`, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotQuery map[string]string

			srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				gotQuery = map[string]string{
					"ids": q.Get("ids"), "format": q.Get("format"), "tool": q.Get("tool"), "email": q.Get("email"),
				}

				_, _ = w.Write([]byte(tt.body)) // a test server: a failed write fails the client's assertions
			}, nil)
			defer srv.Close()

			c := NewWithBases(srv.URL, srv.URL, "me@example.org")

			got, err := c.PMCID(t.Context(), "https://doi.org/"+strings.ToUpper(testDOI))

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, map[string]string{
				"ids": testDOI, "format": "json", "tool": "verdict", "email": "me@example.org",
			}, gotQuery, "the DOI goes out normalized, with the tool name and the e-mail")
		})
	}
}

func TestPMCIDWithoutEmailSendsNone(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.False(t, r.URL.Query().Has("email"))

		_, _ = w.Write([]byte(`{"records":[]}`))
	}, nil)
	defer srv.Close()

	_, err := NewWithBases(srv.URL, srv.URL, "").PMCID(t.Context(), testDOI)

	require.NoError(t, err)
}

func TestPMCIDErrors(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}, nil)
	defer srv.Close()

	c := NewWithBases(srv.URL, srv.URL, "")

	_, err := c.PMCID(t.Context(), testDOI)
	require.ErrorContains(t, err, "HTTP 400")

	got, err := c.PMCID(t.Context(), "  ")
	require.NoError(t, err)
	assert.Empty(t, got, "no DOI, no request")
}

func TestFullText(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		maxChars  int
		wantText  string // a substring; empty means no text at all
		wantEmpty bool
	}{
		{name: "an article with a body", body: bodyXML("HR 0.8 (95% CI 0.6-1.0). "), wantText: "## Results\nHR 0.8"},
		{name: "front matter only: the publisher does not allow the download", body: frontMatterOnly, wantEmpty: true},
		{
			name:      "a stub body is not a full text",
			body:      `<pmc-articleset><article><body><p>Short note.</p></body></article></pmc-articleset>`,
			wantEmpty: true,
		},
		{name: "the cap is applied", body: bodyXML("HR 0.8 (95% CI 0.6-1.0). "), maxChars: 100, wantText: "## Results"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				assert.Equal(t, "pmc", q.Get("db"))
				assert.Equal(t, testPMC, q.Get("id"))
				assert.Equal(t, "xml", q.Get("rettype"))

				_, _ = w.Write([]byte(tt.body))
			})
			defer srv.Close()

			got, err := NewWithBases(srv.URL, srv.URL, "").FullText(t.Context(), "pmc7745181", tt.maxChars)

			require.NoError(t, err)

			if tt.wantEmpty {
				assert.Empty(t, got)
				return
			}

			assert.Contains(t, got, tt.wantText)

			if tt.maxChars > 0 {
				assert.Len(t, []rune(got), tt.maxChars)
			}
		})
	}
}

func TestFullTextErrors(t *testing.T) {
	tests := []struct {
		name   string
		pmcid  string
		status int
		body   string
		want   string
	}{
		{name: "not a PMCID", pmcid: "nonsense", status: http.StatusOK, want: "is not a PMCID"},
		{name: "an HTTP error", pmcid: testPMC, status: http.StatusBadRequest, body: "bad id", want: "HTTP 400"},
		{
			name: "an answer that is not JATS", pmcid: testPMC, status: http.StatusOK,
			body: "<html>maintenance</html>", want: "converting the " + testPMC + " XML",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t, nil, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})
			defer srv.Close()

			_, err := NewWithBases(srv.URL, srv.URL, "").FullText(t.Context(), tt.pmcid, 1000)

			require.ErrorContains(t, err, tt.want)
		})
	}
}
