package app

import "runtime/debug"

// DevVersion is the placeholder of a build without a stamped version.
const DevVersion = "dev"

// Version names the running build for the startup log line. A version
// stamped at link time (-ldflags "-X main.version=...") wins; otherwise the
// VCS revision the Go toolchain embeds in the binary, marked "-dirty" for a
// build from a modified tree; "dev" when neither is available.
func Version(stamped string) string {
	if stamped != "" && stamped != DevVersion {
		return stamped
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return DevVersion
	}

	return revision(info.Settings)
}

// The build settings the Go toolchain records for a build inside a VCS tree.
const (
	settingRevision = "vcs.revision"
	settingModified = "vcs.modified"
)

func revision(settings []debug.BuildSetting) string {
	var rev, dirty string

	for _, s := range settings {
		switch s.Key {
		case settingRevision:
			rev = s.Value
		case settingModified:
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}

	if rev == "" {
		return DevVersion
	}

	const shortLen = 12

	return rev[:min(shortLen, len(rev))] + dirty
}
