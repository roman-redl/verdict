package llm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/pipeline"
)

const (
	quoteMe            = "it's" // an argument with a single quote
	envelopeOverloaded = `{"result":"API Error: 529 overloaded","is_error":true,"usage":{"input_tokens":900}}`
	okUsage            = `"usage":{"input_tokens":3,"cache_read_input_tokens":4,"output_tokens":2}`
)

// echoScript answers with everything the wrapper was told: the stdin, the
// system prompt file and the value of each flag the backend sets.
const echoScript = `
while [ $# -gt 0 ]; do
  case "$1" in
    --system-prompt-file) sp="$2" ;;
    --model) model="$2" ;;
    --effort) effort="$2" ;;
    --fallback-model) fb="$2" ;;
    --tools) tools="$2" ;;
    --mcp-config) if [ "$2" = '{"mcpServers":{}}' ]; then mcp=intact; else mcp="$2"; fi ;;
  esac
  shift
done
stdin=$(cat)
printf '{"result":"stdin=%s;system=%s;model=%s;effort=%s;fallback=%s;tools=[%s];mcp=%s",` + okUsage + `}' \
  "$stdin" "$(cat "$sp")" "$model" "$effort" "$fb" "$tools" "$mcp"
`

// cliScripts are the wrapper stand-ins; STATE is replaced by a counter file
// (a script keeps no memory between runs, the file does).
var cliScripts = map[string]string{
	"ok":         echoScript,
	"error-flag": `printf '{"result":"quota exceeded","is_error":true}'`,
	"empty":      `printf '{"result":"   "}'`,
	"garbage":    `echo not json at all`,
	"exit-1":     `echo "boom" >&2; exit 1`,
	"slow":       `echo x >> STATE; exec sleep 5`,
	// the CLI prints an is_error envelope and exits 1 on a refusal
	"quota": `echo x >> STATE
printf '{"result":"You'"'"'ve hit your limit · resets 5pm","is_error":true}'; exit 1`,
	"auth": `echo x >> STATE
printf '{"result":"Invalid API key · Please run /login","is_error":true}'; exit 1`,
	"overloaded": `echo x >> STATE
printf '` + envelopeOverloaded + `'; exit 1`,
	// two overloads, then an answer
	"overloaded-twice": `echo x >> STATE
n=$(wc -l < STATE | tr -d ' ')
if [ "$n" -lt 3 ]; then printf '` + envelopeOverloaded + `'; exit 1; fi
printf '{"result":"finally",` + okUsage + `}'`,
	"stderr-only-429": `echo x >> STATE
echo "HTTP 429 Too Many Requests" >&2; exit 1`,
}

// sleeps records the retry pauses instead of waiting them out.
type sleeps struct{ waited []time.Duration }

func (s *sleeps) sleep(_ context.Context, d time.Duration) error {
	s.waited = append(s.waited, d)
	return nil
}

type cliFixture struct {
	cli    *ClaudeCLI
	sleeps *sleeps
	state  string
}

// attempts is how many times the wrapper stand-in was started.
func (f *cliFixture) attempts(t *testing.T) int {
	t.Helper()

	b, err := os.ReadFile(f.state)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}

	require.NoError(t, err)

	return strings.Count(string(b), "x")
}

// fakeCLI writes an executable that stands in for a Claude Code wrapper and
// returns a backend over it. The wrapper is given by its absolute path, so
// the backend runs it directly; the fixture's cli.shell is a plain sh for the
// shell-fallback tests.
func fakeCLI(t *testing.T, scenario string, opts ClaudeCLIOptions) *cliFixture {
	t.Helper()

	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	body := []byte("#!/bin/sh\n" + strings.ReplaceAll(cliScripts[scenario], "STATE", state))

	path := filepath.Join(dir, "fake-claude")
	require.NoError(t, os.WriteFile(path, body, 0o700)) //nolint:gosec // a test script must be executable

	opts.Command = path

	c, err := NewClaudeCLI(opts)
	require.NoError(t, err)

	s := &sleeps{}
	c.sleep = s.sleep
	c.shell = []string{"sh", "-c"}

	return &cliFixture{cli: c, sleeps: s, state: state}
}

