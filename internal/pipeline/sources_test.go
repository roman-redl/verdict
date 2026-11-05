package pipeline

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// A failing source must be visible in the live log with the full error, not
// only in the Errors list of the JSON artifact.
func TestEnrichLogsSourceFailures(t *testing.T) {
	works := &fakeWorks{byDOI: func(string) (*model.Work, error) {
		return nil, errors.New("HTTP 400 from openalex: boom")
	}}
	notices := &fakeNotices{updates: func(string) ([]model.Notice, error) {
		return nil, errors.New("HTTP 400 from crossref: boom")
	}}

	var logs bytes.Buffer

	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	p := New(Settings{Concurrency: 1}, Clients{Works: works, Notices: notices}, log)

	st := &State{Papers: []model.Paper{{DOI: "10.1/fail", Source: sourceManual}}}
	require.NoError(t, p.enrich(context.Background(), params(Options{}), st))

	require.Len(t, st.Enriched, 1)
	assert.Equal(t, []string{
		"openalex: HTTP 400 from openalex: boom",
		"crossref: HTTP 400 from crossref: boom",
	}, st.Enriched[0].Errors, "the artifact trail keeps the source error text")

	out := logs.String()
	assert.Contains(t, out, "source=openalex")
	assert.Contains(t, out, "source=crossref")
	assert.Contains(t, out, "doi=10.1/fail")
	assert.Contains(t, out, "HTTP 400")
}
