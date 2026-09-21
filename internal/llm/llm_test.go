package llm

import (
	"context"
	"errors"
	"testing"
)

type fakeCompleter struct {
	replies []string
	calls   int
}

func (f *fakeCompleter) Name() string { return "fake" }

func (f *fakeCompleter) Complete(_ context.Context, _, _ string, _ Options) (string, error) {
	if f.calls >= len(f.replies) {
		return "", errors.New("no canned replies left")
	}
	r := f.replies[f.calls]
	f.calls++
	return r, nil
}

func TestAskJSONParsesFenced(t *testing.T) {
	f := &fakeCompleter{replies: []string{"```json\n{\"verdict\": \"weak\"}\n```"}}
	var out struct {
		Verdict string `json:"verdict"`
	}
	if err := AskJSON(context.Background(), f, "sys", "usr", &out, 100); err != nil {
		t.Fatal(err)
	}
	if out.Verdict != "weak" {
		t.Fatalf("got %q", out.Verdict)
	}
}

func TestAskJSONRetriesBrokenJSON(t *testing.T) {
	f := &fakeCompleter{replies: []string{"hold on, here it is:", `{"x": 1}`}}
	var out struct {
		X int `json:"x"`
	}
	if err := AskJSON(context.Background(), f, "s", "u", &out, 100); err != nil {
		t.Fatal(err)
	}
	if out.X != 1 || f.calls != 2 {
		t.Fatalf("x=%d calls=%d (want 1 retry, 2 calls total)", out.X, f.calls)
	}
}