func complete(c *ClaudeCLI) (string, pipeline.Usage, error) {
	return c.Complete(context.Background(), "THE SYSTEM PROMPT", "THE USER PROMPT", pipeline.CompleteOptions{})
}

func TestClaudeCLIComplete(t *testing.T) {
	f := fakeCLI(t, "ok", ClaudeCLIOptions{Model: ModelSonnet})
	assert.Contains(t, f.cli.Name(), "claude-cli:")
	assert.Contains(t, f.cli.Name(), "/sonnet")

	text, usage, err := complete(f.cli)
	require.NoError(t, err)

	assert.Equal(t, `stdin=THE USER PROMPT;system=THE SYSTEM PROMPT;model=sonnet;effort=;fallback=;`+
		`tools=[];mcp=intact`, text,
		"the user prompt goes through stdin, the system prompt through a file, the model by name; "+
			"--tools gets an empty value and the MCP config stays one argument")
	assert.Equal(t, pipeline.Usage{Calls: 1, InputTokens: 7, OutputTokens: 2}, usage, "cached reads count as input")
}

func TestClaudeCLIPassesEffortAndFallback(t *testing.T) {
	f := fakeCLI(t, "ok", ClaudeCLIOptions{Model: ModelOpus, Effort: effortHigh, Fallback: ModelSonnet})

	text, _, err := complete(f.cli)
	require.NoError(t, err)
	assert.Contains(t, text, "model=opus;effort=high;fallback=sonnet;")
}

// A wrapper that is not on PATH (a zsh function, ~/.local/bin missing from the
// agent's PATH) goes through the shell with quoted arguments.
func TestClaudeCLIShellFallback(t *testing.T) {
	f := fakeCLI(t, "ok", ClaudeCLIOptions{Model: "haiku", Effort: effortLow})
	f.cli.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }

	text, _, err := complete(f.cli)
	require.NoError(t, err)
	assert.Equal(t, `stdin=THE USER PROMPT;system=THE SYSTEM PROMPT;model=haiku;effort=low;fallback=;`+
		`tools=[];mcp=intact`, text, "the quoted line delivers the same arguments")
}

func TestClaudeCLIArgs(t *testing.T) {
	base := []string{
		"-p", "--output-format", "json", "--system-prompt-file", "/tmp/sp.md", "--tools", "",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--disable-slash-commands",
		"--no-session-persistence", "--safe-mode",
	}

	tests := []struct {
		name string
		opts ClaudeCLIOptions
		want []string
	}{
		{"no model, effort or fallback", ClaudeCLIOptions{}, base},
		{"model only", ClaudeCLIOptions{Model: ModelSonnet}, append(append([]string{}, base...), "--model", ModelSonnet)},
		{
			"model and effort",
			ClaudeCLIOptions{Model: ModelSonnet, Effort: effortLow},
			append(append([]string{}, base...), "--model", ModelSonnet, "--effort", effortLow),
		},
		{
			"all three",
			ClaudeCLIOptions{Model: ModelOpus, Effort: effortHigh, Fallback: ModelSonnet},
			append(append([]string{}, base...), "--model", ModelOpus, "--effort", effortHigh, "--fallback-model", ModelSonnet),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewClaudeCLI(tt.opts)
			require.NoError(t, err)
			assert.Equal(t, tt.want, c.args("/tmp/sp.md"))
		})
	}
}

func TestShellLine(t *testing.T) {
	c, err := NewClaudeCLI(ClaudeCLIOptions{Command: "claude-zai", Model: quoteMe})
	require.NoError(t, err)

	assert.Equal(t, `claude-zai -p --output-format json --system-prompt-file /tmp/sp.md --tools '' `+
		`--strict-mcp-config --mcp-config '{"mcpServers":{}}' --disable-slash-commands `+
		`--no-session-persistence --safe-mode --model 'it'\''s'`, c.shellLine(c.args("/tmp/sp.md")))
}

func TestShellQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain-word_1.2/x:y=z,@+%", "plain-word_1.2/x:y=z,@+%"},
		{"", "''"},
		{"two words", "'two words'"},
		{quoteMe, `'it'\''s'`},
		{`{"a":1}`, `'{"a":1}'`},
		{"$HOME `id` $(id)", "'$HOME `id` $(id)'"},
		{"a;b|c&d", "'a;b|c&d'"},
		{"line\nbreak", "'line\nbreak'"},
		{"~", "'~'"},
		{"!bang", "'!bang'"},
		{"'", `''\'''`},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, shellQuote(tt.in), "quoting %q", tt.in)
	}
}

