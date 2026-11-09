package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStudyDesign(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    StudyDesign
		wantErr bool
	}{
		{name: "exact", in: "cluster_rct", want: DesignClusterRCT},
		{name: "case and spaces", in: "  Meta_Analysis ", want: DesignMetaAnalysis},
		{name: "hyphen", in: "meta-analysis", want: DesignMetaAnalysis},
		{name: "words", in: "systematic review", want: DesignSystematicReview},
		{name: "the unclear design itself", in: string(DesignUnclear), want: DesignUnclear},
		{name: "empty", in: "", wantErr: true},
		{name: "unknown", in: "randomised", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseStudyDesign(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				assert.Empty(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Every listed design parses back to itself, so the list and the parser
// cannot drift apart.
func TestStudyDesignsRoundTrip(t *testing.T) {
	for _, d := range StudyDesigns() {
		got, err := ParseStudyDesign(string(d))
		require.NoError(t, err, string(d))
		assert.Equal(t, d, got)
	}
}

func TestStudyDesignIsSecondary(t *testing.T) {
	secondary := map[StudyDesign]bool{
		DesignSystematicReview: true, DesignMetaAnalysis: true, DesignNarrativeReview: true, DesignGuideline: true,
	}

	for _, d := range StudyDesigns() {
		assert.Equal(t, secondary[d], d.IsSecondary(), string(d))
	}

	assert.False(t, StudyDesign("").IsSecondary(), "an empty design is not secondary")
}

func TestStudyDesignIsKnown(t *testing.T) {
	assert.True(t, DesignRCT.IsKnown())
	assert.True(t, DesignOther.IsKnown())
	assert.False(t, DesignUnclear.IsKnown())
	assert.False(t, StudyDesign("").IsKnown())
}
