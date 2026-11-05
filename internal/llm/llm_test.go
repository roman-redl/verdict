package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/pipeline"
)

// The personal key goes to the pinned address, whatever the environment says.
func TestAnthropicBaseIsPinned(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://gateway.corp.example")

	a := NewAnthropic("sk-personal", "claude-sonnet-5")
	assert.Equal(t, "https://api.anthropic.com", a.base)
	assert.Equal(t, "anthropic:claude-sonnet-5", a.Name())
}

func TestAnthropicComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)
		assert.Equal(t, "sk-test", r.Header.Get("x-api-key"))
		assert.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))

		var req request
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "claude-test", req.Model)
		assert.Equal(t, "SYSTEM", req.System)
		assert.Equal(t, 4096, req.MaxTokens, "the default cap applies when the caller sets none")
		assert.Equal(t, []message{{Role: "user", Content: "USER"}}, req.Messages)

		_, _ = w.Write([]byte(`{"content":[{"type":"thinking","text":"skip"},{"type":"text","text":"Hello "},` +
			`{"type":"text","text":"world"}],"usage":{"input_tokens":11,"output_tokens":7}}`))
	}))
	defer srv.Close()

	a := newAnthropic(srv.URL, "sk-test", "claude-test")

	text, usage, err := a.Complete(t.Context(), "SYSTEM", "USER", pipeline.CompleteOptions{})
	require.NoError(t, err)

	assert.Equal(t, "Hello world", text, "only text blocks are returned, concatenated")
	assert.Equal(t, pipeline.Usage{Calls: 1, InputTokens: 11, OutputTokens: 7}, usage)
}

func TestAnthropicHonorsMaxTokensAndReportsEmptyAnswers(t *testing.T) {
	var gotMax int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req request

		_ = json.NewDecoder(r.Body).Decode(&req)
		gotMax = req.MaxTokens

		_, _ = w.Write([]byte(`{"content":[],"stop_reason":"max_tokens"}`))
	}))
	defer srv.Close()

	_, _, err := newAnthropic(srv.URL, "k", "m").Complete(t.Context(), "s", "u", pipeline.CompleteOptions{MaxTokens: 123})

	require.ErrorContains(t, err, "max_tokens", "the stop reason explains the empty answer")
	assert.Equal(t, 123, gotMax)
}

func TestAnthropicHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid x-api-key"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, _, err := newAnthropic(srv.URL, "bad", "m").Complete(t.Context(), "s", "u", pipeline.CompleteOptions{})
	require.ErrorContains(t, err, "401")
}

func TestOpenAICompatComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))

		body, _ := io.ReadAll(r.Body)
		assert.JSONEq(t, `{"model":"gpt-test","max_tokens":4096,"temperature":0.2,"messages":[`+
			`{"role":"system","content":"SYSTEM"},{"role":"user","content":"USER"}]}`, string(body))

		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"the answer"}}],` +
			`"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
	}))
	defer srv.Close()

	o := NewOpenAICompat(srv.URL+"/v1", "sk-test", "gpt-test")
	assert.Equal(t, "openai-compat:gpt-test", o.Name())

	text, usage, err := o.Complete(t.Context(), "SYSTEM", "USER", pipeline.CompleteOptions{})
	require.NoError(t, err)
	assert.Equal(t, "the answer", text)
	assert.Equal(t, pipeline.Usage{Calls: 1, InputTokens: 5, OutputTokens: 3}, usage)
}

func TestOpenAICompatEmptyCompletionAndDefaultBase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	_, _, err := NewOpenAICompat(srv.URL, "k", "m").Complete(t.Context(), "s", "u", pipeline.CompleteOptions{})
	require.ErrorContains(t, err, "empty completion")

	assert.Equal(t, "https://api.openai.com/v1", NewOpenAICompat("", "k", "m").base)
}

// fakeCLI writes an executable that stands in for a Claude Code wrapper: it
// echoes its stdin and the system prompt file into the JSON envelope, or
// fails the way the scenario asks.
func fakeCLI(t *testing.T, scenario string) *ClaudeCLI {
	t.Helper()

	script := map[string]string{
		"ok": `
sp=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--system-prompt-file" ]; then sp="$2"; fi
  if [ "$1" = "--model" ]; then model="$2"; fi
  shift
done
stdin=$(cat)
usage='"usage":{"input_tokens":3,"cache_read_input_tokens":4,"output_tokens":2}'
printf '{"result":"stdin=%s;system=%s;model=%s",%s}' "$stdin" "$(cat "$sp")" "$model" "$usage"
`,
		"error-flag": `printf '{"result":"quota exceeded","is_error":true}'`,
		"empty":      `printf '{"result":"   "}'`,
		"garbage":    `echo not json at all`,
		"exit-1":     `echo "boom" >&2; exit 1`,
		"slow":       `sleep 5`,
	}[scenario]

	path := filepath.Join(t.TempDir(), "fake-claude")
	body := []byte("#!/bin/sh\n" + script)
	require.NoError(t, os.WriteFile(path, body, 0o700)) //nolint:gosec // a test script must be executable

	c := NewClaudeCLI(path, "claude-test", 0)
	c.shell = []string{"sh", "-c"}

	return c
}

func TestClaudeCLIComplete(t *testing.T) {
	c := fakeCLI(t, "ok")
	assert.Contains(t, c.Name(), "claude-cli:")

	text, usage, err := c.Complete(t.Context(), "THE SYSTEM PROMPT", "THE USER PROMPT", pipeline.CompleteOptions{})
	require.NoError(t, err)

	assert.Equal(t, "stdin=THE USER PROMPT;system=THE SYSTEM PROMPT;model=claude-test", text,
		"the user prompt goes through stdin, the system prompt through a file, the model by exact name")
	assert.Equal(t, pipeline.Usage{Calls: 1, InputTokens: 7, OutputTokens: 2}, usage,
		"cached reads count as input")
}

func TestClaudeCLIFailures(t *testing.T) {
	tests := []struct {
		scenario string
		want     string
	}{
		{"error-flag", "quota exceeded"},
		{"empty", "empty answer"},
		{"garbage", "other than the JSON envelope"},
		{"exit-1", "boom"},
	}

	for _, tt := range tests {
		t.Run(tt.scenario, func(t *testing.T) {
			_, _, err := fakeCLI(t, tt.scenario).Complete(t.Context(), "s", "u", pipeline.CompleteOptions{})
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestClaudeCLITimeout(t *testing.T) {
	c := fakeCLI(t, "slow")
	c.timeout = 100 * time.Millisecond

	start := time.Now()
	_, _, err := c.Complete(t.Context(), "s", "u", pipeline.CompleteOptions{})

	require.ErrorContains(t, err, "timed out")
	assert.Less(t, time.Since(start), 3*time.Second)
}

func TestClaudeCLIDefaults(t *testing.T) {
	c := NewClaudeCLI("", "", 0)

	assert.Equal(t, "claude-cli:claude-zai", c.Name())
	assert.Equal(t, 10*time.Minute, c.timeout)
	assert.Equal(t, []string{"zsh", "-ic"}, c.shell)
	assert.NotContains(t, c.script("/tmp/sp.md"), "--model", "no model flag without a model")
	assert.Contains(t, c.script("/tmp/sp.md"), `--system-prompt-file "/tmp/sp.md"`)
}
