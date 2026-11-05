package app

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/pipeline"
)

const (
	modelX = "claude-x"
	//nolint:gosec // a fake proxy URL with a fake password: the tests never touch the network
	proxyURL   = "https://user:pw@vps.example.org:443"
	maxName    = "claude-cli:claude-max/"
	wrapperDS  = "claude-ds"
	wrapperMax = "claude-max"
	wrapperZai = "claude-zai"
	bareCLI    = "claude"
	modelEnv   = "m-env"
	synthEnv   = "synth-env"
	modelRun   = "run"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// bufLog is a logger whose output a test can read.
func bufLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer

	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func openAICompatConfig() *config.Config {
	return &config.Config{LLMBackend: llm.BackendOpenAICompat, OpenAIKey: "k", OpenAIProxy: proxyURL}
}

func TestResolveWrapper(t *testing.T) {
	tests := []struct {
		name       string
		flag       string
		cfg        config.Config
		want       Wrapper
		wantErrHas string
	}{
		{"nothing set is claude-max", "", config.Config{}, Wrapper{wrapperMax, SourceDefault}, ""},
		{".env names a wrapper", "", config.Config{LLMCommand: wrapperDS}, Wrapper{wrapperDS, SourceEnv}, ""},
		{
			"the session beats .env", "",
			config.Config{ClaudeWrapper: wrapperZai, LLMCommand: wrapperDS},
			Wrapper{wrapperZai, SourceSession},
			"",
		},
		{
			"the flag beats the session and .env", wrapperMax,
			config.Config{ClaudeWrapper: wrapperZai, LLMCommand: wrapperDS},
			Wrapper{wrapperMax, SourceFlag},
			"",
		},
		{
			"blank values are unset", "  ",
			config.Config{ClaudeWrapper: " ", LLMCommand: "\t"},
			Wrapper{wrapperMax, SourceDefault},
			"",
		},
		{"values are trimmed", " claude-ds ", config.Config{}, Wrapper{wrapperDS, SourceFlag}, ""},
		{
			"the bare CLI as the flag", bareCLI,
			config.Config{},
			Wrapper{},
			"--llm-command must name a wrapper such as claude-max, not the bare claude CLI",
		},
		{"a path to the bare CLI as the flag", "/opt/bin/claude", config.Config{}, Wrapper{}, "--llm-command must name"},
		{
			"the bare CLI as the session wrapper", "",
			config.Config{ClaudeWrapper: bareCLI},
			Wrapper{},
			"CLAUDE_WRAPPER must name a wrapper such as claude-max, not the bare claude CLI",
		},
		{
			"the bare CLI in .env", "",
			config.Config{LLMCommand: bareCLI},
			Wrapper{},
			"LLM_COMMAND must name a wrapper such as claude-max, not the bare claude CLI",
		},
		{
			"the session wrapper is not rescued by a good .env", "",
			config.Config{ClaudeWrapper: bareCLI, LLMCommand: wrapperDS},
			Wrapper{},
			"CLAUDE_WRAPPER must name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveWrapper(tt.flag, &tt.cfg)
			if tt.wantErrHas != "" {
				require.ErrorContains(t, err, tt.wantErrHas)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildCompleterPicksTheBackend(t *testing.T) {
	tests := []struct {
		name     string
		cfg      config.Config
		wantName string
	}{
		{"auto without keys is the claude-cli wrapper", config.Config{LLMCommand: wrapperDS}, "claude-cli:claude-ds/sonnet"},
		{
			"an Anthropic key never switches auto to the paid API",
			config.Config{AnthropicAPIKey: "k", AnthropicProxy: proxyURL, AnthropicModel: modelX},
			maxName + llm.ModelSonnet,
		},
		{
			"an OpenAI key never switches auto to the paid API",
			config.Config{OpenAIKey: "k", LLMModel: "m1"},
			maxName + "m1",
		},
		{
			"the explicit anthropic backend uses ANTHROPIC_MODEL, not a role alias",
			config.Config{
				LLMBackend: llm.BackendAnthropic, AnthropicAPIKey: "k", AnthropicProxy: proxyURL, AnthropicModel: modelX,
			},
			"anthropic:claude-x",
		},
		{
			"an explicit LLM_MODEL beats ANTHROPIC_MODEL",
			config.Config{
				LLMBackend: llm.BackendAnthropic, AnthropicAPIKey: "k", AnthropicProxy: proxyURL, AnthropicModel: modelX,
				LLMModel: "claude-y",
			},
			"anthropic:claude-y",
		},
		{
			"a role variable beats LLM_MODEL for the anthropic backend",
			config.Config{
				LLMBackend: llm.BackendAnthropic, AnthropicAPIKey: "k", AnthropicProxy: proxyURL, AnthropicModel: modelX,
				LLMModel: "claude-y", LLMModelCritique: "claude-z",
			},
			"anthropic:claude-z",
		},
		{
			"an explicit claude-cli wins over keys",
			config.Config{LLMBackend: llm.BackendClaudeCLI, AnthropicAPIKey: "k", LLMCommand: wrapperZai},
			"claude-cli:claude-zai/sonnet",
		},
		{
			"an explicit openai-compat uses LLM_MODEL",
			config.Config{LLMBackend: llm.BackendOpenAICompat, OpenAIKey: "k", OpenAIProxy: proxyURL, LLMModel: "m1"},
			"openai-compat:m1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.LLMTimeout = time.Minute

			c, err := BuildCompleter(&tt.cfg, quietLog(), CompleterSpec{Role: llm.RoleCritique})
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, c.Name())
		})
	}
}

func TestBuildCompleterBackendErrors(t *testing.T) {
	spec := CompleterSpec{Role: llm.RoleCritique}

	t.Run("openai-compat needs a model", func(t *testing.T) {
		_, err := BuildCompleter(openAICompatConfig(), quietLog(), spec)
		require.ErrorContains(t, err, "LLM_MODEL")

		spec.Model = "stage-model"
		c, err := BuildCompleter(openAICompatConfig(), quietLog(), spec)
		require.NoError(t, err, "a role model is enough")
		assert.Equal(t, "openai-compat:stage-model", c.Name())
	})

	t.Run("anthropic needs a model", func(t *testing.T) {
		cfg := config.Config{LLMBackend: llm.BackendAnthropic, AnthropicAPIKey: "k", AnthropicProxy: proxyURL}
		_, err := BuildCompleter(&cfg, quietLog(), CompleterSpec{Role: llm.RoleCritique})
		require.ErrorContains(t, err, "ANTHROPIC_MODEL")
	})

	t.Run("a bad proxy is refused without echoing it", func(t *testing.T) {
		cfg := config.Config{
			LLMBackend: llm.BackendAnthropic, AnthropicAPIKey: "k", AnthropicModel: modelX,
			AnthropicProxy: "user:s3cret@vps.example.org",
		}
		_, err := BuildCompleter(&cfg, quietLog(), CompleterSpec{Role: llm.RoleCritique})
		require.ErrorIs(t, err, llm.ErrBadProxy)
		assert.NotContains(t, err.Error(), "s3cret") //nolint:forbidigo // checks the message text for a secret
	})

	t.Run("the bare CLI is refused", func(t *testing.T) {
		_, err := BuildCompleter(&config.Config{}, quietLog(), CompleterSpec{Role: llm.RoleCritique, Command: bareCLI})
		require.ErrorContains(t, err, "not the bare claude CLI")
	})
}

// Model precedence per role: the role's flag, LLM_MODEL_<ROLE>, --llm-model,
// LLM_MODEL, the role default (the synthesis default is fable with --deep).
func TestModelPrecedence(t *testing.T) {
	full := config.Config{
		LLMModel: modelEnv, LLMModelPlan: "plan-env", LLMModelCritique: "crit-env",
		LLMModelDeep: "deep-env", LLMModelSynthesis: synthEnv, LLMModelAsk: "ask-env",
	}

	tests := []struct {
		name string
		cfg  config.Config
		spec CompleterSpec
		want string
	}{
		{"role flag wins", full, CompleterSpec{Role: llm.RoleCritique, Model: "flag", RunModel: modelRun}, "flag"},
		{"role variable beats --llm-model", full, CompleterSpec{Role: llm.RoleCritique, RunModel: modelRun}, "crit-env"},
		{
			"--llm-model beats LLM_MODEL",
			config.Config{LLMModel: modelEnv},
			CompleterSpec{Role: llm.RoleAsk, RunModel: modelRun},
			modelRun,
		},
		{"LLM_MODEL beats the default", config.Config{LLMModel: modelEnv}, CompleterSpec{Role: llm.RolePlan}, modelEnv},
		{"plan variable", full, CompleterSpec{Role: llm.RolePlan}, "plan-env"},
		{"deep variable", full, CompleterSpec{Role: llm.RoleDeep}, "deep-env"},
		{"synthesis variable beats --deep", full, CompleterSpec{Role: llm.RoleSynthesis, Deep: true}, synthEnv},
		{"ask variable", full, CompleterSpec{Role: llm.RoleAsk}, "ask-env"},
		{"plan default", config.Config{}, CompleterSpec{Role: llm.RolePlan}, llm.ModelSonnet},
		{"critique default", config.Config{}, CompleterSpec{Role: llm.RoleCritique}, llm.ModelSonnet},
		{"deep default", config.Config{}, CompleterSpec{Role: llm.RoleDeep}, llm.ModelOpus},
		{"synthesis default", config.Config{}, CompleterSpec{Role: llm.RoleSynthesis}, llm.ModelOpus},
		{"synthesis default with --deep", config.Config{}, CompleterSpec{Role: llm.RoleSynthesis, Deep: true}, "fable"},
		{
			"--deep leaves the other roles alone",
			config.Config{},
			CompleterSpec{Role: llm.RoleCritique, Deep: true},
			llm.ModelSonnet,
		},
		{
			"an explicit synthesis flag beats --deep",
			config.Config{},
			CompleterSpec{Role: llm.RoleSynthesis, Deep: true, Model: llm.ModelOpus},
			llm.ModelOpus,
		},
		{"ask default", config.Config{}, CompleterSpec{Role: llm.RoleAsk}, llm.ModelSonnet},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.LLMTimeout = time.Minute

			c, err := BuildCompleter(&tt.cfg, quietLog(), tt.spec)
			require.NoError(t, err)
			assert.True(t, strings.HasSuffix(c.Name(), "/"+tt.want), "got %s, want model %s", c.Name(), tt.want)
		})
	}
}

// recordingWrapper puts an executable named command first on PATH. It
// records its arguments and answers with a minimal envelope, so a test sees
// the exact argument list the backend builds without any real CLI.
func recordingWrapper(t *testing.T, command string) (argsFile string) {
	t.Helper()

	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s ' \"$@\" > " + argsFile + "\ncat > /dev/null\n" +
		`printf '{"result":"ok","usage":{"input_tokens":1,"output_tokens":1}}'` + "\n"

	//nolint:gosec // an executable test stand-in
	require.NoError(t, os.WriteFile(filepath.Join(dir, command), []byte(script), 0o700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return argsFile
}

func argsOf(t *testing.T, c pipeline.Completer, argsFile string) string {
	t.Helper()

	_, _, err := c.Complete(context.Background(), "sys", "user", pipeline.CompleteOptions{})
	require.NoError(t, err)

	b, err := os.ReadFile(argsFile) //nolint:gosec // the path is the test's own temp file
	require.NoError(t, err)

	return string(b)
}

// Effort and the fallback model go only to claude-max: the GLM and DeepSeek
// gateways disable adaptive thinking.
func TestEffortAndFallbackOnlyForClaudeMax(t *testing.T) {
	cfg := config.Config{LLMTimeout: time.Minute}

	t.Run("claude-max synthesis", func(t *testing.T) {
		argsFile := recordingWrapper(t, wrapperMax)

		c, err := BuildCompleter(&cfg, quietLog(), CompleterSpec{Role: llm.RoleSynthesis})
		require.NoError(t, err)
		assert.Contains(t, argsOf(t, c, argsFile), "--model opus --effort high --fallback-model sonnet")
	})

	t.Run("claude-max fable also falls back to sonnet", func(t *testing.T) {
		argsFile := recordingWrapper(t, wrapperMax)

		c, err := BuildCompleter(&cfg, quietLog(), CompleterSpec{Role: llm.RoleSynthesis, Deep: true})
		require.NoError(t, err)
		assert.Contains(t, argsOf(t, c, argsFile), "--model fable --effort high --fallback-model sonnet")
	})

	t.Run("claude-max critique has no fallback", func(t *testing.T) {
		argsFile := recordingWrapper(t, wrapperMax)

		c, err := BuildCompleter(&cfg, quietLog(), CompleterSpec{Role: llm.RoleCritique})
		require.NoError(t, err)

		got := argsOf(t, c, argsFile)
		assert.Contains(t, got, "--model sonnet --effort low")
		assert.NotContains(t, got, "--fallback-model")
	})

	t.Run("claude-max ask is medium", func(t *testing.T) {
		argsFile := recordingWrapper(t, wrapperMax)

		c, err := BuildCompleter(&cfg, quietLog(), CompleterSpec{Role: llm.RoleAsk})
		require.NoError(t, err)
		assert.Contains(t, argsOf(t, c, argsFile), "--model sonnet --effort medium")
	})

	t.Run("another wrapper gets the model alone", func(t *testing.T) {
		argsFile := recordingWrapper(t, wrapperDS)

		c, err := BuildCompleter(&cfg, quietLog(), CompleterSpec{Role: llm.RoleSynthesis, Command: wrapperDS})
		require.NoError(t, err)

		got := argsOf(t, c, argsFile)
		assert.Contains(t, got, "--model opus")
		assert.NotContains(t, got, "--effort")
		assert.NotContains(t, got, "--fallback-model")
		assert.Contains(t, got, "--safe-mode")
	})
}

func TestBuildCompleterLogsTheResolvedWrapper(t *testing.T) {
	tests := []struct {
		name     string
		cfg      config.Config
		wantLogs []string
		noWarn   bool
	}{
		{
			"default",
			config.Config{},
			[]string{"command=claude-max", "source=default", "role=synthesis", "model=opus", "effort=high"},
			true,
		},
		{
			"session",
			config.Config{ClaudeWrapper: wrapperZai, LLMCommand: wrapperDS},
			[]string{"command=claude-zai", "source=session", "effort=\"(not passed)\""},
			true,
		},
		{".env", config.Config{LLMCommand: wrapperDS}, []string{"command=claude-ds", "source=.env"}, true},
		{
			".env with the gateway wrapper warns",
			config.Config{LLMCommand: "claude-zai-flash"},
			[]string{"command=claude-zai-flash", "source=.env", "one-way cooldown"},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, buf := bufLog()
			tt.cfg.LLMTimeout = time.Minute

			_, err := BuildCompleter(&tt.cfg, log, CompleterSpec{Role: llm.RoleSynthesis})
			require.NoError(t, err)

			for _, want := range tt.wantLogs {
				assert.Contains(t, buf.String(), want)
			}

			if tt.noWarn {
				assert.NotContains(t, buf.String(), "one-way cooldown", "the warning is for .env/flag runs on the gateway wrapper")
			}
		})
	}
}

// The three pipeline roles get their own backends with their own models.
func TestBuildClientsGivesEachRoleItsModel(t *testing.T) {
	cfg := config.Config{LLMTimeout: time.Minute}

	def, err := BuildClients(&cfg, quietLog(), pipeline.Options{})
	require.NoError(t, err)
	assert.Equal(t, maxName+llm.ModelSonnet, def.LLM.Name())
	assert.Equal(t, maxName+llm.ModelOpus, def.LLMDeep.Name())
	assert.Equal(t, maxName+llm.ModelOpus, def.LLMSynthesis.Name())
	assert.NotNil(t, def.Citations)
	assert.NotNil(t, def.Works)
	assert.NotNil(t, def.PDFs)
	assert.NotNil(t, def.PDFText)

	deep, err := BuildClients(&cfg, quietLog(), pipeline.Options{Deep: true})
	require.NoError(t, err)
	assert.Equal(t, maxName+"fable", deep.LLMSynthesis.Name(), "--deep lifts the judge")
	assert.Equal(t, maxName+llm.ModelOpus, deep.LLMDeep.Name())

	cfg.LLMModelSynthesis = synthEnv
	run, err := BuildClients(&cfg, quietLog(), pipeline.Options{
		LLMCommand: wrapperDS, ModelCritique: "haiku", ModelDeep: llm.ModelSonnet, Deep: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "claude-cli:claude-ds/haiku", run.LLM.Name(), "the role flag wins")
	assert.Equal(t, "claude-cli:claude-ds/sonnet", run.LLMDeep.Name())
	assert.Equal(t, "claude-cli:claude-ds/synth-env", run.LLMSynthesis.Name(), "the role variable beats --deep")
}

func TestBuildClientsPropagatesBackendErrors(t *testing.T) {
	cfg := openAICompatConfig()

	_, err := BuildClients(cfg, quietLog(), pipeline.Options{})
	require.Error(t, err)

	_, err = BuildPipeline(cfg, quietLog(), pipeline.Options{})
	require.Error(t, err)

	_, err = BuildClients(&config.Config{}, quietLog(), pipeline.Options{LLMCommand: bareCLI})
	require.ErrorContains(t, err, "bare claude CLI")
}

func TestPipelineSettings(t *testing.T) {
	got := PipelineSettings(&config.Config{
		MaxPapers: 15, Concurrency: 4, LLMConcurrency: 3, LLMChunk: 5, ConsensusPages: 2,
		TextMode: pipeline.TextFull, HybridPivotalMax: 6, EnrichCacheTTLDays: 30, CritiqueCacheTTLDays: 0,
	})

	assert.Equal(t, pipeline.Settings{
		MaxPapers: 15, Concurrency: 4, LLMConcurrency: 3, LLMChunk: 5, ConsensusPages: 2,
		TextMode: pipeline.TextFull, HybridPivotalMax: 6,
		EnrichCacheTTL: 30 * 24 * time.Hour, CritiqueCacheTTL: 0,
	}, got, "cache TTLs in days become durations; zero stays zero (cache off)")
}

func TestBuildPipeline(t *testing.T) {
	cfg := config.Config{LLMTimeout: time.Minute}

	p, err := BuildPipeline(&cfg, quietLog(), pipeline.Options{})
	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestVersionFallsBackToBuildInfo(t *testing.T) {
	// under `go test` there is no VCS stamp for the test binary: dev
	assert.NotEmpty(t, Version(""))
	assert.NotEmpty(t, Version(DevVersion))
}
