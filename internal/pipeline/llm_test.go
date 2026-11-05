package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoCompleter replays canned answers and records the prompts it got.
type echoCompleter struct {
	replies []string
	prompts []string
	calls   int
	failAt  int // 1-based call number that fails; 0 = never
}

func (f *echoCompleter) Name() string { return "echo" }

func (f *echoCompleter) Complete(_ context.Context, _, user string, _ CompleteOptions) (string, Usage, error) {
	f.prompts = append(f.prompts, user)
	f.calls++

	if f.failAt == f.calls {
		return "", Usage{}, errors.New("backend down")
	}

	if f.calls > len(f.replies) {
		return "", Usage{}, errors.New("no canned replies left")
	}

	return f.replies[f.calls-1], Usage{InputTokens: 10, OutputTokens: 5}, nil
}

func TestAskJSONParsesFenced(t *testing.T) {
	f := &echoCompleter{replies: []string{"```json\n{\"verdict\": \"weak\"}\n```"}}

	var out struct {
		Verdict string `json:"verdict"`
	}

	_, err := AskJSON(context.Background(), f, "sys", "usr", &out, 100)
	require.NoError(t, err)

	require.Equal(t, "weak", out.Verdict)
}

func TestAskJSONRetriesBrokenJSON(t *testing.T) {
	f := &echoCompleter{replies: []string{"hold on, here it is:", `{"x": 1}`}}

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
	f := &echoCompleter{replies: []string{"oops not json", `{"x": 2}`}}

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

func TestAskJSONGivesUpAfterOneRetry(t *testing.T) {
	f := &echoCompleter{replies: []string{"nope", "still nope", `{"x": 3}`}}

	var out struct{ X int }

	u, err := AskJSON(context.Background(), f, "s", "u", &out, 100)
	require.ErrorContains(t, err, "invalid JSON after a retry")
	require.Equal(t, 2, f.calls, "exactly one retry")
	assert.Equal(t, 20, u.InputTokens, "the spent usage is still reported")
}

func TestAskJSONBackendErrors(t *testing.T) {
	var out struct{ X int }

	t.Run("first call", func(t *testing.T) {
		f := &echoCompleter{failAt: 1}

		_, err := AskJSON(context.Background(), f, "s", "u", &out, 100)
		require.ErrorContains(t, err, "backend down")
		require.ErrorContains(t, err, "echo", "the error names the backend")
	})

	t.Run("retry call", func(t *testing.T) {
		f := &echoCompleter{replies: []string{"broken"}, failAt: 2}

		u, err := AskJSON(context.Background(), f, "s", "u", &out, 100)
		require.ErrorContains(t, err, "backend down")
		require.Equal(t, 10, u.InputTokens, "the first attempt's usage survives")
	})
}

func TestStripFencesAndCapText(t *testing.T) {
	assert.JSONEq(t, `{"a":1}`, string(stripFences("```json\n{\"a\":1}\n```")))
	assert.JSONEq(t, `{"a":1}`, string(stripFences("  {\"a\":1}  ")))
	assert.JSONEq(t, `{"a":1}`, string(stripFences("```\n{\"a\":1}\n```")))

	assert.Equal(t, "héllo", capText("héllo", 5))
	assert.Equal(t, "hé…", capText("héllo", 2), "the cap counts runes, not bytes")
}

func TestUsageAdd(t *testing.T) {
	sum := Usage{Calls: 1, InputTokens: 2, OutputTokens: 3}.Add(Usage{Calls: 4, InputTokens: 5, OutputTokens: 6})
	assert.Equal(t, Usage{Calls: 5, InputTokens: 7, OutputTokens: 9}, sum)
}

func FuzzStripFences(f *testing.F) {
	for _, seed := range []string{"", "```json\n{}\n```", "```", "``` ", "{\"a\":1}", "```\n```"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		// must not panic whatever the model wraps around its answer
		_ = stripFences(s)
	})
}
