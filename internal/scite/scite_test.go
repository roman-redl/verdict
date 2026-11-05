package scite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const doiA = "10.1/a"

// tallyOf builds one DOI's entry of a tallies response.
func tallyOf(total, supporting, contradicting, mentioning int) map[string]int {
	return map[string]int{
		"total": total, "supporting": supporting, "contradicting": contradicting, "mentioning": mentioning,
	}
}

// Tallies must survive both of Scite's historical response formats.
func TestTalliesFormats(t *testing.T) {
	tests := []struct {
		name    string
		body    map[string]any
		wantDOI string
		want    [2]int // {total, contradicting}
	}{
		{
			name: "flat",
			body: map[string]any{
				doiA: tallyOf(5, 3, 1, 1),
			},
			wantDOI: doiA,
			want:    [2]int{5, 1},
		},
		{
			name: "tallies-envelope",
			body: map[string]any{
				"tallies": map[string]any{
					"10.2/b": tallyOf(9, 8, 0, 1),
				},
			},
			wantDOI: "10.2/b",
			want:    [2]int{9, 0},
		},
		{
			name: "dois-envelope",
			body: map[string]any{
				"dois": map[string]any{
					"10.3/C": tallyOf(2, 1, 1, 0),
				},
			},
			wantDOI: "10.3/c",
			want:    [2]int{2, 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(tt.body)
			}))
			defer srv.Close()

			c := NewWithBase(srv.URL, "")

			got, err := c.Tallies(context.Background(), []string{tt.wantDOI})
			require.NoError(t, err)

			require.Contains(t, got, tt.wantDOI)

			tl := got[tt.wantDOI]
			assert.Equal(t, tt.want[0], tl.Total, "%s: total", tt.wantDOI)
			assert.Equal(t, tt.want[1], tl.Contradicting, "%s: contradicting", tt.wantDOI)
			assert.Equal(t, tt.wantDOI, tl.DOI, "the tally carries the normalized DOI")
		})
	}
}

// The request body is a bare JSON array (a {"dois": [...]} envelope is
// rejected by the API with 400), authorized by a bearer key when there is one.
func TestTalliesRequest(t *testing.T) {
	var gotAuth string

	var gotBody []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/tallies", r.URL.Path)

		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		_, _ = w.Write([]byte(`{"tallies":{"10.1/a":{"total":1}}}`))
	}))
	defer srv.Close()

	_, err := NewWithBase(srv.URL, "key-1").Tallies(context.Background(), []string{doiA, "10.1/b"})
	require.NoError(t, err)
	assert.Equal(t, "Bearer key-1", gotAuth)
	assert.Equal(t, []string{doiA, "10.1/b"}, gotBody)

	_, err = NewWithBase(srv.URL, "").Tallies(context.Background(), []string{doiA})
	require.NoError(t, err)
	assert.Empty(t, gotAuth, "the free tier sends no key")
}

func TestTalliesEmptyInputMakesNoRequest(t *testing.T) {
	got, err := NewWithBase("http://127.0.0.1:1", "").Tallies(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestTalliesBadResponses(t *testing.T) {
	for name, body := range map[string]string{
		"not json":       `<html>`,
		"no tallies":     `{"tallies":{}}`,
		"wrong envelope": `{"status":"ok"}`,
		"array":          `[1,2,3]`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			_, err := NewWithBase(srv.URL, "").Tallies(context.Background(), []string{doiA})
			require.Error(t, err)
		})
	}
}

func TestTalliesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := NewWithBase(srv.URL, "").Tallies(context.Background(), []string{doiA})
	require.ErrorContains(t, err, "400")
}

func TestContradictingSources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api_partner/citations/citing/10.1/target", r.URL.Path)
		assert.Equal(t, "Bearer k", r.Header.Get("Authorization"))

		_, _ = w.Write([]byte(`{"citations":[
			{"source":"10.9/Critic","type":"contradicting","section":"Discussion"},
			{"source":"10.9/fan","type":"supporting"},
			{"source":"10.9/old-label","type":"disagreeing"},
			{"source":"10.9/neutral","type":"mentioning"}
		]}`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, "k")
	require.True(t, c.HasKey())
	require.False(t, New("").HasKey())

	got, err := c.ContradictingSources(context.Background(), "10.1/target")
	require.NoError(t, err)
	assert.Equal(t, []string{"10.9/critic", "10.9/old-label"}, got, "only contradicting citations, DOIs normalized")
}

func TestContradictingSourcesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "scope", http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := NewWithBase(srv.URL, "k").ContradictingSources(context.Background(), "10.1/x")
	require.ErrorContains(t, err, "403")
}

// A service key like "status" next to the DOIs is ignored.
func TestParseTalliesSkipsServiceKeys(t *testing.T) {
	got, err := parseTallies(json.RawMessage(`{"10.1/a":{"total":1},"status":{"total":99}}`))
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Contains(t, got, doiA)
}

func TestSnippetCapsLongBodies(t *testing.T) {
	long := json.RawMessage(strings.Repeat("x", 500))
	assert.Len(t, snippet(long), 203)
	assert.Equal(t, "short", snippet(json.RawMessage("short")))
}

// Whatever the service answers, parsing must not panic.
func FuzzParseTallies(f *testing.F) {
	for _, seed := range []string{
		`{"10.1/a":{"total":1}}`, `{"tallies":{"10.1/a":{}}}`, `{"dois":null}`, `[]`, `null`, `"x"`, ``, `{`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		out, err := parseTallies(json.RawMessage(s))
		if err == nil {
			for doi, tally := range out {
				if doi != tally.DOI {
					t.Fatalf("key %q does not match the tally DOI %q", doi, tally.DOI)
				}
			}
		}
	})
}
