package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
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

	_, err := AskJSON(context.Background(), f, "sys", "usr", &out, 100)
	require.NoError(t, err)

	require.Equal(t, "weak", out.Verdict)
}

func TestAskJSONRetriesBrokenJSON(t *testing.T) {
	f := &fakeCompleter{replies: []string{"hold on, here it is:", `{"x": 1}`}}

	var out struct {
		X int `json:"x"`
	}

	u, err := AskJSON(context.Background(), f, "s", "u", &out, 100)
	require.NoError(t, err)

	require.Equal(t, 1, out.X)
	require.Equal(t, 2, f.calls, "want 1 retry, 2 calls total")

	require.Equal(t, 20, u.InputTokens, "usage must accumulate both attempts")
	require.Equal(t, 10, u.OutputTokens, "usage must accumulate both attempts")
}

// The backends are stateless: a retry without the original task reaches a
// model that has never seen it. The retry must carry the payload and the
// broken answer.
func TestAskJSONRetryResendsPayload(t *testing.T) {
	f := &fakeCompleter{replies: []string{"oops not json", `{"x": 2}`}}

	var out struct {
		X int `json:"x"`
	}

	_, err := AskJSON(context.Background(), f, "s", "ASSESS THIS STUDY", &out, 100)
	require.NoError(t, err)

	require.Len(t, f.prompts, 2)

	retry := f.prompts[1]
	require.Contains(t, retry, "ASSESS THIS STUDY", "retry lost the original task")
	require.Contains(t, retry, "oops not json", "retry must quote the broken answer")
}
