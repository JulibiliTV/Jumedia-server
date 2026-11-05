package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "1234567890abcdef"}, {Key: "vcs.modified", Value: "true"}}}
	for _, tc := range []struct {
		name, version, revision string
		info                    *debug.BuildInfo
		want                    string
	}{
		{"workspace", "0.1.0", "", info, "0.1.0+1234567.dirty"},
		{"release override", "v1.2.3", "abcdef123456", info, "1.2.3+abcdef1"},
		{"archive without metadata", "0.1.0", "", nil, "0.1.0+dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := format(tc.version, tc.revision, tc.info); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
