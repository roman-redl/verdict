package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLanguage(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Language
	}{
		{"", ""},
		{"  ", ""},
		{"en", LangEN},
		{"EN", LangEN},
		{" ru ", LangRU},
		{"Ru", LangRU},
	} {
		got, err := ParseLanguage(tc.in)
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
	}

	for _, in := range []string{"de", "russian", "en-US", "r u"} {
		_, err := ParseLanguage(in)
		require.Error(t, err, in)
		assert.Contains(t, err.Error(), "en | ru") //nolint:forbidigo // checks the message text
	}
}

// The environment parser goes through UnmarshalText: an unknown value is a
// startup error, an empty one leaves the language unset.
func TestLanguageUnmarshalText(t *testing.T) {
	var l Language

	require.NoError(t, l.UnmarshalText([]byte("RU")))
	assert.Equal(t, LangRU, l)

	require.NoError(t, l.UnmarshalText([]byte("")))
	assert.Equal(t, Language(""), l)

	require.Error(t, l.UnmarshalText([]byte("fr")))
}
