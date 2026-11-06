// Package httpx is the shared HTTP layer for all external APIs: rate
// limiting, retries on network errors and 429/5xx with exponential backoff
// and Retry-After support, a response size cap, plus a JSON codec.
//
// Every request this program sends is a query or a model completion without
// side effects, so a repeated request is harmless; the retry loop relies on
// that and does not distinguish methods.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

const (
	// defaultTimeout is the timeout of one request.
	defaultTimeout = 90 * time.Second
	attempts       = 4
	// defaultBackoff is the pause before the second attempt; it doubles with
	// every further one.
	defaultBackoff = 400 * time.Millisecond
	// maxRetryAfter caps how long a server may ask us to wait.
	maxRetryAfter = 2 * time.Minute
	// maxJSONBytes caps a JSON response.
	maxJSONBytes = 32 << 20
	// bodySnippetBytes is how much of an error body goes into StatusError.
	bodySnippetBytes = 300
)

// StatusError is a final (non-retryable) HTTP response; the status code is
// available to callers (e.g. OpenAlex tells a 404 "unknown DOI" apart from a
// real failure).
type StatusError struct {
	Name   string
	Status int
	Body   string

	retryAfter time.Duration // the server's Retry-After, zero when absent
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("HTTP %d from %s: %s", e.Status, e.Name, e.Body)
}

// Client sends requests to one external API.
type Client struct {
	name    string
	hc      *http.Client
	rl      *rate.Limiter
	backoff time.Duration
}

// Option adjusts a Client at construction.
type Option func(*http.Client)

// WithTimeout sets the timeout of one request (the default is 90 seconds):
// a model completion of thousands of tokens takes longer.
func WithTimeout(d time.Duration) Option {
	return func(hc *http.Client) { hc.Timeout = d }
}

// WithTransport replaces the HTTP transport (a proxy, for instance).
func WithTransport(rt http.RoundTripper) Option {
	return func(hc *http.Client) { hc.Transport = rt }
}

// New returns a client with a limit of qps requests per second
// (burst equals qps, at least 1). name identifies the API in errors.
func New(name string, qps int, opts ...Option) *Client {
	if qps < 1 {
		qps = 1
	}

	hc := &http.Client{Timeout: defaultTimeout}
	for _, o := range opts {
		o(hc)
	}

	return &Client{
		name:    name,
		hc:      hc,
		rl:      rate.NewLimiter(rate.Limit(qps), qps),
		backoff: defaultBackoff,
	}
}

// DoJSON performs a request; body and out may be nil.
func (c *Client) DoJSON(ctx context.Context, method, url string, body, out any, headers map[string]string) error {
	r := request{method: method, url: url, accept: "application/json", headers: headers, maxBytes: maxJSONBytes}

	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding the request for %s: %w", c.name, err)
		}

		r.body = payload
	}

	data, err := c.send(ctx, r)
	if err != nil {
		return err
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding the response from %s: %w", c.name, err)
	}

	return nil
}

// Get fetches a raw resource with the shared retry/rate-limit machinery
// (OA PDF downloads, Europe PMC full-text XML). maxBytes caps the response; a larger one is an
// error rather than a silently truncated file.
func (c *Client) Get(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	return c.send(ctx, request{
		method: http.MethodGet, url: url, accept: "application/pdf,*/*", maxBytes: maxBytes,
	})
}

type request struct {
	method, url string
	body        []byte // nil: no body
	accept      string
	headers     map[string]string
	maxBytes    int64
}

