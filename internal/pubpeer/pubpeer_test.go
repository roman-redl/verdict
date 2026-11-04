package pubpeer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The API shape is probed live (POST /v3/publications requires a devkey
// and a dois field); the response body is implemented per the docs and
// verified on the first live run with a key.
func TestPublications(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v3/publications", r.URL.Path)

		_, _ = w.Write([]byte(`{"feedbacks":[
			{"id":"10.1/x","title":"Retracted paper discussion","total_comments":4,
			 "url":"https://pubpeer.com/t/abc","last_commented_at":{"timezone":"UTC"}}
		]}`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, "devkey")

	threads, err := c.Publications(context.Background(), "10.1/x")
	require.NoError(t, err)

	require.Len(t, threads, 1)
	require.Equal(t, 4, threads[0].TotalComments)
	require.Equal(t, "10.1/x", threads[0].ID)

	require.Equal(t, "PubPeer commentary (4 comments): https://pubpeer.com/t/abc", Note(threads))
	require.Empty(t, Note(nil), "no threads must yield no note")
}
