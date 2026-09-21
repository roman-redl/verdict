package pubpeer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The API shape is probed live (POST /v3/publications requires a devkey
// and a dois field); the response body is implemented per the docs and
// verified on the first live run with a key.
func TestPublications(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v3/publications" {
			t.Errorf("request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"feedbacks":[
			{"id":"10.1/x","title":"Retracted paper discussion","total_comments":4,
			 "url":"https://pubpeer.com/t/abc","last_commented_at":{"timezone":"UTC"}}
		]}`))
	}))
	defer srv.Close()
	c := NewWithBase(srv.URL, "devkey")

	threads, err := c.Publications(context.Background(), "10.1/x")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].TotalComments != 4 || threads[0].ID != "10.1/x" {
		t.Fatalf("bad mapping: %+v", threads)
	}
	if note := Note(threads); note != "PubPeer commentary (4 comments): https://pubpeer.com/t/abc" {
		t.Fatalf("note: %q", note)
	}
	if note := Note(nil); note != "" {
		t.Fatalf("no threads must yield no note, got %q", note)
	}
}
