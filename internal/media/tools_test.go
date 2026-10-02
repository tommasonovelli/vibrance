package media

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// The toolchain images carry the pinned ffmpeg and ffprobe at the fixed
// paths, and their versions are read, not assumed. A Dockerfile that copies
// other binaries, or none, fails the gate here, before it can fail a start.
func TestPinnedToolsInstalled(t *testing.T) {
	if PinnedVersion != "8.1.3-musiclib1" {
		t.Fatalf("PinnedVersion is %q: DESIGN.md D18 pins 8.1.3-musiclib1, the tools of MusicLib 1.2.0", PinnedVersion)
	}
	if FFmpegPath != "/usr/local/bin/ffmpeg" || FFprobePath != "/usr/local/bin/ffprobe" {
		t.Fatalf("the tools are at %s and %s, the image has them in /usr/local/bin", FFmpegPath, FFprobePath)
	}
	if got := newTools(t).Version(); got != PinnedVersion {
		t.Fatalf("Version() = %q, want %q", got, PinnedVersion)
	}
}

// DESIGN.md §6.3 step 4: 30 seconds for a probe, 10 minutes for a
// fingerprint.
func TestTimeouts(t *testing.T) {
	if ProbeTimeout != 30*time.Second || FingerprintTimeout != 10*time.Minute {
		t.Fatalf("timeouts %s and %s, want 30s and 10m", ProbeTimeout, FingerprintTimeout)
	}
}

// A tool that is missing, of another version, not the tool its path says,
// or that fails, is refused, each with its code (§11.2 step 4).
func TestNewToolsRefusesWrongTools(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	older := script(t, dir, "older", `echo "ffmpeg version 7.1.2 Copyright (c) 2000-2025 the FFmpeg developers"`)
	otherBuild := script(t, dir, "other-build", `echo "ffprobe version 8.1.3 Copyright (c) 2007-2026 the FFmpeg developers"`)
	newerBuild := script(t, dir, "newer-build", `echo "ffprobe version 8.1.3-musiclib2 Copyright (c) 2007-2026 the FFmpeg developers"`)
	garbled := script(t, dir, "garbled", `echo "hello"`)
	silent := script(t, dir, "silent", `exit 0`)
	failing := script(t, dir, "failing", `echo "ffmpeg version `+PinnedVersion+` Copyright"; exit 1`)
	secondLine := script(t, dir, "second-line", `echo; echo "ffmpeg version `+PinnedVersion+` Copyright"`)
	notExecutable := writeFile(t, filepath.Join(dir, "not-executable"), []byte("#!/bin/sh\n"), 0o644)

	for _, tc := range []struct {
		name            string
		ffmpeg, ffprobe string
		code            string
	}{
		{"ffmpeg is missing", missing, FFprobePath, CodeToolUnavailable},
		{"ffprobe is missing", FFmpegPath, missing, CodeToolUnavailable},
		{"ffmpeg is not executable", notExecutable, FFprobePath, CodeToolUnavailable},
		{"ffmpeg of another release", older, FFprobePath, CodeToolVersion},
		{"ffprobe without MusicLib's build suffix", FFmpegPath, otherBuild, CodeToolVersion},
		{"ffprobe of another build", FFmpegPath, newerBuild, CodeToolVersion},
		{"not a version line", garbled, FFprobePath, CodeToolUnavailable},
		{"no output", FFmpegPath, silent, CodeToolUnavailable},
		{"the version line is not the first", secondLine, FFprobePath, CodeToolUnavailable},
		{"the version is printed, exit 1", failing, FFprobePath, CodeToolUnavailable},
		{"ffprobe at the path of ffmpeg", FFprobePath, FFprobePath, CodeToolUnavailable},
		{"ffmpeg at the path of ffprobe", FFmpegPath, FFmpegPath, CodeToolUnavailable},
		{"a relative path", "ffmpeg", FFprobePath, CodeToolUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools, err := NewTools(t.Context(), NewRunner(1), tc.ffmpeg, tc.ffprobe)
			wantCode(t, err, tc.code)
			if tools != nil {
				t.Fatal("NewTools returned tools it refused")
			}
		})
	}
}

// A stop that arrives while the tools are being checked is a
// cancellation, not a missing tool.
func TestNewToolsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := NewTools(ctx, NewRunner(1), FFmpegPath, FFprobePath)
	wantCode(t, err, CodeCanceled)
}

func TestVersionLine(t *testing.T) {
	for line, want := range map[string]string{
		"ffmpeg version 8.1.3-musiclib1 Copyright (c) 2000-2026 the FFmpeg developers":  "8.1.3-musiclib1",
		"ffprobe version 8.1.3-musiclib1 Copyright (c) 2007-2026 the FFmpeg developers": "8.1.3-musiclib1",
		"ffprobe version n8.1 Copyright":                                                "n8.1",
		"ffmpeg version 8.1.3":                                                          "8.1.3",
		" ffmpeg version 8.1.3":                                                         "",
		"ffplay version 8.1.3 Copyright":                                                "",
		"ffmpeg  version 8.1.3":                                                         "",
		"ffmpeg version ":                                                               "",
		"libavutil      60. 26.103 / 60. 26.103":                                        "",
		"":                                                                              "",
	} {
		got := ""
		if m := versionLine.FindStringSubmatch(line); m != nil {
			got = m[2]
		}
		if got != want {
			t.Errorf("%q: version %q, want %q", line, got, want)
		}
	}
}
