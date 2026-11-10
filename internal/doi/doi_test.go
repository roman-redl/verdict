package doi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDOI = "10.1056/nejmoa2034577"

// handle is the doi.org answer for a DOI: the URL value sits among the
// administrative ones, whose data is an object.
const handle = `{"responseCode":1,"handle":"10.1056/NEJMoa2034577","values":[` +
	`{"index":100,"type":"HS_ADMIN","data":{"format":"admin","value":{"handle":"0.na/10.1056","index":200}}},` +
	`{"index":1,"type":"URL","data":{"format":"string","value":"http://www.nejm.org/doi/10.1056/NEJMoa2034577"}}]}`

func TestLandingURL(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name: "the registered URL", status: http.StatusOK, body: handle,
			want: "http://www.nejm.org/doi/10.1056/NEJMoa2034577",
		},
		{name: "an unknown DOI", status: http.StatusNotFound, body: `{"responseCode":100}`, want: ""},
		{name: "a handle without a URL value", status: http.StatusOK, body: `{"responseCode":1,"values":[]}`, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path

				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body)) // a test server: a failed write fails the client's assertions
			}))
			defer srv.Close()

			got, err := NewWithBase(srv.URL).LandingURL(t.Context(), "https://doi.org/10.1056/NEJMoa2034577")

			require.NoError(t, err, "an unknown DOI is data, not an error")
			assert.Equal(t, tt.want, got)
			assert.Equal(t, "/api/handles/"+testDOI, gotPath, "the DOI goes out normalized, its slash kept")
		})
	}
}

func TestLandingURLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	r := NewWithBase(srv.URL)

	_, err := r.LandingURL(t.Context(), testDOI)
	require.ErrorContains(t, err, "HTTP 400")

	got, err := r.LandingURL(t.Context(), " ")
	require.NoError(t, err)
	assert.Empty(t, got, "no DOI, no request")

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[{"type":"URL","data":{"value":{"not":"a string"}}}]}`))
	}))
	defer bad.Close()

	_, err = NewWithBase(bad.URL).LandingURL(t.Context(), testDOI)
	require.ErrorContains(t, err, "reading the URL registered for "+testDOI)
}