// Quoted arguments survive a real shell byte for byte.
func TestShellQuoteRoundTripsThroughSh(t *testing.T) {
	for _, in := range []string{quoteMe, `{"mcpServers":{}}`, "$HOME", "a b\tc", "`id`", "", `back\slash`, "*"} {
		//nolint:gosec // the quoting under test builds the command line on purpose
		out, err := exec.CommandContext(t.Context(), "sh", "-c", "printf %s "+shellQuote(in)).Output()
		require.NoError(t, err)
		assert.Equal(t, in, string(out))
	}
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
			f := fakeCLI(t, tt.scenario, ClaudeCLIOptions{})

			_, _, err := complete(f.cli)
			require.ErrorContains(t, err, tt.want)
			require.NotErrorIs(t, err, pipeline.ErrLLMStop)
			assert.Empty(t, f.sleeps.waited, "an unclassified failure is not retried")
		})
	}
}

// The refusal comes as an is_error envelope with exit status 1: the envelope
// text classifies the failure, and a stop is never retried.
func TestClaudeCLIStopsOnQuotaAndAuth(t *testing.T) {
	for scenario, want := range map[string]string{"quota": "hit your limit", "auth": "Invalid API key"} {
		t.Run(scenario, func(t *testing.T) {
			f := fakeCLI(t, scenario, ClaudeCLIOptions{})

			_, usage, err := complete(f.cli)
			require.ErrorIs(t, err, pipeline.ErrLLMStop)
			require.ErrorContains(t, err, want, "the envelope text reaches the error")
			assert.Equal(t, 1, f.attempts(t), "no retry after a stop")
			assert.Empty(t, f.sleeps.waited)
			assert.Zero(t, usage)
		})
	}
}

func TestClaudeCLIRetriesTransientFailures(t *testing.T) {
	f := fakeCLI(t, "overloaded-twice", ClaudeCLIOptions{})

	text, usage, err := complete(f.cli)
	require.NoError(t, err)

	assert.Equal(t, "finally", text)
	assert.Equal(t, 3, f.attempts(t))
	assert.Equal(t, []time.Duration{20 * time.Second, 60 * time.Second}, f.sleeps.waited)
	assert.Equal(t, pipeline.Usage{Calls: 1, InputTokens: 7, OutputTokens: 2}, usage,
		"only the answering attempt is counted, not the 900 input tokens of a failed one")
}

func TestClaudeCLIGivesUpAfterThreeAttempts(t *testing.T) {
	f := fakeCLI(t, "overloaded", ClaudeCLIOptions{})

	_, _, err := complete(f.cli)
	require.ErrorContains(t, err, "giving up")
	require.ErrorContains(t, err, "529", "the last failure is in the message")
	require.NotErrorIs(t, err, pipeline.ErrLLMStop, "an overload is not a stop")

	assert.Equal(t, 3, f.attempts(t))
	assert.Equal(t, []time.Duration{20 * time.Second, 60 * time.Second}, f.sleeps.waited)
}

// A rate limit that the CLI only mentions on stderr is still transient.
func TestClaudeCLIRetriesOnStderrText(t *testing.T) {
	f := fakeCLI(t, "stderr-only-429", ClaudeCLIOptions{})

	_, _, err := complete(f.cli)
	require.ErrorContains(t, err, "giving up")
	assert.Equal(t, 3, f.attempts(t))
}

func TestClaudeCLIRetryWaitIsInterruptible(t *testing.T) {
	f := fakeCLI(t, "overloaded", ClaudeCLIOptions{})

	ctx, cancel := context.WithCancel(t.Context())
	f.cli.sleep = func(ctx context.Context, _ time.Duration) error {
		cancel() // the user interrupts while the retry pause runs

		return ctx.Err()
	}

	_, _, err := f.cli.Complete(ctx, "s", "u", pipeline.CompleteOptions{})
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "waiting to retry")
	assert.Equal(t, 1, f.attempts(t), "no second attempt after the interruption")
}

