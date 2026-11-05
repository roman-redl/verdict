package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.Completer = (*ClaudeCLI)(nil)

const (
	// DefaultCommand is the wrapper used when nothing names another one.
	DefaultCommand = "claude-max"

	defaultTimeout = 10 * time.Minute
	// snippetChars is how much process output goes into an error message.
	snippetChars = 300
	// killGrace is how long Run waits for the output pipes to close after the
	// context ended the process.
	killGrace = 2 * time.Second
	// maxAttempts is the number of tries of one call, the first included.
	maxAttempts = 3
)

// defaultRetryWaits are the pauses before the second and the third attempt.
var defaultRetryWaits = []time.Duration{20 * time.Second, 60 * time.Second}

// ClaudeCLIOptions are the parameters of a ClaudeCLI.
type ClaudeCLIOptions struct {
	// Command is the wrapper (empty = DefaultCommand).
	Command string
	// Model is a model name or alias; empty = the wrapper's default.
	Model string
	// Effort and Fallback are the --effort and --fallback-model values; the
	// caller sets them only for a wrapper that takes them (claude-max): the
	// GLM and DeepSeek gateways disable adaptive thinking.
	Effort   string
	Fallback string
	// Timeout caps one attempt (empty = 10 minutes).
	Timeout time.Duration
	// Log reports the retries (nil = silent).
	Log *slog.Logger
}

// ClaudeCLI is a backend over the Claude Code CLI in print mode: it spends
// existing subscriptions, no separate tokens to buy.
//
// The command is always a wrapper (claude-max, claude-zai, claude-ds, ...),
// never the bare CLI: the wrapper sets the account, the gateway and the proxy
// of the call, so NewClaudeCLI refuses `claude`. A wrapper found on PATH is
// run directly with an argument list. One that is not — a zsh function from
// ~/.zshrc, or a script in a directory the agent's PATH lacks — runs through
// `zsh -ic` with quoted arguments. The user prompt is passed via stdin, which
// is independent of the CLI's argument quirks (--allowedTools is variadic and
// swallows positional arguments); the system prompt goes through a temp file
// (--system-prompt-file), see args.
//
// The call runs with --output-format json: the envelope carries the answer
// plus the session's usage — the measurement behind every quota-economy
// decision (the session boilerplate the CLI loads is part of input_tokens).
// The CLI prints an is_error envelope and exits non-zero on a refusal, so
// the envelope is read on failure too.
//
// A failed attempt is classified (see classify): a rate limit or an overload
// is retried after a pause, an exhausted quota or a lost login ends the run
// (pipeline.ErrLLMStop), anything else — a bad answer, our own timeout, which
// costs too much to repeat — fails the call as is.
//
// The very first claude-zai request blocks the corporate gateway for hours
// (one-way cooldown) — for that wrapper the choice is deliberate configuration.
type ClaudeCLI struct {
	command  string
	model    string
	effort   string
	fallback string
	timeout  time.Duration
	log      *slog.Logger

	// lookPath finds the wrapper on PATH; tests substitute it.
	lookPath func(string) (string, error)
	// shell runs the command line when the wrapper is not on PATH; an
	// interactive zsh sees the wrapper functions of ~/.zshrc. Tests
	// substitute a plain sh.
	shell []string
	// sleep and retryWaits pace the retries; time is a dependency.
	sleep      func(context.Context, time.Duration) error
	retryWaits []time.Duration
}

// NewClaudeCLI returns a backend over the wrapper; the bare CLI is an error.
func NewClaudeCLI(o ClaudeCLIOptions) (*ClaudeCLI, error) {
	command := strings.TrimSpace(o.Command)
	if command == "" {
		command = DefaultCommand
	}

	if err := CheckWrapper(command); err != nil {
		return nil, fmt.Errorf("the LLM command %q %w", command, err)
	}

	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	timeout := o.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	return &ClaudeCLI{
		command: command, model: o.Model, effort: o.Effort, fallback: o.Fallback,
		timeout: timeout, log: log,
		lookPath: exec.LookPath, shell: []string{"zsh", "-ic"},
		sleep: sleepCtx, retryWaits: defaultRetryWaits,
	}, nil
}

func (c *ClaudeCLI) Name() string {
	if c.model == "" {
		return "claude-cli:" + c.command
	}

	return "claude-cli:" + c.command + "/" + c.model
}

