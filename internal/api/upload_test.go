package api

import (
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadExtensionCompatibility(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		for _, tc := range []struct{ name, ext string }{{"upper.MP4", ".MP4"}, {"plain", ""}, {"bad.bad-ext", ""}, {"long.1234567890123456", ""}, {"safe.a1", ".a1"}} {
			data := creatorData(t, creatorUpload(t, s, session, "/x/upload/web/video", tc.name, []byte("payload")))
			name := filepath.Base(data["filename"].(string))
			if filepath.Ext(name) != tc.ext {
				t.Fatalf("%q: extension changed: %q", tc.name, name)
			}
			random, err := hex.DecodeString(strings.TrimSuffix(strings.TrimPrefix(name, "media-"), tc.ext))
			if err != nil || len(random) != 16 {
				t.Fatalf("bad random name: %s", name)
			}
		}
	})
}
