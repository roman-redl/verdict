package jina

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pageURL = "https://pmc.ncbi.nlm.nih.gov/articles/PMC7745181/"
	// wantChallenge is what the rejection of a challenge, error or paywall
	// page says.
	wantChallenge = "a challenge, error or paywall page"
	wantNoDiscuss = "no Discussion section"
	paperTitle    = "Paper"
	// fastRate keeps the tests from waiting on the rate limiter.
	fastRate = 60000
)

// article is a plausible full text: the sections of an article, a table, and
// prose long enough to pass the check.
func article() string {
	return "## Abstract\n\n" + strings.Repeat("Background and methods. ", 20) +
		"\n\n## Introduction\n\n" + strings.Repeat("Why the vaccine was studied. ", 100) +
		"\n\n## Methods\n\n" + strings.Repeat("Participants were randomized. ", 100) +
		"\n\n## Results\n\n" + strings.Repeat("Vaccine efficacy was 95% (95% CI 90.3 to 97.6). ", 120) +
		"\n\n| Outcome | Vaccine | Placebo |\n|---|---|---|\n| Cases | 8 | 162 |\n" +
		"\n\n## Discussion\n\n" + strings.Repeat("The finding agrees with earlier work. ", 100)
}

// reply is the reader's JSON envelope.
func reply(t *testing.T, w http.ResponseWriter, title, content string) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"code": 200, "status": 20000,
		"data": map[string]string{"title": title, "url": pageURL, "content": content},
	}))
}

func TestPageTextAccepted(t *testing.T) {
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))

		reply(t, w, "Safety and Efficacy", "![Image 1](https://cdn.example/x.svg)\n\n"+
			"[Skip to main content](https://pmc.ncbi.nlm.nih.gov/articles/PMC7745181/#main)\n\n\n\n\n"+article())
	}))
	defer srv.Close()

	got, err := NewWithBase(srv.URL, "", fastRate).PageText(t.Context(), pageURL)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"url": pageURL}, gotBody, "the URL goes in the body, whole")
	assert.True(t, strings.HasPrefix(got, "Skip to main content"), "the link is reduced to its text: %q", got[:40])
	assert.NotContains(t, got, "cdn.example", "images are dropped")
	assert.NotContains(t, got, "#main", "link addresses are dropped")
	assert.NotContains(t, got, "\n\n\n", "blank runs are collapsed")
	assert.Contains(t, got, "| Cases | 8 | 162 |", "tables are kept")
}

func TestPageTextRejected(t *testing.T) {
	long := strings.Repeat("Navigation and cookie text. ", 300)

	tests := []struct {
		name    string
		title   string
		content string
		want    string
	}{
		{
			name: "a Cloudflare challenge", title: "Just a moment...",
			content: "Verifying you are human. This may take a few seconds.\n\n" + article(),
			want:    wantChallenge,
		},
		{
			name: "a security verification page", title: "Example",
			content: "Performing security verification\n\n" + article(), want: wantChallenge,
		},
		{
			name: "a long captcha page", title: "Are you a robot?",
			content: "Please confirm you are a human by completing the captcha challenge below.\n\n" + long + article(),
			want:    wantChallenge,
		},
		{
			name: "an Anubis proof-of-work page", title: "Making sure you're not a bot!",
			content: "Protected by Anubis from TecharoHQ.\n\n" + article(), want: wantChallenge,
		},
		{
			name: "a page that needs JavaScript", title: "x",
			content: "Enable JavaScript and cookies to continue\n" + article(), want: wantChallenge,
		},
		{name: "the target's error title", title: "HTTP 404 [Not Found]", content: article(), want: wantChallenge},
		{
			name: "an abstract only", title: paperTitle,
			content: "## Abstract\n\nBackground. Methods. Results: HR 0.8 (95% CI 0.6 to 1.0, p = 0.03).", want: "too short",
		},
		{
			name: "a long page with navigation, no Discussion", title: paperTitle,
			content: "## Abstract\n\nHR 0.8 (95% CI 0.6 to 1.0).\n\n## Introduction\n\n" + long + long,
			want:    wantNoDiscuss,
		},
		{
			name: "a long page with a Discussion but no body section", title: paperTitle,
			content: "## Abstract\n\nHR 0.8.\n\n" + long + long + "\n\n## Discussion\n\nShort.", want: "no Introduction",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reply(t, w, tt.title, tt.content)
			}))
			defer srv.Close()

			got, err := NewWithBase(srv.URL, "", fastRate).PageText(t.Context(), pageURL)

			require.Error(t, err)
			assert.Empty(t, got)
			require.ErrorContains(t, err, tt.want)

			var rejected *RejectedError

			require.ErrorAs(t, err, &rejected)
		})
	}
}

func fixture(t *testing.T, name string) string {
	t.Helper()

	//nolint:gosec // a fixed file name inside testdata
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)

	return string(b)
}