// Complete performs one model call, retrying transient failures. The usage
// of failed attempts is not counted: only the answer's attempt reports one.
func (c *ClaudeCLI) Complete(
	ctx context.Context, system, user string, _ pipeline.CompleteOptions,
) (string, pipeline.Usage, error) {
	spPath, cleanup, err := writeSystemPrompt(system)
	if err != nil {
		return "", pipeline.Usage{}, err
	}

	defer cleanup()

	var lastErr error

	for attempt := range maxAttempts {
		if attempt > 0 {
			wait := c.retryWaits[min(attempt-1, len(c.retryWaits)-1)]
			c.log.Warn("LLM call failed, retrying", "backend", c.Name(),
				"attempt", attempt+1, "of", maxAttempts, "wait", wait, "err", lastErr)

			if err := c.sleep(ctx, wait); err != nil {
				return "", pipeline.Usage{}, fmt.Errorf("waiting to retry %s: %w", c.Name(), err)
			}
		}

		text, usage, retry, err := c.attempt(ctx, spPath, user)
		if err == nil {
			return text, usage, nil
		}

		if !retry {
			return "", pipeline.Usage{}, err
		}

		lastErr = err
	}

	return "", pipeline.Usage{}, fmt.Errorf("giving up on %s after %d attempts: %w", c.Name(), maxAttempts, lastErr)
}

// attempt is one process run. retry says whether the failure is transient.
func (c *ClaudeCLI) attempt(
	ctx context.Context, systemPromptFile, user string,
) (text string, usage pipeline.Usage, retry bool, err error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.process(callCtx, c.args(systemPromptFile))
	cmd.Stdin = strings.NewReader(user)
	// The shell is killed at the timeout, but the wrapper it started may keep
	// the output pipes open; without a grace period Run would wait for it.
	cmd.WaitDelay = killGrace

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if runErr != nil && callCtx.Err() != nil {
		// our own timeout or the caller's cancellation: repeating a call that
		// ran for minutes is too expensive
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return "", pipeline.Usage{}, false, fmt.Errorf("%s timed out after %s", c.Name(), c.timeout)
		}

		return "", pipeline.Usage{}, false, fmt.Errorf("running %s: %w", c.Name(), callCtx.Err())
	}

	env, envErr := decodeEnvelope(stdout.Bytes())

	var cause error

	switch {
	case runErr != nil:
		cause = runErr
	case envErr != nil:
		cause = envErr
	case env.IsError:
		cause = errSessionError
	case strings.TrimSpace(env.Result) == "":
		return "", pipeline.Usage{}, false,
			fmt.Errorf("%s returned an empty answer (stderr: %s)", c.Name(), snip(stderr.String()))
	default:
		return env.Result, env.usage(), false, nil
	}

	retry, err = c.failure(cause, env, envErr, stdout.String(), stderr.String())

	return "", pipeline.Usage{}, retry, err
}

// errSessionError is an envelope with is_error set; its text is the detail.
var errSessionError = errors.New("session error")

// failure turns a failed run into an error and says whether to retry it. The
// text it classifies is the envelope's result plus the stderr snippet (plus
// the raw stdout when no envelope could be read): the CLI reports a refusal
// there, and exits non-zero.
func (c *ClaudeCLI) failure(cause error, env envelope, envErr error, stdout, stderr string) (bool, error) {
	parts := []string{snip(env.Result), snip(stderr)}
	if envErr != nil {
		parts = append(parts, snip(stdout))
	}

	detail := strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), "; ")

	switch classify(detail) {
	case failureQuota, failureAuth:
		return false, fmt.Errorf("%w: %s: %s", pipeline.ErrLLMStop, c.Name(), detail)
	case failureTransient:
		return true, fmt.Errorf("%s: %w (%s)", c.Name(), cause, detail)
	case failureOther:
	}

	return false, fmt.Errorf("%s: %w (%s)", c.Name(), cause, detail)
}

// writeSystemPrompt stores the system prompt in a temp file and returns its
// path with a function that removes it.
func writeSystemPrompt(system string) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "verdict-sp-*.md")
	if err != nil {
		return "", nil, fmt.Errorf("creating the system prompt file: %w", err)
	}

	remove := func() { _ = os.Remove(f.Name()) } // a leftover temp file is harmless and cleaned by the OS

	if _, err := f.WriteString(system); err != nil {
		_ = f.Close() // the write error below is the one that matters

		remove()

		return "", nil, fmt.Errorf("writing the system prompt file: %w", err)
	}

	if err := f.Close(); err != nil {
		remove()

		return "", nil, fmt.Errorf("closing the system prompt file: %w", err)
	}

	return f.Name(), remove, nil
}

// envelope is the --output-format json answer of the CLI.
type envelope struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Usage   struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

func (e envelope) usage() pipeline.Usage {
	return pipeline.Usage{
		Calls:        1,
		InputTokens:  e.Usage.InputTokens + e.Usage.CacheCreationInputTokens + e.Usage.CacheReadInputTokens,
		OutputTokens: e.Usage.OutputTokens,
	}
}

// decodeEnvelope reads the envelope from the process output; an output that
// is not one is an error.
func decodeEnvelope(stdout []byte) (envelope, error) {
	var env envelope
	if err := json.Unmarshal(stdout, &env); err != nil {
		return envelope{}, fmt.Errorf("printed something other than the JSON envelope: %w", err)
	}

	return env, nil
}

func snip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > snippetChars {
		s = s[:snippetChars]
	}

	return s
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting was interrupted: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}
