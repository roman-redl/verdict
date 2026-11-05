package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBackend(t *testing.T) {
	tests := []struct {
		in      string
		want    Backend
		wantErr bool
	}{
		{"", BackendAuto, false},
		{"  ", BackendAuto, false},
		{"anthropic", BackendAnthropic, false},
		{"Claude-CLI", BackendClaudeCLI, false},
		{"openai-compat", BackendOpenAICompat, false},
		{"gemini", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseBackend(tt.in)
			if tt.wantErr {
				require.ErrorContains(t, err, "unknown LLM backend")
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// The environment parser goes through UnmarshalText, so a typo in
// LLM_BACKEND stops the program at startup.
func TestBackendUnmarshalText(t *testing.T) {
	var b Backend

	require.NoError(t, b.UnmarshalText([]byte("anthropic")))
	assert.Equal(t, BackendAnthropic, b)
	require.Error(t, b.UnmarshalText([]byte("nope")))
}
