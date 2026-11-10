package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	testPMCID   = "PMC7745181"
	testPMCPage = "https://pmc.ncbi.nlm.nih.gov/articles/PMC7745181/"
)

func TestParseHostType(t *testing.T) {
	tests := []struct {
		in   string
		want HostType
	}{
		{"repository", HostRepository},
		{" Repository ", HostRepository},
		{"journal", HostPublisher},
		{"conference", HostPublisher},
		{"ebook platform", HostPublisher},
		{"book series", HostPublisher},
		{"metadata", HostUnknown},
		{"other", HostUnknown},
		{"", HostUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseHostType(tt.in))
		})
	}
}

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

func TestPMCIDFromURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://www.ncbi.nlm.nih.gov/pmc/articles/7745181", testPMCID},
		{"https://www.ncbi.nlm.nih.gov/pmc/articles/PMC7745181/", testPMCID},
		{"http://www.ncbi.nlm.nih.gov/pmc/articles/PMC7745181/pdf/x.pdf", testPMCID},
		{testPMCPage, testPMCID},
		{"https://pubmed.ncbi.nlm.nih.gov/33301246", ""},
		{"https://europepmc.org/articles/PMC7745181", ""},
		{"https://example.org/pmc/articles/7745181", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, PMCIDFromURL(tt.in))
		})
	}
}

func TestPMCPageURL(t *testing.T) {
	assert.Equal(t, testPMCPage, PMCPageURL("pmc7745181"))
	assert.Equal(t, testPMCPage, PMCPageURL("7745181"))
	assert.Empty(t, PMCPageURL("nonsense"))
}
