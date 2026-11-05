package llm

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/roman-redl/verdict/internal/pipeline"
)

var _ pipeline.Completer = (*ClaudeCLI)(nil)

const (
	defaultCommand = "claude-zai"
	defaultTimeout = 10 * time.Minute
	// snippetChars is how much process output goes into an error message.
	snippetChars = 300
	// killGrace is how long Run waits for the output pipes to close after the
	// context ended the process.
	killGrace = 2 * time.Second
)

// ClaudeCLI is a backend over the Claude Code CLI in print mode: it spends
// existing subscriptions, no separate tokens to buy.
//
// The wrappers (claude-zai and friends) are shell functions in ~/.zshrc, so
// the call goes through `zsh -ic`. The user prompt is passed via stdin: this
// is independent of the CLI's argument quirks (--allowedTools is variadic and
// swallows positional arguments); the system prompt goes through a temp file
// (--system-prompt-file), see Complete.
//
// The call runs with --output-format json: the envelope carries the answer
// plus the session's usage — the measurement behind every quota-economy
// decision (the session boilerplate the CLI loads is part of input_tokens).
//
// The first claude-zai request blocks the corporate gateway for hours
// (one-way cooldown) — the wrapper choice is deliberate configuration.
type ClaudeCLI struct {
	command string
	model   string
	timeout time.Duration
	// shell runs the command line; an interactive zsh sees the wrapper
	// functions of ~/.zshrc. Tests substitute a plain sh.
	shell []string
}

func NewClaudeCLI(command, model string, timeout time.Duration) *ClaudeCLI {
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	return &ClaudeCLI{
		command: cmp.Or(command, defaultCommand), model: model, timeout: timeout,
		shell: []string{"zsh", "-ic"},
	}
}

func (c *ClaudeCLI) Name() string { return "claude-cli:" + c.command }

func (c *ClaudeCLI) Complete(
	ctx context.Context, system, user string, _ pipeline.CompleteOptions,
) (string, pipeline.Usage, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	spPath, cleanup, err := writeSystemPrompt(system)
	if err != nil {
		return "", pipeline.Usage{}, err
	}

	defer cleanup()

	//nolint:gosec // the wrapper command comes from the user's trusted configuration (.env), not from outside
	cmd := exec.CommandContext(callCtx, c.shell[0], slices.Concat(c.shell[1:], []string{c.script(spPath)})...)
	cmd.Stdin = strings.NewReader(user)
	// The shell is killed at the timeout, but the wrapper it started may keep
	// the output pipes open; without a grace period Run would wait for it.
	cmd.WaitDelay = killGrace

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return "", pipeline.Usage{}, fmt.Errorf("%s timed out after %s", c.Name(), c.timeout)
		}

		return "", pipeline.Usage{}, fmt.Errorf("running %s: %w (stderr: %s)", c.Name(), err, snip(stderr.String()))
	}

	return c.parseEnvelope(stdout.Bytes(), stderr.String())
}

// script is the shell command line of one call.
//
// The lean-invocation flag set. A default `-p` session drags in the whole
// Claude Code harness — built-in system prompt, CLAUDE.md, memory, skill
// list, MCP tool schemas — ~74K input tokens measured per call (2026-10-18),
// against ~1-2K of useful payload. For machine-to-machine JSON calls none
// of that is needed: a custom --system-prompt skips the built-in prompt
// (and with it the CLAUDE.md/memory auto-load), strict-mcp-config with an
// empty config skips .mcp.json servers, disable-slash-commands skips
// skills. The system prompt travels via a temp file: flag values go
// through zsh -ic, and inline quoting of a multi-page prompt is fragile.
func (c *ClaudeCLI) script(systemPromptFile string) string {
	script := c.command + ` -p --output-format json` +
		` --system-prompt-file ` + strconv.Quote(systemPromptFile) +
		` --strict-mcp-config --mcp-config '{"mcpServers":{}}'` +
		` --disable-slash-commands`
	if c.model != "" {
		script += ` --model ` + c.model // exact name only: aliases do not resolve
	}

	return script
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

func (c *ClaudeCLI) parseEnvelope(stdout []byte, stderr string) (string, pipeline.Usage, error) {
	var env envelope
	if err := json.Unmarshal(stdout, &env); err != nil {
		return "", pipeline.Usage{}, fmt.Errorf("%s printed something other than the JSON envelope: %w (stdout: %s)",
			c.Name(), err, snip(string(stdout)))
	}

	if env.IsError {
		return "", pipeline.Usage{}, fmt.Errorf("%s session error: %s", c.Name(), cmp.Or(env.Result, snip(stderr)))
	}

	if strings.TrimSpace(env.Result) == "" {
		return "", pipeline.Usage{}, fmt.Errorf("%s returned an empty answer (stderr: %s)", c.Name(), snip(stderr))
	}

	usage := pipeline.Usage{
		Calls:        1,
		InputTokens:  env.Usage.InputTokens + env.Usage.CacheCreationInputTokens + env.Usage.CacheReadInputTokens,
		OutputTokens: env.Usage.OutputTokens,
	}

	return env.Result, usage, nil
}

func snip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > snippetChars {
		s = s[:snippetChars]
	}

	return s
}
