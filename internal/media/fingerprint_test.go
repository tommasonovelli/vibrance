package media

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// The command line of the fingerprint is the one of DESIGN.md §5.4, with
// the file on descriptor 3 (T2) and the first audio stream chosen by its
// type (T1).
func TestFingerprintArgs(t *testing.T) {
	want := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-protocol_whitelist", "fd", "-fd", "3", "-f", "flac", "-i", "fd:",
		"-map", "0:a:0", "-c", "copy", "-f", "hash", "-hash", "sha256", "-",
	}
	if got := fingerprintArgs("flac"); !slices.Equal(got, want) {
		t.Fatalf("fingerprintArgs:\n got %q\nwant %q", got, want)
	}
}

var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Every track of the fixture library has the fingerprint that the spike
// recorded for it, at every run; tracks with different audio have different
// fingerprints, and the two tracks of album F, which have the same audio
// and different tags, have the same (§5.4).
func TestFingerprintFixtureLibrary(t *testing.T) {
	tools := newTools(t)
	byFingerprint := map[string][]string{}
	for _, tr := range fixtureTracks {
		f := open(t, inFixture(tr.path))
		for range 2 {
			fp, version, err := tools.Fingerprint(t.Context(), f, containerOf(t, tr.path))
			if err != nil {
				t.Fatalf("%s: %v", tr.path, err)
			}
			if !hexSHA256.MatchString(fp) || fp != tr.fingerprint {
				t.Fatalf("%s: fingerprint %q, want %q", tr.path, fp, tr.fingerprint)
			}
			if version != PinnedVersion {
				t.Fatalf("%s: version %q, want %q", tr.path, version, PinnedVersion)
			}
		}
		byFingerprint[tr.fingerprint] = append(byFingerprint[tr.fingerprint], tr.path)
	}
	if len(byFingerprint) != len(fixtureTracks)-1 {
		t.Fatalf("%d fingerprints for %d tracks, want one less: only album F repeats its audio", len(byFingerprint), len(fixtureTracks))
	}
	twins := byFingerprint["aff7f05f07877bc111dda862c7a0217429b323d1aaa8a2276294096309ea7904"]
	if !slices.Equal(twins, []string{"Foxtrot Twins/Phi Same Audio/01 - Same Audio.flac", "Foxtrot Twins/Phi Same Audio/02 - Same Audio Again.flac"}) {
		t.Fatalf("the tracks with the same fingerprint are %q", twins)
	}
}

func fileSHA256(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

// The fingerprint follows the audio, not the file: a file with other tags
// has another SHA-256 and the same fingerprint, for the four codecs. It is
// what keeps the id of a track through an edit in MusicLib (§5.4).
func TestFingerprintSurvivesNewTags(t *testing.T) {
	tools := newTools(t)
	for _, rel := range []string{fixtureFLAC, fixtureMP3, fixtureAAC, fixtureALAC} {
		t.Run(rel, func(t *testing.T) {
			edited := retagged(t, rel)
			if fileSHA256(t, edited) == fileSHA256(t, inFixture(rel)) {
				t.Fatal("the retagged file is the same file: the test changed nothing")
			}
			fp, _, err := tools.Fingerprint(t.Context(), open(t, edited), containerOf(t, rel))
			if err != nil {
				t.Fatal(err)
			}
			if want := track(t, rel).fingerprint; fp != want {
				t.Fatalf("the fingerprint changed with the tags: %q, want %q", fp, want)
			}
		})
	}
}

// The fingerprint is that of the first audio stream, wherever it is among
// the streams, and of nothing else: not of a video stream before it.
func TestFingerprintOfTheAudioStreamOnly(t *testing.T) {
	fp, _, err := newTools(t).Fingerprint(t.Context(), open(t, videoFirst(t)), ContainerM4A)
	if err != nil {
		t.Fatal(err)
	}
	if want := track(t, fixtureALAC).fingerprint; fp != want {
		t.Fatalf("fingerprint %q, want that of the audio alone, %q", fp, want)
	}
}

// Files without audio, and audio said to be of another container, fail
// with a typed error and no fingerprint.
func TestFingerprintNotAudio(t *testing.T) {
	tools := newTools(t)
	empty := writeFile(t, filepath.Join(t.TempDir(), "empty"), nil, 0o644)
	const album = "Aurora Sines/Alpha_ Light_/"
	for _, tc := range []struct {
		name string
		path string
		c    Container
		code string
	}{
		{"an empty file as FLAC", empty, ContainerFLAC, CodeToolFailed},
		{"an empty file as MP3", empty, ContainerMP3, CodeToolFailed},
		{"an empty file as M4A", empty, ContainerM4A, CodeToolFailed},
		{"a cover as FLAC", inFixture(album + "cover.jpg"), ContainerFLAC, CodeToolFailed},
		{"a cover as MP3", inFixture(album + "cover.jpg"), ContainerMP3, CodeToolFailed},
		{"lyrics as M4A", inFixture(album + "01 - First Light.lrc"), ContainerM4A, CodeToolFailed},
		{"an MP3 as M4A", inFixture(fixtureMP3), ContainerM4A, CodeToolFailed},
		{"an M4A as MP3", inFixture(fixtureAAC), ContainerMP3, CodeToolFailed},
		{"a container MusicLib does not write", inFixture(fixtureFLAC), Container("ogg"), CodeNotSupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp, version, err := tools.Fingerprint(t.Context(), open(t, tc.path), tc.c)
			e := wantCode(t, err, tc.code)
			if fp != "" || version != "" {
				t.Fatalf("a failed fingerprint returned %q, %q", fp, version)
			}
			if tc.code == CodeToolFailed && len(e.Stderr) == 0 {
				t.Fatal("the error does not carry what ffmpeg said")
			}
			if bytes.Contains([]byte(err.Error()), e.Stderr) && len(e.Stderr) > 0 {
				t.Fatalf("the text of the error quotes the standard error: %v", err)
			}
		})
	}
}

// Only the one line of the hash muxer is a fingerprint.
func TestHashLine(t *testing.T) {
	const digest = "e2b19f9a76987131c390ccc590eaf6d6819c8a809fc7ffb0ca7251d29054464c"
	for out, want := range map[string]string{
		"SHA256=" + digest + "\n":                 digest,
		"SHA256=" + digest:                        "",
		"SHA256=" + digest + "\n\n":               "",
		"SHA256=" + digest + "\nSHA256=" + digest: "",
		"\nSHA256=" + digest + "\n":               "",
		"sha256=" + digest + "\n":                 "",
		"MD5=" + digest + "\n":                    "",
		"SHA256=" + digest[:63] + "\n":            "",
		"SHA256=" + digest + "0\n":                "",
		"SHA256=E2B19F9A" + digest[8:] + "\n":     "",
		"SHA256=" + digest[:63] + "g\n":           "",
		"warning\nSHA256=" + digest + "\n":        "",
		"":                                        "",
	} {
		got := ""
		if m := hashLine.FindStringSubmatch(out); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("%q: fingerprint %q, want %q", out, got, want)
		}
	}
}
