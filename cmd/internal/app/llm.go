package app

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// Where the resolved wrapper came from.
const (
	SourceFlag    = "flag"    // --llm-command of this run
	SourceSession = "session" // CLAUDE_WRAPPER, exported by the wrapper of the agent session
	SourceEnv     = ".env"    // LLM_COMMAND
	SourceDefault = "default" // nothing named a wrapper: claude-max
)

// gatewayWrapperPrefix marks the corporate gateway wrappers (claude-zai,
// claude-zai-flash, ...): their very first request starts a one-way cooldown.
const gatewayWrapperPrefix = "claude-zai"

// Wrapper is the claude-cli command of a run and the place it came from.
type Wrapper struct {
	Command string
	Source  string
}

// ResolveWrapper picks the claude-cli wrapper: the --llm-command flag, then
// CLAUDE_WRAPPER (the wrapper of the agent session this process runs in),
// then LLM_COMMAND, then claude-max. The session beats .env on purpose: .env
// serves runs outside an agent session (a terminal, verdictd), while a run
// started from a session should use the wrapper of that session. The bare
// `claude` is no wrapper and is refused wherever it comes from.
func ResolveWrapper(flag string, cfg *config.Config) (Wrapper, error) {
	candidates := []struct{ name, command, source string }{
		{"--llm-command", flag, SourceFlag},
		{"CLAUDE_WRAPPER", cfg.ClaudeWrapper, SourceSession},
		{"LLM_COMMAND", cfg.LLMCommand, SourceEnv},
	}

	for _, c := range candidates {
		command := strings.TrimSpace(c.command)
		if command == "" {
			continue
		}

		if err := llm.CheckWrapper(command); err != nil {
			return Wrapper{}, fmt.Errorf("%s %w", c.name, err)
		}

		return Wrapper{Command: command, Source: c.source}, nil
	}

	return Wrapper{Command: llm.DefaultCommand, Source: SourceDefault}, nil
}

// CompleterSpec says what completer to build: the role, plus the run's
// overrides (flags of the command line).
type CompleterSpec struct {
	Role llm.Role
	// Command is the --llm-command flag.
	Command string
	// Model is the role's own flag (--model-critique, ...).
	Model string
	// RunModel is --llm-model, the model of every role.
	RunModel string
	// Deep (--deep) makes the synthesis default the strongest model.
	Deep bool
}

// roleEnvModel is LLM_MODEL_<ROLE> of the config.
func roleEnvModel(cfg *config.Config, role llm.Role) string {
	switch role {
	case llm.RolePlan:
		return cfg.LLMModelPlan
	case llm.RoleCritique:
		return cfg.LLMModelCritique
	case llm.RoleDeep:
		return cfg.LLMModelDeep
	case llm.RoleSynthesis:
		return cfg.LLMModelSynthesis
	case llm.RoleAsk:
		return cfg.LLMModelAsk
	}

	return ""
}

// explicitModel is the model somebody asked for: the role's flag, the role's
// environment variable, --llm-model, LLM_MODEL. Empty means "the default".
func explicitModel(cfg *config.Config, s CompleterSpec) string {
	return cmp.Or(s.Model, roleEnvModel(cfg, s.Role), s.RunModel, cfg.LLMModel)
}

// BuildCompleter builds the LLM backend of one role. LLM_BACKEND empty
// always means claude-cli — the subscription: a key in .env never silently
// switches a run to a paid API, so anthropic and openai-compat are used only
// when LLM_BACKEND names them. Their credentials are checked by
// config.Validate.
//
// The model comes from the explicit settings (see explicitModel). Without
// one, claude-cli takes the role's default alias and anthropic takes
// ANTHROPIC_MODEL — the API does not accept aliases.
func BuildCompleter(cfg *config.Config, log *slog.Logger, spec CompleterSpec) (pipeline.Completer, error) {
	backend := cmp.Or(cfg.LLMBackend, llm.BackendClaudeCLI)
	model := explicitModel(cfg, spec)

	switch backend {
	case llm.BackendAnthropic:
		return buildAnthropic(cfg, log, spec.Role, model)
	case llm.BackendOpenAICompat:
		if model == "" {
			return nil, errors.New("openai-compat requires LLM_MODEL (the endpoint's model name)")
		}

		o, err := llm.NewOpenAICompat(cfg.OpenAIBaseURL, cfg.OpenAIKey, model, cfg.OpenAIProxy)
		if err != nil {
			return nil, fmt.Errorf("building the OpenAI-compatible client: %w", err)
		}

		log.Info("LLM backend", "backend", backend, "role", spec.Role, "model", model, "proxy", cfg.OpenAIProxy != "")

		return o, nil
	case llm.BackendClaudeCLI:
		return buildClaudeCLI(cfg, log, spec, model)
	case llm.BackendAuto:
		// resolved above; reaching this is a bug in this function
	}

	log.Error("LLM backend not resolved", "backend", backend)

	return nil, fmt.Errorf("unknown LLM backend %q", backend)
}

func buildAnthropic(cfg *config.Config, log *slog.Logger, role llm.Role, model string) (pipeline.Completer, error) {
	model = cmp.Or(model, cfg.AnthropicModel)
	if model == "" {
		return nil, errors.New("the anthropic backend requires ANTHROPIC_MODEL (an exact API model id)")
	}

	a, err := llm.NewAnthropic(cfg.AnthropicAPIKey, model, cfg.AnthropicProxy)
	if err != nil {
		return nil, fmt.Errorf("building the Anthropic client: %w", err)
	}

	log.Info("LLM backend", "backend", llm.BackendAnthropic, "role", role, "model", model)

	return a, nil
}

// isMax reports whether the command is the claude-max wrapper (by name or by
// path): the only one that takes --effort and --fallback-model — the GLM and
// DeepSeek gateways disable adaptive thinking.
func isMax(command string) bool {
	fields := strings.Fields(command)

	return len(fields) > 0 && filepath.Base(fields[0]) == llm.DefaultCommand
}

func buildClaudeCLI(
	cfg *config.Config, log *slog.Logger, spec CompleterSpec, explicit string,
) (pipeline.Completer, error) {
	w, err := ResolveWrapper(spec.Command, cfg)
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(w.Command, gatewayWrapperPrefix) && w.Source != SourceSession {
		log.Warn("claude-zai: the very first request blocks the corporate gateway " +
			"for hours (one-way cooldown) — make sure corporate models are not needed soon")
	}

	model := cmp.Or(explicit, spec.Role.DefaultModel(spec.Deep))

	var effort, fallback string
	if isMax(w.Command) {
		effort, fallback = spec.Role.DefaultEffort(), llm.FallbackModel(model)
	}

	log.Info("LLM backend", "command", w.Command, "source", w.Source, "role", spec.Role,
		"model", model, "effort", cmp.Or(effort, "(not passed)"))

	c, err := llm.NewClaudeCLI(llm.ClaudeCLIOptions{
		Command: w.Command, Model: model, Effort: effort, Fallback: fallback,
		Timeout: cfg.LLMTimeout, Log: log,
	})
	if err != nil {
		return nil, fmt.Errorf("building the claude-cli backend: %w", err)
	}

	return c, nil
}
