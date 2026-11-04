package crossref

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The response shape is verified live (Wakefield DOI): retractions and
// corrections both arrive as updated-by entries, discriminated by type.
func TestUpdates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/works/10.1016/S0140-6736(97)11096-0", r.URL.Path, "want the DOI path (decoded form)")

		_, _ = w.Write([]byte(`{"message":{"updated-by":[
			{"DOI":"10.1016/s0140-6736(04)15715-2","type":"correction","label":"Correction","source":"retraction-watch"},
			{"DOI":"10.1016/s0140-6736(10)60175-4","type":"retraction","label":"Retraction","source":"retraction-watch"}
		]}}`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, "")

	ups, err := c.Updates(context.Background(), "10.1016/S0140-6736(97)11096-0")
	require.NoError(t, err)

	require.Len(t, ups, 2)
	require.True(t, ups[1].IsRetraction(), "retraction discrimination broken")
	require.False(t, ups[0].IsRetraction(), "retraction discrimination broken")
}

func TestUpdatesCleanRecord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"DOI":"10.1/ok"}}`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, "")

	ups, err := c.Updates(context.Background(), "10.1/ok")
	require.NoError(t, err)

	require.Empty(t, ups, "clean record: want no updates")
}

// A 404 (Crossref does not know the DOI) is "no data", not an error.
func TestUpdatesUnknownDOI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, "")

	ups, err := c.Updates(context.Background(), "10.1/void")
	require.NoError(t, err, "404 must not be an error")

	require.Empty(t, ups)
}
