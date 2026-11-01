package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
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
}

func NewClaudeCLI(command, model string, timeout time.Duration) *ClaudeCLI {
	if command == "" {
		command = "claude-zai"
	}

	if timeout <= 0 {
		timeout = 10 * time.Minute
	}

	return &ClaudeCLI{command: command, model: model, timeout: timeout}
}

func (c *ClaudeCLI) Name() string { return "claude-cli:" + c.command }

func (c *ClaudeCLI) Complete(ctx context.Context, system, user string, _ Options) (string, Usage, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// The lean-invocation flag set. A default `-p` session drags in the whole
	// Claude Code harness — built-in system prompt, CLAUDE.md, memory, skill
	// list, MCP tool schemas — ~74K input tokens measured per call (2026-10-18),
	// against ~1-2K of useful payload. For machine-to-machine JSON calls none
	// of that is needed: a custom --system-prompt skips the built-in prompt
	// (and with it the CLAUDE.md/memory auto-load), strict-mcp-config with an
	// empty config skips .mcp.json servers, disable-slash-commands skips
	// skills. The system prompt travels via a temp file: flag values go
	// through zsh -ic, and inline quoting of a multi-page prompt is fragile.
	spFile, err := os.CreateTemp("", "verdict-sp-*.md")
	if err != nil {
		return "", Usage{}, fmt.Errorf("%s: system prompt tempfile: %w", c.Name(), err)
	}

	defer func() { _ = os.Remove(spFile.Name()) }()

	if _, err := spFile.WriteString(system); err != nil {
		_ = spFile.Close()
		return "", Usage{}, fmt.Errorf("%s: system prompt tempfile: %w", c.Name(), err)
	}

	if err := spFile.Close(); err != nil {
		return "", Usage{}, fmt.Errorf("%s: system prompt tempfile: %w", c.Name(), err)
	}

	script := c.command + ` -p --output-format json` +
		` --system-prompt-file ` + strconv.Quote(spFile.Name()) +
		` --strict-mcp-config --mcp-config '{"mcpServers":{}}'` +
		` --disable-slash-commands`
	if c.model != "" {
		script += ` --model ` + c.model // exact name only: aliases do not resolve
	}
	//nolint:gosec // команда-обёртка приходит из доверенного конфига пользователя (.env), а не извне
	cmd := exec.CommandContext(callCtx, "zsh", "-ic", script)
	cmd.Stdin = strings.NewReader(user)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if callCtx.Err() == context.DeadlineExceeded {
			return "", Usage{}, fmt.Errorf("%s: timeout after %s", c.Name(), c.timeout)
		}

		return "", Usage{}, fmt.Errorf("%s: %w (stderr: %s)", c.Name(), err, snip(stderr.String()))
	}

	var env struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
		Usage   struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		return "", Usage{}, fmt.Errorf("%s: output is not the expected JSON envelope: %w (stdout: %s)",
			c.Name(), err, snip(stdout.String()))
	}

	if env.IsError {
		return "", Usage{}, fmt.Errorf("%s: session error: %s", c.Name(), firstNonEmpty(env.Result, snip(stderr.String())))
	}

	if strings.TrimSpace(env.Result) == "" {
		return "", Usage{}, fmt.Errorf("%s: empty response (stderr: %s)", c.Name(), snip(stderr.String()))
	}

	u := Usage{
		Calls:        1,
		InputTokens:  env.Usage.InputTokens + env.Usage.CacheCreationInputTokens + env.Usage.CacheReadInputTokens,
		OutputTokens: env.Usage.OutputTokens,
	}

	return env.Result, u, nil
}

func snip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[:300]
	}

	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}
