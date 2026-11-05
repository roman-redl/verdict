package pubpeer

import (
	"context"
	"encoding/json"
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

		var body map[string]string

		_ = json.NewDecoder(r.Body).Decode(&body)

		assert.Equal(t, map[string]string{"dois": "10.1/x", "devkey": "devkey"}, body)

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

	require.Equal(t, "PubPeer commentary (4 comments): https://pubpeer.com/t/abc", summarize(threads))
	require.Empty(t, summarize(nil), "no threads must yield no note")
}

func TestNote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string

		_ = json.NewDecoder(r.Body).Decode(&body)

		if body["dois"] == "10.1/quiet" {
			_, _ = w.Write([]byte(`{"feedbacks":[]}`))
			return
		}

		_, _ = w.Write([]byte(`{"feedbacks":[
			{"total_comments":2,"url":"https://pubpeer.com/t/1"},
			{"total_comments":1,"url":"https://pubpeer.com/t/2"}
		]}`))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, "k")
	require.True(t, c.HasKey())
	require.False(t, New("").HasKey())

	note, err := c.Note(context.Background(), "10.1/loud")
	require.NoError(t, err)
	require.Equal(t, "PubPeer commentary (3 comments): https://pubpeer.com/t/1, https://pubpeer.com/t/2", note)

	note, err = c.Note(context.Background(), "10.1/quiet")
	require.NoError(t, err)
	require.Empty(t, note)
}

func TestNoteFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := NewWithBase(srv.URL, "bad").Note(context.Background(), "10.1/x")
	require.Error(t, err)
}
