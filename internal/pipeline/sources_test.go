package pipeline

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/crossref"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/openalex"
)

// A failing source must be visible in the live log with the full error, not
// only in the Errors list of the JSON artifact.
func TestEnrichLogsSourceFailures(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadRequest) // 4xx: no retries, the test stays fast
	}))
	defer failing.Close()

	var logs bytes.Buffer

	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	p := New(Settings{Concurrency: 1}, Clients{
		OpenAlex: openalex.NewWithBase(failing.URL, ""),
		Crossref: crossref.NewWithBase(failing.URL, ""),
	}, log)

	st := &State{Papers: []model.Paper{{DOI: "10.1/fail", Source: sourceManual}}}
	require.NoError(t, p.enrich(context.Background(), st, Options{}))

	require.Len(t, st.Enriched, 1)
	assert.Equal(t, []string{
		"openalex: openalex: HTTP 400: boom\n",
		"crossref: crossref: HTTP 400: boom\n",
	}, st.Enriched[0].Errors, "the artifact trail keeps the source error text")

	out := logs.String()
	assert.Contains(t, out, "source=openalex")
	assert.Contains(t, out, "source=crossref")
	assert.Contains(t, out, "doi=10.1/fail")
	assert.Contains(t, out, "HTTP 400")
}
