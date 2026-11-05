package llm

import (
	"context"
	"errors"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strings"
)

// ErrBareClaude rejects the bare Claude Code CLI as the command: the account,
// the gateway and the proxy of a call are set by the wrapper, so a bare
// `claude` would run on whatever the environment happens to hold. Callers
// prefix the name of the setting.
var ErrBareClaude = errors.New("must name a wrapper such as claude-max, not the bare claude CLI")

// CheckWrapper rejects the bare Claude Code CLI (`claude`, `/usr/bin/claude`,
// `claude --flag`). An empty command passes: the caller resolves the default.
func CheckWrapper(command string) error {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil
	}

	name := path.Base(strings.ReplaceAll(fields[0], `\`, "/"))
	if name == "claude" || name == "claude.exe" {
		return ErrBareClaude
	}

	return nil
}

// safeShellWord matches an argument that needs no quoting in a POSIX shell.
var safeShellWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote quotes one argument for a POSIX shell (zsh included): single
// quotes around it, an embedded quote written as '\”. The empty string
// becomes ” so that it stays an argument.
func shellQuote(s string) string {
	if safeShellWord.MatchString(s) {
		return s
	}

	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// emptyMCPConfig is an MCP config with no servers.
const emptyMCPConfig = `{"mcpServers":{}}`

// args is the argument list of one call, without the command itself.
//
// The lean-invocation flag set. A default `-p` session drags in the whole
// Claude Code harness — the built-in system prompt, CLAUDE.md, memory, the
// skill list, hooks, plugins, MCP tool schemas — ~74K input tokens measured
// per call (2026-10-18) against ~1-2K of useful payload. For
// machine-to-machine JSON calls none of that is needed:
//   - --system-prompt-file replaces the built-in prompt (and the CLAUDE.md
//     and memory auto-load that rides on it); the prompt travels via a temp
//     file because a multi-page prompt does not survive inline quoting;
//   - --tools "" drops the built-in tool schemas;
//   - --strict-mcp-config with an empty config and --disable-slash-commands
//     skip MCP servers and skills;
//   - --safe-mode skips the user's hooks, plugins, CLAUDE.md, skills and MCP
//     servers while keeping the OAuth login and the model selection
//     (--bare is unusable here: it disables OAuth, and the subscription is
//     the point);
//   - --no-session-persistence leaves no session file per call.
//
// The token saving of the extended set (--tools "" and --safe-mode on top of
// the earlier flags) was not yet measured live: the envelope's usage will
// show it on the first real run.
//
// Model, effort and fallback model are added when set.
func (c *ClaudeCLI) args(systemPromptFile string) []string {
	args := []string{
		"-p", "--output-format", "json",
		"--system-prompt-file", systemPromptFile,
		"--tools", "",
		"--strict-mcp-config", "--mcp-config", emptyMCPConfig,
		"--disable-slash-commands",
		"--no-session-persistence",
		"--safe-mode",
	}

	if c.model != "" {
		args = append(args, "--model", c.model)
	}

	if c.effort != "" {
		args = append(args, "--effort", c.effort)
	}

	if c.fallback != "" {
		args = append(args, "--fallback-model", c.fallback)
	}

	return args
}

// shellLine is the command line for the shell fallback: the wrapper as typed
// (it may be a function or carry its own flags) and the quoted arguments.
func (c *ClaudeCLI) shellLine(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}

	return c.command + " " + strings.Join(quoted, " ")
}

// process builds the process of one call. A wrapper found on PATH is run
// directly with an argument list: no shell, no quoting. Otherwise it is a zsh
// function from ~/.zshrc (or lives in a directory the agent's PATH lacks,
// such as ~/.local/bin), and an interactive shell runs it.
func (c *ClaudeCLI) process(ctx context.Context, args []string) *exec.Cmd {
	if bin, err := c.lookPath(c.command); err == nil {
		//nolint:gosec // the wrapper command comes from the user's trusted configuration, not from outside
		return exec.CommandContext(ctx, bin, args...)
	}

	//nolint:gosec // the wrapper command comes from the user's trusted configuration, not from outside
	return exec.CommandContext(ctx, c.shell[0], slices.Concat(c.shell[1:], []string{c.shellLine(args)})...)
}