// send is the one retry loop: it returns the body of the first 2xx response,
// retrying network errors, truncated bodies and retryable statuses.
func (c *Client) send(ctx context.Context, r request) ([]byte, error) {
	var lastErr error

	for attempt := range attempts {
		if attempt > 0 {
			if err := sleep(ctx, c.delay(attempt, lastErr)); err != nil {
				return nil, fmt.Errorf("waiting to retry %s: %w", c.name, err)
			}
		}

		if err := c.rl.Wait(ctx); err != nil {
			return nil, fmt.Errorf("waiting for the %s rate limit: %w", c.name, err)
		}

		data, err := c.attempt(ctx, r)
		if err == nil {
			return data, nil
		}

		if ctx.Err() != nil {
			return nil, fmt.Errorf("requesting %s: %w", c.name, ctx.Err())
		}

		if !retryable(err) {
			return nil, err
		}

		lastErr = err
	}

	return nil, fmt.Errorf("giving up on %s after %d attempts: %w", c.name, attempts, lastErr)
}

// attempt performs one request. A response with a non-2xx status is a
// *StatusError; a transport failure or an unreadable body is wrapped as is.
func (c *Client) attempt(ctx context.Context, r request) ([]byte, error) {
	var reader io.Reader
	if r.body != nil {
		reader = bytes.NewReader(r.body)
	}

	req, err := http.NewRequestWithContext(ctx, r.method, r.url, reader)
	if err != nil {
		return nil, &fatalError{fmt.Errorf("building the request for %s: %w", c.name, err)}
	}

	req.Header.Set("Accept", r.accept)

	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for k, v := range r.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", c.name, err)
	}

	// the body is read below; a failure to close a read body changes nothing
	defer func() { _ = resp.Body.Close() }() // the body is already read; a close failure loses nothing

	data, err := readCapped(resp.Body, r.maxBytes)
	if err != nil {
		return nil, c.bodyError(err)
	}

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return data, nil
	}

	return nil, &StatusError{
		Name: c.name, Status: resp.StatusCode, Body: snippet(data),
		retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}
}

// readCapped reads the whole body. A body longer than max is an error:
// truncating would hand the caller a broken document.
func readCapped(body io.Reader, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the body: %w", err)
	}

	if int64(len(data)) > maxBytes {
		return nil, errBodyTooLarge
	}

	return data, nil
}

var errBodyTooLarge = errors.New("response body exceeds the size cap")

// bodyError wraps a body read failure; an oversized body is final, a broken
// connection mid-body is a network error worth retrying.
func (c *Client) bodyError(err error) error {
	if errors.Is(err, errBodyTooLarge) {
		return &fatalError{fmt.Errorf("reading the response from %s: %w", c.name, err)}
	}

	return fmt.Errorf("reading the response from %s: %w", c.name, err)
}

// fatalError marks an error that a retry cannot fix.
type fatalError struct{ error }

func (e *fatalError) Unwrap() error { return e.error }

// retryable reports whether another attempt may succeed.
func retryable(err error) bool {
	if _, fatal := errors.AsType[*fatalError](err); fatal {
		return false
	}

	if se, ok := errors.AsType[*StatusError](err); ok {
		return se.Status == http.StatusTooManyRequests ||
			(se.Status >= http.StatusInternalServerError && se.Status <= http.StatusGatewayTimeout)
	}

	return true
}

// delay is the pause before the given (second or later) attempt: exponential
// backoff with jitter, or the server's Retry-After when that is longer.
func (c *Client) delay(attempt int, prev error) time.Duration {
	//nolint:gosec // jitter between retries, not a security-sensitive random number
	d := c.backoff<<attempt + rand.N(c.backoff/2+1)

	if se, ok := errors.AsType[*StatusError](prev); ok {
		d = max(d, min(se.retryAfter, maxRetryAfter))
	}

	return d
}

// parseRetryAfter reads a Retry-After header in either of its forms (seconds
// or an HTTP date); zero means absent or unusable.
func parseRetryAfter(h string, now time.Time) time.Duration {
	if h == "" {
		return 0
	}

	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(max(secs, 0)) * time.Second
	}

	if t, err := http.ParseTime(h); err == nil {
		return max(t.Sub(now), 0)
	}

	return 0
}

// sleep waits for d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting was interrupted: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}

func snippet(b []byte) string {
	if len(b) > bodySnippetBytes {
		b = b[:bodySnippetBytes]
	}

	return string(b)
}
