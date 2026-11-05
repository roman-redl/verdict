package httpx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const works = "works" // a path segment the URL tests share

func errorsAsStatus(err error) (*StatusError, bool) { return errors.AsType[*StatusError](err) }

// fast returns a client whose retry pauses are negligible.
func fast(name string) *Client {
	c := New(name, 1000)
	c.backoff = time.Millisecond

	return c
}

func TestDoJSONRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "secret", r.Header.Get("x-api-key"))

		body, _ := io.ReadAll(r.Body)
		assert.JSONEq(t, `{"q":"creatine"}`, string(body))

		_, _ = w.Write([]byte(`{"answer":42}`))
	}))
	defer srv.Close()

	var out struct {
		Answer int `json:"answer"`
	}

	err := fast("test").DoJSON(t.Context(), http.MethodPost, srv.URL,
		map[string]string{"q": "creatine"}, &out, map[string]string{"x-api-key": "secret"})
	require.NoError(t, err)
	assert.Equal(t, 42, out.Answer)
}

func TestDoJSONWithoutBodyOrOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Content-Type"), "no body, no content type")

		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	require.NoError(t, fast("test").DoJSON(t.Context(), http.MethodGet, srv.URL, nil, nil, nil))
}

// Retryable statuses are retried with the same body until one succeeds.
func TestRetriesRetryableStatuses(t *testing.T) {
	for _, status := range []int{
		http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				assert.JSONEq(t, `{"n":1}`, string(body), "every attempt must resend the body")

				if calls.Add(1) < 3 {
					w.WriteHeader(status)
					return
				}

				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()

			var out struct {
				OK bool `json:"ok"`
			}

			err := fast("test").DoJSON(t.Context(), http.MethodPost, srv.URL, map[string]int{"n": 1}, &out, nil)
			require.NoError(t, err)
			assert.True(t, out.OK)
			assert.EqualValues(t, 3, calls.Load())
		})
	}
}

// A final status is returned at once, typed, with the body snippet.
func TestFinalStatusIsNotRetried(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "no such paper", http.StatusNotFound)
	}))
	defer srv.Close()

	err := fast("openalex").DoJSON(t.Context(), http.MethodGet, srv.URL, nil, nil, nil)

	se, ok := errorsAsStatus(err)
	require.True(t, ok, "want *StatusError, got %v", err)
	assert.Equal(t, http.StatusNotFound, se.Status)
	assert.Contains(t, se.Body, "no such paper")
	assert.Contains(t, err.Error(), "openalex")
	assert.EqualValues(t, 1, calls.Load())
}

func TestRetriesExhausted(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	err := fast("test").DoJSON(t.Context(), http.MethodGet, srv.URL, nil, nil, nil)

	se, ok := errorsAsStatus(err)
	require.True(t, ok, "the last status must stay reachable through the chain")
	assert.Equal(t, http.StatusServiceUnavailable, se.Status)
	assert.EqualValues(t, attempts, calls.Load())
	assert.ErrorContains(t, err, "giving up")
}

