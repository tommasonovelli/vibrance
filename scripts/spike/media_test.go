package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDurationMS(t *testing.T) {
	ts := func(v int64) *int64 { return &v }
	cases := []struct {
		name   string
		s      probeStream
		want   int64
		wantOK bool
	}{
		{"FLAC 2 s", probeStream{TimeBase: "1/44100", DurationTS: ts(88200)}, 2000, true},
		{"rounded down", probeStream{TimeBase: "1/44100", DurationTS: ts(88199)}, 1999, true},
		{"MP3 time base", probeStream{TimeBase: "1/14112000", DurationTS: ts(28224000)}, 2000, true},
		{"one tick under a ms", probeStream{TimeBase: "1/1000000", DurationTS: ts(999)}, 0, true},
		{"no duration_ts", probeStream{TimeBase: "1/44100"}, 0, false},
		{"zero", probeStream{TimeBase: "1/44100", DurationTS: ts(0)}, 0, false},
		{"bad time base", probeStream{TimeBase: "44100", DurationTS: ts(1)}, 0, false},
		{"zero denominator", probeStream{TimeBase: "1/0", DurationTS: ts(1)}, 0, false},
		{"no overflow", probeStream{TimeBase: "1/1", DurationTS: ts(1 << 62)}, 0, false},
	}
	for _, c := range cases {
		got, ok := durationMS(c.s)
		if got != c.want || ok != c.wantOK {
			t.Errorf("%s: %d, %v; want %d, %v", c.name, got, ok, c.want, c.wantOK)
		}
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"plain-1.0/x:y=z": "plain-1.0/x:y=z",
		"":                "''",
		"two words":       "'two words'",
		"it's":            `'it'\''s'`,
		"$HOME":           "'$HOME'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// The fingerprint of the real fixture with the pinned ffmpeg: the two
// tracks of album F share their audio, a track of another album does not.
func TestFingerprintOnTheFixture(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	f := filepath.Join(fixtureRoot, "Foxtrot Twins", "Phi Same Audio")
	a, run, err := fingerprint(ctx, filepath.Join(f, "01 - Same Audio.flac"))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := fingerprint(ctx, filepath.Join(f, "02 - Same Audio Again.flac"))
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := fingerprint(ctx, filepath.Join(fixtureRoot, "Bravo Tones", "Beta MP3", "01 - One.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if a != b || a == c {
		t.Fatalf("F1 %s, F2 %s, B1 %s: want F1 == F2 != B1 (%s)", a, b, c, run.Cmdline)
	}
	if _, _, err := fingerprint(ctx, filepath.Join(fixtureRoot, "Bravo Tones", "Beta MP3", ".musiclib.json")); err == nil {
		t.Fatal("a receipt was fingerprinted")
	}
}

// A file that is not what its extension says fails with the tool's error,
// not with a fingerprint.
func TestFingerprintOfABadFileFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	src := filepath.Join(fixtureRoot, "Aurora Sines", "Alpha_ Light_", "cover.jpg")
	dst := filepath.Join(t.TempDir(), "not audio.flac")
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if fp, _, err := fingerprint(ctx, dst); err == nil {
		t.Fatalf("fingerprint %s for a JPEG", fp)
	}
}
