package crossref

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The response shape is verified live (Wakefield DOI): retractions and
// corrections both arrive as updated-by entries, discriminated by type.
func TestUpdates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/works/10.1016/S0140-6736(97)11096-0" {
			t.Errorf("path: got %q, want the DOI path (decoded form)", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"message":{"updated-by":[
			{"DOI":"10.1016/s0140-6736(04)15715-2","type":"correction","label":"Correction","source":"retraction-watch"},
			{"DOI":"10.1016/s0140-6736(10)60175-4","type":"retraction","label":"Retraction","source":"retraction-watch"}
		]}}`))
	}))
	defer srv.Close()
	c := NewWithBase(srv.URL, "")

	ups, err := c.Updates(context.Background(), "10.1016/S0140-6736(97)11096-0")
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 2 {
		t.Fatalf("want 2 updates, got %d", len(ups))
	}
	if !ups[1].IsRetraction() || ups[0].IsRetraction() {
		t.Fatalf("retraction discrimination broken: %+v", ups)
	}
}

func TestUpdatesCleanRecord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"DOI":"10.1/ok"}}`))
	}))
	defer srv.Close()
	c := NewWithBase(srv.URL, "")

	ups, err := c.Updates(context.Background(), "10.1/ok")
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 0 {
		t.Fatalf("clean record: want no updates, got %+v", ups)
	}
}

// A 404 (Crossref does not know the DOI) is "no data", not an error.
func TestUpdatesUnknownDOI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewWithBase(srv.URL, "")

	ups, err := c.Updates(context.Background(), "10.1/void")
	if err != nil {
		t.Fatalf("404 must not be an error, got %v", err)
	}
	if len(ups) != 0 {
		t.Fatalf("want no updates, got %+v", ups)
	}
}
