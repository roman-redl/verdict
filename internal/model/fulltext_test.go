package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const testPMCID = "PMC7745181"

func TestNormalizePMCID(t *testing.T) {
	tests := []struct{ in, want string }{
		{testPMCID, testPMCID},
		{"pmc7745181", testPMCID},
		{" PMC7745181 ", testPMCID},
		{"7745181", testPMCID},
		{"", ""},
		{"PMC", ""},
		{"PMC12x", ""},
		{"https://example.org/PMC1", ""},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizePMCID(tt.in))
		})
	}
}
