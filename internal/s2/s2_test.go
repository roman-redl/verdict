package s2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The anonymous API is verified live: batch POST works keyless, unknown
// DOIs arrive as null entries in their position.
func TestBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/paper/batch" {
			t.Errorf("request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			IDs []string `json:"ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.IDs) != 2 || body.IDs[0] != "DOI:10.1/a" {
			t.Errorf("ids: %+v", body.IDs)
		}
		_, _ = w.Write([]byte(`[
			{"externalIds":{"DOI":"10.1/a"},"title":"Study A","abstract":"abs",
			 "tldr":{"text":"A one-liner"},"citationCount":10,"influentialCitationCount":3,
			 "openAccessPdf":{"url":"https://x/a.pdf"}},
			null
		]`))
	}))
	defer srv.Close()
	c := NewWithBase(srv.URL, "")

	papers, err := c.Batch(context.Background(), []string{"10.1/a", "10.1/void"})
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) != 2 || papers[1] != nil {
		t.Fatalf("want [paper, nil], got %+v", papers)
	}
	a := papers[0]
	if a.Title != "Study A" || a.TLDRText() != "A one-liner" ||
		a.InfluentialCitations != 3 || a.OpenAccessPDF.URL != "https://x/a.pdf" {
		t.Fatalf("bad mapping: %+v", a)
	}
}

func TestBatchEmpty(t *testing.T) {
	c := New("")
	if papers, err := c.Batch(context.Background(), nil); err != nil || papers != nil {
		t.Fatalf("empty batch: %v %+v", err, papers)
	}
}
