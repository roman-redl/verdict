package scite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
				"10.1/a": map[string]int{"total": 5, "supporting": 3, "contradicting": 1, "mentioning": 1},
			},
			wantDOI: "10.1/a",
			want:    [2]int{5, 1},
		},
		{
			name: "tallies-envelope",
			body: map[string]any{
				"tallies": map[string]any{
					"10.2/b": map[string]int{"total": 9, "supporting": 8, "contradicting": 0, "mentioning": 1},
				},
			},
			wantDOI: "10.2/b",
			want:    [2]int{9, 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(tt.body)
			}))
			defer srv.Close()
			c := New("")
			c.base = srv.URL

			got, err := c.Tallies(context.Background(), []string{tt.wantDOI})
			if err != nil {
				t.Fatal(err)
			}
			tl, ok := got[tt.wantDOI]
			if !ok {
				t.Fatalf("no tallies for %s: %+v", tt.wantDOI, got)
			}
			if tl.Total != tt.want[0] || tl.Contradicting != tt.want[1] {
				t.Errorf("%s: got %+v, want total=%d contradicting=%d", tt.wantDOI, tl, tt.want[0], tt.want[1])
			}
		})
	}
}
