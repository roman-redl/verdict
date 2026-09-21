package llm

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ClaudeCLI is a backend over the Claude Code CLI in print mode: it spends
// existing subscriptions, no separate tokens to buy.
//
// The wrappers (claude-zai and friends) are shell functions in ~/.zshrc, so
// the call goes through `zsh -ic`. The prompt is passed via stdin: this is
// independent of the CLI's argument quirks (--allowedTools is variadic and
// swallows positional arguments).
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

func (c *ClaudeCLI) Complete(ctx context.Context, system, user string, _ Options) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	script := c.command + ` -p --output-format text`
	if c.model != "" {
		script += ` --model ` + c.model // exact name only: aliases do not resolve
	}
	cmd := exec.CommandContext(callCtx, "zsh", "-ic", script)
	cmd.Stdin = strings.NewReader(system + "\n\n---\n\n" + user)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if callCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("%s: timeout after %s", c.Name(), c.timeout)
		}
		return "", fmt.Errorf("%s: %w (stderr: %s)", c.Name(), err, snip(stderr.String()))
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return "", fmt.Errorf("%s: empty response (stderr: %s)", c.Name(), snip(stderr.String()))
	}
	return out, nil
}

func snip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
