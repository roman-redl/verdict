package llm

import (
	"cmp"
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// ErrBadProxy is a proxy URL that cannot be used.
var ErrBadProxy = errors.New("the proxy must be a URL like https://user:pass@host:443")

// ProxyTransport returns an HTTP transport that goes through an HTTPS CONNECT
// proxy over HTTP/1.1 only.
//
// The proxy (Caddy on the VPS) offers HTTP/2 through ALPN, while Go talks to
// an HTTPS proxy only over HTTP/1.1. Clone() inherits ForceAttemptHTTP2=true,
// which quietly mixes h2 into ALPN on top of our forcing — so h2 is switched
// off on the transport as a whole.
func ProxyTransport(proxy string) (*http.Transport, error) {
	u, err := url.Parse(proxy)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		// url.Parse errors quote the URL, credentials included: not wrapped
		return nil, ErrBadProxy
	}

	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("the default HTTP transport is not an *http.Transport")
	}

	transport := tr.Clone()
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	transport.TLSClientConfig = &tls.Config{NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12}
	transport.Proxy = http.ProxyURL(u)

	return transport, nil
}

// paidAPIDomains are the providers whose personal paid API keys go out only
// through the VPS proxy, never from the local address: calls from the
// owner's own (Belarusian or Russian) IP risk the account and the card. A
// host is covered when it is one of these domains or a subdomain of one.
var paidAPIDomains = []string{"openai.com", "groq.com"}

// ErrProxyRequired is a paid personal-key provider configured without a proxy.
var ErrProxyRequired = errors.New("personal paid API keys never go out directly: this endpoint requires a proxy")

// ErrBadBaseURL is an API base URL that cannot be used.
var ErrBadBaseURL = errors.New("the base URL must be a URL like https://api.example.org/v1")

// ProxyRequired reports whether the API base URL (empty = the OpenAI
// default) belongs to a paid personal-key provider, which may be reached only
// through a proxy. Other hosts (DeepSeek, OpenRouter, a local vLLM) may use a
// proxy optionally.
func ProxyRequired(base string) (bool, error) {
	u, err := url.Parse(cmp.Or(base, openAIDefaultBase))
	if err != nil || u.Hostname() == "" {
		// url.Parse errors quote the URL: not wrapped
		return false, ErrBadBaseURL
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")

	for _, d := range paidAPIDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true, nil
		}
	}

	return false, nil
}
