package jina

import (
	"fmt"
	"regexp"
	"strings"
)

// The content check. The reader answers HTTP 200 for a Cloudflare challenge
// or a captcha (one Elsevier captcha page is 113k characters long), and a
// publisher's record page carries the abstract, a paywall and a page of
// navigation, so neither the status nor the length tells a page from an
// article: the text is judged by what it says.
const (
	// minChars is the shortest text taken for a full article: an abstract
	// page, a cookie wall or a paywall page is shorter, or padded with
	// navigation that the headings below expose.
	minChars = 10000
	// scanChars is how much of the head is searched for the markers: a
	// challenge or paywall page says so at the top, and an article may
	// mention a captcha or a subscription far below.
	scanChars = 4000
)

// rejectMarkers are the phrases of challenge, captcha, error, paywall and
// landing pages (lower case; matched against the head of the page and its
// title).
var rejectMarkers = []string{
	// challenge and error pages
	"just a moment",
	"performing security verification",
	"verify you are human",
	"verifying you are human",
	"enable javascript and cookies",
	"checking your browser",
	"checking if the site connection is secure",
	"are you a robot",
	"confirm you are a human",
	"captcha",
	"anubis",
	"attention required",
	"access denied",
	"unusual traffic",
	"target url returned error",
	"http 403",
	"http 404",
	"http 429",
	"http 500",
	"http 503",
	// paywall, cookie wall and record pages
	"access this article",
	"buy article",
	"buy this article",
	"purchase this article",
	"log in to view",
	// "subscribe to" alone also matches the "Subscribe to RSS" and "Subscribe
	// to Alert Service" links of a real article's page (Thieme), so only the
	// paywall phrasings are listed
	"subscribe to access",
	"subscribe to read",
	"subscribe to view",
	"subscribe to continue",
	"subscribe to unlock",
	"rent this article",
	"your privacy, your choice",
	"skip to main navigation",
	"institutional access",
	"ovid® ovid logo",
}

var (
	// discussionHeading is a section that only a full article has: a markdown
	// heading with the word Discussion in it ("## Results and Discussion"), or
	// a line that is just that word, with or without a section number ("4.
	// Discussion", as a PDF's text has it). A structured abstract ends with
	// its Conclusion and never has one.
	discussionHeading = regexp.MustCompile(
		`(?im)^(?:#{1,6}[ \t]+[^\n]*\bdiscussion\b[^\n]*|` + bareHeading(`discussion`) + `)$`)
	// bodyHeading is a section of the article's body: introduction,
	// background, methods or materials and methods.
	bodyHeading = regexp.MustCompile(
		`(?im)^(?:#{1,6}[ \t]+[^\n]*\b(?:introduction|background|methods?|materials)\b[^\n]*|` +
			bareHeading(`introduction|background|methods?|materials and methods`) + `)$`)
)

// bareHeading is the pattern of a heading that is a line of its own with no
// markdown: an optional bold mark and section number ("2.1"), the name, an
// optional colon.
func bareHeading(names string) string {
	return `[ \t]*\**(?:\d+(?:\.\d+)*[.)]?[ \t]+)?(?:` + names + `):?\**[ \t]*`
}

// RejectedError says the reader returned something that is not a full
// article.
type RejectedError struct {
	Reason string
}

func (e *RejectedError) Error() string { return "the page is not a full text: " + e.Reason }

// Check judges what the reader returned. A page is rejected when its head
// carries a challenge, error or paywall marker, when it is shorter than
// minChars, or when it lacks a Discussion section or any of the Introduction,
// Background and Methods sections (an abstract page padded with navigation).
func Check(title, text string) error {
	head := strings.ToLower(title + "\n" + text[:min(len(text), scanChars)])

	for _, m := range rejectMarkers {
		if strings.Contains(head, m) {
			return &RejectedError{Reason: fmt.Sprintf("a challenge, error or paywall page (%q)", m)}
		}
	}

	if n := len([]rune(text)); n < minChars {
		return &RejectedError{Reason: fmt.Sprintf("too short (%d characters, an abstract page?)", n)}
	}

	if !discussionHeading.MatchString(text) {
		return &RejectedError{Reason: "no Discussion section (an abstract page?)"}
	}

	if !bodyHeading.MatchString(text) {
		return &RejectedError{Reason: "no Introduction, Background or Methods section"}
	}

	return nil
}
