package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeCompleter struct {
	replies []string
	prompts []string // user prompts as sent, for assertions
	calls   int
}

func (f *fakeCompleter) Name() string { return "fake" }

func (f *fakeCompleter) Complete(_ context.Context, _, user string, _ Options) (string, Usage, error) {
	f.prompts = append(f.prompts, user)
	if f.calls >= len(f.replies) {
		return "", Usage{}, errors.New("no canned replies left")
	}
	r := f.replies[f.calls]
	f.calls++
	return r, Usage{InputTokens: 10, OutputTokens: 5}, nil
}

func TestAskJSONParsesFenced(t *testing.T) {
	f := &fakeCompleter{replies: []string{"```json\n{\"verdict\": \"weak\"}\n```"}}
	var out struct {
		Verdict string `json:"verdict"`
	}
	if _, err := AskJSON(context.Background(), f, "sys", "usr", &out, 100); err != nil {
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
	u, err := AskJSON(context.Background(), f, "s", "u", &out, 100)
	if err != nil {
		t.Fatal(err)
	}
	if out.X != 1 || f.calls != 2 {
		t.Fatalf("x=%d calls=%d (want 1 retry, 2 calls total)", out.X, f.calls)
	}
	if u.InputTokens != 20 || u.OutputTokens != 10 {
		t.Fatalf("usage must accumulate both attempts: %+v", u)
	}
}

// The backends are stateless: a retry without the original task reaches a
// model that has never seen it. The retry must carry the payload and the
// broken answer.
func TestAskJSONRetryResendsPayload(t *testing.T) {
	f := &fakeCompleter{replies: []string{"oops not json", `{"x": 2}`}}
	var out struct {
		X int `json:"x"`
	}
	if _, err := AskJSON(context.Background(), f, "s", "ASSESS THIS STUDY", &out, 100); err != nil {
		t.Fatal(err)
	}
	if len(f.prompts) != 2 {
		t.Fatalf("want 2 prompts, got %d", len(f.prompts))
	}
	retry := f.prompts[1]
	if !strings.Contains(retry, "ASSESS THIS STUDY") {
		t.Fatalf("retry lost the original task:\n%s", retry)
	}
	if !strings.Contains(retry, "oops not json") {
		t.Fatalf("retry must quote the broken answer:\n%s", retry)
	}
}
