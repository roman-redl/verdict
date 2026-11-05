package llm

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/pipeline"
)

//nolint:gosec // a fake proxy URL with a fake password: the tests never touch the network
const testProxy = "https://user:s3cret@vps.example.org:443"

// The personal key goes to the pinned address, whatever the environment says.
func TestAnthropicBaseIsPinned(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://gateway.corp.example")

	a, err := NewAnthropic("sk-personal", "claude-sonnet-5", testProxy)
	require.NoError(t, err)
	assert.Equal(t, "https://api.anthropic.com", a.base)
	assert.Equal(t, "anthropic:claude-sonnet-5", a.Name())
}

func TestProxyTransport(t *testing.T) {
	tr, err := ProxyTransport(testProxy)
	require.NoError(t, err)

	got, err := tr.Proxy(&http.Request{URL: mustParse(t, "https://api.anthropic.com/v1/messages")})
	require.NoError(t, err)
	assert.Equal(t, "vps.example.org:443", got.Host)
	assert.Equal(t, "user", got.User.Username())

	assert.False(t, tr.ForceAttemptHTTP2, "h2 is switched off: the proxy is spoken to over HTTP/1.1")
	assert.NotNil(t, tr.TLSNextProto)
	assert.Empty(t, tr.TLSNextProto)
	assert.Equal(t, []string{"http/1.1"}, tr.TLSClientConfig.NextProtos)
}

// A bad proxy is refused without echoing it: the URL carries the credentials.
func TestProxyTransportRejectsBadURLs(t *testing.T) {
	for _, bad := range []string{"", "vps.example.org:443", "socks5://u:p@h:1", "https://", "http://u:s3cret@ho st:1"} {
		t.Run(bad, func(t *testing.T) {
			_, err := ProxyTransport(bad)
			require.ErrorIs(t, err, ErrBadProxy)
			assert.NotContains(t, err.Error(), "s3cret") //nolint:forbidigo // checks the message text for a secret
		})
	}
}

// The request leaves through the proxy, with the proxy credentials; the
// target is the pinned host, here replaced by a fake one.
func TestAnthropicGoesThroughTheProxy(t *testing.T) {
	var gotHost, gotAuth string

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotAuth = r.Host, r.Header.Get("Proxy-Authorization")

		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"via proxy"}],"usage":{"input_tokens":1}}`))
	}))
	defer proxy.Close()

	rt, err := ProxyTransport(strings.Replace(proxy.URL, "http://", "http://user:pw@", 1))
	require.NoError(t, err)

	a := newAnthropic("http://api.anthropic.test", "k", "m", rt)

	text, _, err := a.Complete(t.Context(), "s", "u", pipeline.CompleteOptions{})
	require.NoError(t, err)

	assert.Equal(t, "via proxy", text)
	assert.Equal(t, "api.anthropic.test", gotHost)
	assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pw")), gotAuth)
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()

	u, err := url.Parse(raw)
	require.NoError(t, err)

	return u
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

	a := newAnthropic(srv.URL, "sk-test", "claude-test", nil)

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

	a := newAnthropic(srv.URL, "k", "m", nil)

	_, _, err := a.Complete(t.Context(), "s", "u", pipeline.CompleteOptions{MaxTokens: 123})

	require.ErrorContains(t, err, "max_tokens", "the stop reason explains the empty answer")
	assert.Equal(t, 123, gotMax)
}

func TestAnthropicHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid x-api-key"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, _, err := newAnthropic(srv.URL, "bad", "m", nil).Complete(t.Context(), "s", "u", pipeline.CompleteOptions{})
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

	o, err := NewOpenAICompat(srv.URL+"/v1", "sk-test", "gpt-test", "")
	require.NoError(t, err)
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

	o, err := NewOpenAICompat(srv.URL, "k", "m", "")
	require.NoError(t, err)

	_, _, err = o.Complete(t.Context(), "s", "u", pipeline.CompleteOptions{})
	require.ErrorContains(t, err, "empty completion")

	def, err := NewOpenAICompat("", "k", "m", "https://user:pw@vps.example.org:443")
	require.NoError(t, err)
	assert.Equal(t, "https://api.openai.com/v1", def.base)
}

// A paid personal-key provider without a proxy is refused by the constructor
// itself, not only by config.Validate.
func TestOpenAICompatRefusesPaidProviderWithoutProxy(t *testing.T) {
	for _, base := range []string{"", "https://api.openai.com/v1", "https://api.groq.com/openai/v1"} {
		_, err := NewOpenAICompat(base, "k", "m", "")
		require.ErrorIs(t, err, ErrProxyRequired, base)
	}

	_, err := NewOpenAICompat("https://api.deepseek.com/v1", "k", "m", "")
	require.NoError(t, err)
}

// With a proxy every request leaves through it, with the proxy credentials.
func TestOpenAICompatGoesThroughTheProxy(t *testing.T) {
	var gotHost, gotAuth string

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotAuth = r.Host, r.Header.Get("Proxy-Authorization")

		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"via proxy"}}]}`))
	}))
	defer proxy.Close()

	o, err := NewOpenAICompat("http://llm.example.test/v1", "k", "m",
		strings.Replace(proxy.URL, "http://", "http://user:pw@", 1))
	require.NoError(t, err)

	text, _, err := o.Complete(t.Context(), "s", "u", pipeline.CompleteOptions{})
	require.NoError(t, err)

	assert.Equal(t, "via proxy", text)
	assert.Equal(t, "llm.example.test", gotHost)
	assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pw")), gotAuth)
}

func TestOpenAICompatRejectsABadProxy(t *testing.T) {
	_, err := NewOpenAICompat("", "k", "m", "vps.example.org:443")
	require.ErrorIs(t, err, ErrBadProxy)
}

// The paid personal-key providers are reached only through the proxy; other
// hosts may use one optionally.
func TestProxyRequired(t *testing.T) {
	tests := []struct {
		base string
		want bool
	}{
		{"", true}, // the default base is api.openai.com
		{"https://api.openai.com/v1", true},
		{"https://API.OpenAI.com/v1", true},
		{"https://api.openai.com.:443/v1", true},
		{"https://eu.api.openai.com/v1", true},
		{"https://openai.com", true},
		{"https://api.groq.com/openai/v1", true},
		{"https://x.groq.com", true},
		{"https://api.deepseek.com/v1", false},
		{"https://openrouter.ai/api/v1", false},
		{"http://localhost:8000/v1", false},
		{"https://notopenai.com/v1", false},
		{"https://openai.com.evil.test/v1", false},
		{"https://gateway.test/openai.com/v1", false},
		{"https://gateway.test/v1?host=api.groq.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.base, func(t *testing.T) {
			got, err := ProxyRequired(tt.base)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	for _, bad := range []string{"://nope", "not a url", "https://"} {
		_, err := ProxyRequired(bad)
		require.ErrorIs(t, err, ErrBadBaseURL, bad)
	}
}
