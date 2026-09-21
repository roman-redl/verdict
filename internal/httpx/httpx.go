// Package httpx is the shared HTTP layer for all external APIs: rate
// limiting, retries on network errors and 429/5xx with exponential backoff
// and Retry-After support, plus a JSON codec.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

// StatusError is a final (non-retryable) HTTP response; the status code is
// available to callers (e.g. OpenAlex tells a 404 "unknown DOI" apart from a
// real failure).
type StatusError struct {
	Name   string
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Name, e.Status, e.Body)
}

type Client struct {
	Name string
	hc   *http.Client
	rl   *rate.Limiter
}

// New returns a client with a limit of qps requests per second
// (burst equals qps, at least 1).
func New(name string, qps int) *Client {
	if qps < 1 {
		qps = 1
	}
	return &Client{
		Name: name,
		hc:   &http.Client{Timeout: 90 * time.Second},
		rl:   rate.NewLimiter(rate.Limit(qps), qps),
	}
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || (status >= 500 && status <= 504)
}

// DoJSON performs a request; body and out may be nil.
func (c *Client) DoJSON(ctx context.Context, method, url string, body, out any, headers map[string]string) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("%s: encoding request: %w", c.Name, err)
		}
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		if err := c.rl.Wait(ctx); err != nil {
			return err
		}
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", c.Name, err)
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out == nil {
				return nil
			}
			return json.Unmarshal(data, out)
		}
		lastErr = &StatusError{Name: c.Name, Status: resp.StatusCode, Body: snippet(data)}
		if !retryable(resp.StatusCode) {
			return lastErr
		}
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(secs) * time.Second):
			}
		}
	}
	return fmt.Errorf("%s: retries exhausted: %w", c.Name, lastErr)
}

func backoff(attempt int) time.Duration {
	return time.Duration(400<<attempt)*time.Millisecond + time.Duration(rand.Int64N(150))*time.Millisecond
}

// Get fetches a raw resource with the shared retry/rate-limit machinery
// (used for OA PDF downloads). maxBytes caps the response.
func (c *Client) Get(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		if err := c.rl.Wait(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/pdf,*/*")
		resp, err := c.hc.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", c.Name, err)
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return data, nil
		}
		lastErr = &StatusError{Name: c.Name, Status: resp.StatusCode, Body: snippet(data)}
		if !retryable(resp.StatusCode) {
			return nil, lastErr
		}
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(secs) * time.Second):
			}
		}
	}
	return nil, fmt.Errorf("%s: retries exhausted: %w", c.Name, lastErr)
}

func snippet(b []byte) string {
	if len(b) > 300 {
		b = b[:300]
	}
	return string(b)
}
