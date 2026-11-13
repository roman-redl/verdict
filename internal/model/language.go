package model

import (
	"fmt"
	"strings"
)

// Language is the language of a report: the free text the synthesis writes
// and the labels Go renders around it. Enum values and JSON keys stay English
// in every language, so that machine readers (the eval scoring included) never
// depend on it.
type Language string

const (
	// LangEN is English, the default.
	LangEN Language = "en"
	// LangRU is Russian.
	LangRU Language = "ru"
)

// ParseLanguage parses a language typed by a user (a flag, a request field, an
// environment variable), ignoring case and spaces. The empty string is valid
// and means "not set": the caller falls back to its default.
func ParseLanguage(s string) (Language, error) {
	l := Language(strings.ToLower(strings.TrimSpace(s)))

	switch l {
	case "", LangEN, LangRU:
		return l, nil
	}

	return "", fmt.Errorf("unknown report language %q (want en | ru)", s)
}

// UnmarshalText lets the environment parser reject an unknown language at
// startup.
func (l *Language) UnmarshalText(text []byte) error {
	parsed, err := ParseLanguage(string(text))
	if err != nil {
		return err
	}

	*l = parsed

	return nil
}
