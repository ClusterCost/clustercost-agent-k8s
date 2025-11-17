package version

import (
	"runtime/debug"
)

// Version is the agent version injected at build time via -ldflags.
// Default to empty string so we can detect unset state.
var Version = ""

// Value returns the most useful version string available.
// Preference order:
//  1. Build-time injected Version variable (e.g., release tag)
//  2. Module version exposed by runtime/debug ReadBuildInfo (if not "(devel)")
//  3. VCS tag or revision reported by the Go toolchain
//  4. Literal "dev" as a final fallback
func Value() string {
	if Version != "" {
		return Version
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
		var vcsTag, vcsRev string
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.tag":
				if setting.Value != "" {
					vcsTag = setting.Value
				}
			case "vcs.revision":
				if setting.Value != "" {
					vcsRev = setting.Value
				}
			}
		}
		if vcsTag != "" {
			return vcsTag
		}
		if vcsRev != "" {
			return vcsRev
		}
	}

	return "dev"
}
