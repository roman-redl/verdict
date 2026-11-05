package app

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionPrefersTheStampedValue(t *testing.T) {
	require.Equal(t, "v1.2.3", Version("v1.2.3"))
}

func TestRevision(t *testing.T) {
	const full = "0123456789abcdef0123"

	rev := debug.BuildSetting{Key: settingRevision, Value: full}

	tests := []struct {
		name     string
		settings []debug.BuildSetting
		want     string
	}{
		{"no vcs data", nil, DevVersion},
		{"clean tree", []debug.BuildSetting{rev, {Key: settingModified, Value: "false"}}, "0123456789ab"},
		{"modified tree", []debug.BuildSetting{rev, {Key: settingModified, Value: "true"}}, "0123456789ab-dirty"},
		{"short revision", []debug.BuildSetting{{Key: settingRevision, Value: "abc"}}, "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, revision(tt.settings))
		})
	}
}