// The pages the first version of the check wrongly accepted (real excerpts of
// a Springer record page with a structured abstract and a paywall, an Ovid
// landing page, a repository record page) are rejected, and the excerpt of a
// real article is accepted once it has the length of one.
func TestCheckRealPages(t *testing.T) {
	filler := strings.Repeat("Reference list and related content. ", 400) // brings a short excerpt to article length

	tests := []struct {
		name string
		text string
		want string // empty: accepted
	}{
		{name: "Springer abstract page", text: fixture(t, "springer_abstract_page.md"), want: wantChallenge},
		{
			name: "Springer abstract page, padded to full length",
			text: fixture(t, "springer_abstract_page.md") + filler, want: wantChallenge,
		},
		{name: "Ovid landing page", text: fixture(t, "ovid_landing_page.md") + filler, want: wantChallenge},
		{name: "repository record page", text: fixture(t, "repository_record_page.md") + filler, want: wantChallenge},
		{name: "real article", text: fixture(t, "real_article_excerpt.md") + filler},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Check("", tt.text)

			if tt.want == "" {
				require.NoError(t, err)
				return
			}

			require.ErrorContains(t, err, tt.want)
		})
	}
}

// With the paywall words gone, a structured abstract padded to the length of
// an article is still rejected: it ends with a Conclusion and has no
// Discussion. The same text with a Discussion section is accepted.
func TestCheckNeedsADiscussion(t *testing.T) {
	abstract := fixture(t, "springer_abstract_page.md")
	abstract, _, found := strings.Cut(abstract, "## Access this article")
	require.True(t, found, "the fixture has the paywall block")

	body := abstract + strings.Repeat("Navigation and reference text. ", 400)

	require.ErrorContains(t, Check("", body), wantNoDiscuss)
	require.NoError(t, Check("", body+"\n\n## Discussion\n\nThe finding agrees with earlier work."))
}

// A heading the way a PDF's text or a plain page has it still counts: a line
// of its own, numbered or in capitals, with a colon.
func TestCheckHeadingForms(t *testing.T) {
	prose := strings.Repeat("The cohort was followed for a year. ", 300)

	tests := []struct {
		name string
		text string
		ok   bool
	}{
		{"numbered lines of a PDF", "1. Introduction\n\n" + prose + "\n\n4. Discussion \n\nText.", true},
		{"upper case lines", "INTRODUCTION\n\n" + prose + "\n\nDISCUSSION\n\nText.", true},
		{"a colon and bold", "**Methods:**\n\n" + prose + "\n\n**Discussion:**\n\nText.", true},
		{"a combined heading", "## Background\n\n" + prose + "\n\n## Results and Discussion\n\nText.", true},
		{"the word in a sentence is not a heading", "## Methods\n\n" + prose + "\n\nSee the discussion below.", false},
		{"Discussion inside the abstract's line", "## Methods\n\n" + prose + "\n\nDiscussion of results: none.", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.ok {
				require.NoError(t, Check("", tt.text))
				return
			}

			require.Error(t, Check("", tt.text))
		})
	}
}

// Words that name a paywall are rejected in the head only; the "Subscribe to
// RSS" and "Subscribe to Alert Service" links of a real article's page are
// not paywall words.
func TestCheckPaywallMarkers(t *testing.T) {
	for _, marker := range []string{
		"Access this article", "Buy article", "Buy this article", "Purchase this article", "Log in to view",
		"Subscribe to access", "Rent this article", "Your privacy, your choice", "Skip to main navigation",
		"Institutional access", "Ovid® Ovid Logo",
	} {
		t.Run(marker, func(t *testing.T) {
			require.ErrorContains(t, Check("", marker+"\n\n"+article()), wantChallenge)
		})
	}

	require.NoError(t, Check("", "Subscribe to RSS\n\nSubscribe to Alert Service\n\n"+article()))

	// the same words far below the head belong to the article
	require.NoError(t, Check("", article()+strings.Repeat("x ", scanChars)+"\n\nBuy this article"))
}

// The key is sent as a bearer token only when it is set; the anonymous
// request carries no Authorization header at all.
func TestPageTextSendsTheKeyOnlyWhenSet(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{"no key", "", ""},
		{"a key", "jina_secret", "Bearer jina_secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Values("Authorization")

				reply(t, w, paperTitle, article())
			}))
			defer srv.Close()

			_, err := NewWithBase(srv.URL, tt.key, fastRate).PageText(t.Context(), pageURL)

			require.NoError(t, err)

			if tt.want == "" {
				assert.Empty(t, got)
				return
			}

			assert.Equal(t, []string{tt.want}, got)
		})
	}
}

// A service error is an error, and its text never carries the key.
func TestPageTextServiceErrors(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "target unreachable", http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	_, err := NewWithBase(srv.URL, "jina_secret", fastRate).PageText(t.Context(), pageURL)

	require.ErrorContains(t, err, "HTTP 422")
	assert.NotContains(t, fmt.Sprint(err), "jina_secret")
	assert.Equal(t, int32(1), calls.Load(), "a client error is not repeated")
}