func TestClaudeCLITimeoutIsNotRetried(t *testing.T) {
	// the timeout leaves the shell time to start and count its attempt even
	// under -race on a loaded machine (500 ms was flaky)
	f := fakeCLI(t, "slow", ClaudeCLIOptions{Timeout: 2 * time.Second})

	start := time.Now()
	_, _, err := complete(f.cli)

	require.ErrorContains(t, err, "timed out")
	assert.Less(t, time.Since(start), 4500*time.Millisecond)
	assert.Equal(t, 1, f.attempts(t), "a call that ran for minutes is too expensive to repeat")
	assert.Empty(t, f.sleeps.waited)
}

func TestClaudeCLIDefaults(t *testing.T) {
	c, err := NewClaudeCLI(ClaudeCLIOptions{})
	require.NoError(t, err)

	assert.Equal(t, "claude-cli:claude-max", c.Name())
	assert.Equal(t, 10*time.Minute, c.timeout)
	assert.Equal(t, []string{"zsh", "-ic"}, c.shell)
	assert.NotContains(t, c.args("/tmp/sp.md"), "--model", "no model flag without a model")
	assert.Equal(t, defaultRetryWaits, c.retryWaits)
}

// The command is a wrapper by name; the bare CLI would run on whatever
// account, gateway and proxy the environment holds.
func TestWrapperIsNeverTheBareCLI(t *testing.T) {
	tests := []struct {
		command string
		bare    bool
	}{
		{"claude", true},
		{"  claude  ", true},
		{"/usr/local/bin/claude", true},
		{"/Users/x/.local/bin/claude", true},
		{"claude --model x", true},
		{`C:\tools\claude.exe`, true},
		{"claude-max", false},
		{"claude-zai", false},
		{"claude-zai-flash", false},
		{"claude-ds", false},
		{"/Users/x/.local/bin/claude-max", false},
		{"my-claude", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			err := CheckWrapper(tt.command)
			if tt.bare {
				require.ErrorIs(t, err, ErrBareClaude)
				return
			}

			require.NoError(t, err)
		})
	}

	_, err := NewClaudeCLI(ClaudeCLIOptions{Command: "claude"})
	require.ErrorIs(t, err, ErrBareClaude, "the constructor refuses it too")
}

func TestClassify(t *testing.T) {
	tests := []struct {
		text string
		want failureKind
	}{
		{"You've hit your limit · resets 5pm", failureQuota},
		{"Claude AI usage limit reached|1760000000", failureQuota},
		{"5-hour limit reached", failureQuota},
		{"You have reached your weekly limit", failureQuota},
		{"session limit exceeded", failureQuota},
		{"Credit balance is too low", failureQuota},
		{"Invalid API key · Please run /login", failureAuth},
		{"authentication_error: invalid x-api-key", failureAuth},
		{"Authentication failed", failureAuth},
		{"Not logged in", failureAuth},
		{"Invalid bearer token", failureAuth},
		{"OAuth token has expired", failureAuth},
		{"HTTP 403 Forbidden", failureAuth},
		{"API Error: 401", failureAuth},
		{"connect 127.0.0.1:14012 refused", failureOther},
		{"request req_04290 failed after 5290 tokens", failureOther},
		{"API Error: 429 rate_limit_error", failureTransient},
		{"Rate limit exceeded, slow down", failureTransient},
		{"API Error: 529 overloaded_error", failureTransient},
		{"Overloaded", failureTransient},
		{"HTTP 502 Bad Gateway", failureTransient},
		{"503 Service Unavailable", failureTransient},
		{"504 Gateway Timeout", failureTransient},
		{"500 Internal Server Error", failureTransient},
		{"read ECONNRESET", failureTransient},
		{"socket hang up", failureTransient},
		{"Connection error.", failureTransient},
		{"TypeError: fetch failed", failureTransient},
		// quota and auth win over transient
		{"429: weekly limit reached", failureQuota},
		{"rate limit; invalid api key", failureAuth},
		{"overloaded, you've hit your limit", failureQuota},
		// everything else
		{"", failureOther},
		{"boom", failureOther},
		{"quota exceeded", failureOther},
		{"model not found", failureOther},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			assert.Equal(t, tt.want, classify(tt.text))
		})
	}
}
