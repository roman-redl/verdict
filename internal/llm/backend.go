package llm

import (
	"fmt"
	"strings"
)

// Backend names an LLM backend.
type Backend string

const (
	// BackendAuto is claude-cli: a key in the environment never switches a
	// run from the subscription to a paid API, that takes an explicit choice.
	BackendAuto Backend = ""
	// BackendAnthropic is the direct Messages API.
	BackendAnthropic Backend = "anthropic"
	// BackendOpenAICompat is any OpenAI-compatible /chat/completions endpoint.
	BackendOpenAICompat Backend = "openai-compat"
	// BackendClaudeCLI is a Claude Code wrapper run in print mode.
	BackendClaudeCLI Backend = "claude-cli"
)

// ParseBackend parses LLM_BACKEND; the empty string is BackendAuto.
func ParseBackend(s string) (Backend, error) {
	b := Backend(strings.ToLower(strings.TrimSpace(s)))

	switch b {
	case BackendAuto, BackendAnthropic, BackendOpenAICompat, BackendClaudeCLI:
		return b, nil
	}

	return "", fmt.Errorf("unknown LLM backend %q (want anthropic | claude-cli | openai-compat, or empty for auto)", s)
}

// UnmarshalText lets the environment parser reject an unknown backend at startup.
func (b *Backend) UnmarshalText(text []byte) error {
	parsed, err := ParseBackend(string(text))
	if err != nil {
		return err
	}

	*b = parsed

	return nil
}
