package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Version and Revision may be set with -ldflags -X for source archives.
var Version = "0.1.0"
var Revision string

func Current() string {
	info, _ := debug.ReadBuildInfo()
	return format(Version, Revision, info)
}

func format(version, revision string, info *debug.BuildInfo) string {
	modified := false
	if revision == "" && info != nil {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	if revision == "" {
		revision = "dev"
	}
	if modified {
		revision += ".dirty"
	}
	return strings.TrimPrefix(version, "v") + "+" + revision
}
