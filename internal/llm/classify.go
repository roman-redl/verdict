package llm

import (
	"regexp"
	"strings"
)

// failureKind says what to do about a failed CLI call.
type failureKind int

const (
	// failureOther is not worth a retry: a bad answer, a crash, our own timeout.
	failureOther failureKind = iota
	// failureTransient is a rate limit, an overload or a dropped connection:
	// the same call may succeed a little later.
	failureTransient
	// failureQuota is an exhausted subscription window; no call will pass
	// until the limit resets.
	failureQuota
	// failureAuth is a lost or invalid login; no call will pass until the
	// user logs in again.
	failureAuth
)

// The failure-text patterns, matched as lowercase substrings. The CLI prints
// failures as an is_error envelope whose result is human text, so text is all
// there is to match.
//
// The exact CLI wording for an exhausted Claude Max quota was not verified
// live (it needs a spent window); the quota patterns are the phrases the CLI
// and the API are known to use, and a missed phrase degrades safely: the
// failure is "other" — no retry, per-study fallback — instead of a stop.
//
// quota and auth are checked before transient: a quota message may also
// mention a rate limit, and retrying it only burns the next window.
//
// HTTP status codes are matched as whole numbers (statusAuth,
// statusTransient): as substrings they would hit ports, request ids and
// token counts, and a false auth match stops the whole run.
var (
	quotaPatterns = []string{
		"usage limit", "limit reached", "hit your limit", "you've hit your",
		"weekly limit", "session limit", "credit balance is too low",
	}
	authPatterns = []string{
		"invalid api key", "authentication_error", "authentication failed", "not logged in",
		"/login", "invalid bearer", "oauth token", "403 forbidden",
	}
	transientPatterns = []string{
		"rate_limit", "rate limit", "overloaded", "500 internal",
		"econnreset", "socket hang up", "connection error", "fetch failed",
	}
	statusAuth      = regexp.MustCompile(`\b401\b`)
	statusTransient = regexp.MustCompile(`\b(429|529|502|503|504)\b`)
)

// classify sorts a failure text into what the caller should do about it.
func classify(text string) failureKind {
	text = strings.ToLower(text)

	switch {
	case containsAny(text, quotaPatterns):
		return failureQuota
	case containsAny(text, authPatterns) || statusAuth.MatchString(text):
		return failureAuth
	case containsAny(text, transientPatterns) || statusTransient.MatchString(text):
		return failureTransient
	}

	return failureOther
}

func containsAny(text string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(text, p) {
			return true
		}
	}

	return false
}