// A refused connection is a network error: retried, then reported.
func TestNetworkErrorsAreRetried(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	closedURL := srv.URL
	srv.Close()

	start := time.Now()
	err := fast("test").DoJSON(t.Context(), http.MethodGet, closedURL, nil, nil, nil)

	require.ErrorContains(t, err, "giving up")
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestMalformedJSONIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"answer":`))
	}))
	defer srv.Close()

	var out map[string]any

	err := fast("test").DoJSON(t.Context(), http.MethodGet, srv.URL, nil, &out, nil)
	assert.ErrorContains(t, err, "decoding the response")
}

// Cancelling the context ends the retry loop instead of exhausting it.
func TestContextCancelStopsRetries(t *testing.T) {
	var calls atomic.Int32

	ctx, cancel := context.WithCancel(t.Context())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			cancel()
		}

		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New("test", 1000)
	c.backoff = time.Hour // a retry pause that would hang the test unless it is interruptible

	err := c.DoJSON(ctx, http.MethodGet, srv.URL, nil, nil, nil)

	require.ErrorIs(t, err, context.Canceled)
	assert.EqualValues(t, 1, calls.Load())
}

func TestGetReturnsRawBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.Header.Get("Accept"), "application/pdf")

		_, _ = w.Write([]byte("%PDF-1.4 body"))
	}))
	defer srv.Close()

	data, err := fast("pdf").Get(t.Context(), srv.URL, 1<<20)
	require.NoError(t, err)
	assert.Equal(t, "%PDF-1.4 body", string(data))
}

// An oversized response is an error, not a silently truncated document.
func TestResponseSizeCap(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)

		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer srv.Close()

	_, err := fast("pdf").Get(t.Context(), srv.URL, 99)
	require.ErrorContains(t, err, "size cap")
	assert.EqualValues(t, 1, calls.Load(), "an oversized response is final, not retried")

	data, err := fast("pdf").Get(t.Context(), srv.URL, 100)
	require.NoError(t, err, "a response exactly at the cap is fine")
	assert.Len(t, data, 100)
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"absent", "", 0},
		{"seconds", "7", 7 * time.Second},
		{"zero seconds", "0", 0},
		{"negative is ignored", "-3", 0},
		{"http date", now.Add(30 * time.Second).UTC().Format(http.TimeFormat), 30 * time.Second},
		{"date in the past", now.Add(-time.Minute).UTC().Format(http.TimeFormat), 0},
		{"garbage", "soon", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, parseRetryAfter(tt.header, now))
		})
	}
}

// The pause is the exponential backoff, or Retry-After when that is longer,
// but never beyond the cap.
func TestDelay(t *testing.T) {
	c := New("test", 1)
	c.backoff = 100 * time.Millisecond

	for attempt, base := range map[int]time.Duration{1: 200 * time.Millisecond, 2: 400 * time.Millisecond} {
		d := c.delay(attempt, nil)
		assert.GreaterOrEqual(t, d, base)
		assert.LessOrEqual(t, d, base+c.backoff/2)
	}

	long := &StatusError{retryAfter: 5 * time.Second}
	assert.Equal(t, 5*time.Second, c.delay(1, long))

	absurd := &StatusError{retryAfter: 24 * time.Hour}
	assert.Equal(t, maxRetryAfter, c.delay(1, absurd))
}

func TestURL(t *testing.T) {
	const host = "https://api.test"

	tests := []struct {
		name     string
		base     string
		query    url.Values
		segments []string
		want     string
	}{
		{"plain path", host, nil, []string{works}, host + "/works"},
		{"base path kept", host + "/v1", nil, []string{"search"}, host + "/v1/search"},
		{"trailing slash base", host + "/v1/", nil, []string{"chat", "completions"}, host + "/v1/chat/completions"},
		{"segment with slashes", host, nil, []string{works, "doi:10.1/x"}, host + "/works/doi:10.1/x"},
		{
			"query is encoded", host,
			url.Values{"mailto": {"a+b&c@x.org"}},
			[]string{works},
			host + "/works?mailto=a%2Bb%26c%40x.org",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := URL(tt.base, tt.query, tt.segments...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Characters that are legal in an old-style DOI but special in a URL must
// survive the round trip through URL.
func TestURLKeepsDOICharacters(t *testing.T) {
	const doi = "10.1002/(sici)1097-0258(19980430)17:8<849::aid-sim777>3.0.co;2-f?#%"

	got, err := URL("https://api.test", nil, works, doi)
	require.NoError(t, err)

	parsed, err := url.Parse(got)
	require.NoError(t, err)
	assert.Equal(t, "/works/"+doi, parsed.Path)
	assert.Empty(t, parsed.RawQuery, "a '?' inside a DOI must not start a query")
	assert.Empty(t, parsed.Fragment)
}
