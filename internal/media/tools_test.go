package media

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// pinnedToolVersion is the version both tools must report: FFmpeg 8.1.3 plus
// the --extra-version of MusicLib's build, whose binaries the Dockerfile
// copies from the pinned MusicLib image (DESIGN.md D18, §3.6).
const pinnedToolVersion = "8.1.3-musiclib1"

// The first line of `ffmpeg -version` / `ffprobe -version`.
var toolVersionLine = regexp.MustCompile(`^(ffmpeg|ffprobe) version (\S+) `)

// The toolchain images carry the pinned ffmpeg and ffprobe at the fixed
// paths, and their versions are read, not assumed. A Dockerfile that copies
// other binaries, or none, fails the gate here.
func TestPinnedToolsInstalled(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		t.Run(tool, func(t *testing.T) {
			path := "/usr/local/bin/" + tool
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, path, "-version").Output()
			if err != nil {
				t.Fatalf("%s -version: %v", path, err)
			}
			first, _, _ := strings.Cut(string(out), "\n")
			m := toolVersionLine.FindStringSubmatch(first)
			if m == nil {
				t.Fatalf("%s -version: first line %q is not a version line", path, first)
			}
			if m[1] != tool {
				t.Fatalf("%s is %s, not %s", path, m[1], tool)
			}
			if m[2] != pinnedToolVersion {
				t.Fatalf("%s reports version %q, want %q", path, m[2], pinnedToolVersion)
			}
		})
	}
}
