package pipeline

import (
	"fmt"
	"strings"
)

// TextMode selects what the critic reads.
type TextMode string

const (
	// TextLean reads abstracts only.
	TextLean TextMode = "lean"
	// TextHybrid reads abstracts first, then open-access full texts for the
	// pivotal studies (one deepening pass at the end of the critique stage,
	// before the single synthesis).
	TextHybrid TextMode = "hybrid"
	// TextFull reads open-access full texts for every paper at enrichment.
	TextFull TextMode = "full"
)

// ParseTextMode parses a mode typed by a user (a flag, a request field, an
// environment variable). The empty string is valid and means "not set": the
// run falls back to the configured default.
func ParseTextMode(s string) (TextMode, error) {
	m := TextMode(strings.ToLower(strings.TrimSpace(s)))

	switch m {
	case "", TextLean, TextHybrid, TextFull:
		return m, nil
	}

	return "", &UserError{Msg: fmt.Sprintf("unknown text mode %q (want lean | hybrid | full)", s)}
}

// UnmarshalText lets the environment parser reject an unknown mode at startup.
func (m *TextMode) UnmarshalText(text []byte) error {
	parsed, err := ParseTextMode(string(text))
	if err != nil {
		return err
	}

	*m = parsed

	return nil
}

// Engine is a discovery engine.
type Engine string

const (
	// EngineConsensus is semantic search through the Consensus API (mode B).
	EngineConsensus Engine = "consensus"
	// EngineOpenAlex is keyword search through OpenAlex (mode A).
	EngineOpenAlex Engine = "openalex"
	// EngineDOIs marks a run whose corpus is a user-supplied DOI list
	// (mode C); it is recorded in the artifacts but cannot be selected.
	EngineDOIs Engine = "dois"
)

// ParseEngine parses the --engine flag. The empty string means "auto":
// Consensus when its key is configured, else OpenAlex.
func ParseEngine(s string) (Engine, error) {
	e := Engine(strings.ToLower(strings.TrimSpace(s)))

	switch e {
	case "", EngineConsensus, EngineOpenAlex:
		return e, nil
	case EngineDOIs:
		// recorded for DOI-list runs only; a list is selected with --dois
	}

	return "", &UserError{Msg: fmt.Sprintf("unknown engine %q (want consensus | openalex)", s)}
}
