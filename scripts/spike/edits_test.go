package main

import (
	"strings"
	"testing"
)

func state(build string, rev int64, fps map[string]string) albumState {
	s := albumState{Dir: albumDir{Receipt: receipt{AlbumID: "a", BuildID: build, AlbumRevision: rev, RenderVersion: "r"}}}
	s.API.Revision = rev
	for id, fp := range fps {
		s.Tracks = append(s.Tracks, trackState{ID: id, FP: fp})
	}
	return s
}

func TestFingerprintDiffs(t *testing.T) {
	before := state("b1", 1, map[string]string{"t1": "x", "t2": "y"})
	if d := fingerprintDiffs(before, state("b2", 2, map[string]string{"t1": "x", "t2": "y"})); d != nil {
		t.Fatalf("diffs for equal fingerprints: %q", d)
	}
	d := fingerprintDiffs(before, state("b2", 2, map[string]string{"t1": "x", "t2": "z", "t3": "w"}))
	if len(d) != 2 || !strings.Contains(d[0], "t2: z, was y") || !strings.Contains(d[1], "t3 is new") {
		t.Fatalf("diffs %q", d)
	}
	if d := fingerprintDiffs(before, state("b2", 2, map[string]string{"t1": "x"})); len(d) != 1 || !strings.Contains(d[0], "t2 is gone") {
		t.Fatalf("diffs %q", d)
	}
}

func TestReceiptDiffs(t *testing.T) {
	before := state("b1", 1, nil)
	cases := []struct {
		name    string
		after   albumState
		revises bool
		want    string // "" for no diff
	}{
		{"change", state("b2", 2, nil), true, ""},
		{"forced render", state("b2", 1, nil), false, ""},
		{"change without a new revision", state("b2", 1, nil), true, "album_revision 1 after a change"},
		{"forced render with a new revision", state("b2", 2, nil), false, "after a forced render"},
		{"same build", state("b1", 2, nil), true, "build_id unchanged"},
	}
	for _, c := range cases {
		d := receiptDiffs(before, c.after, c.revises)
		switch {
		case c.want == "" && d != nil:
			t.Errorf("%s: diffs %q", c.name, d)
		case c.want != "" && (len(d) == 0 || !strings.Contains(strings.Join(d, ";"), c.want)):
			t.Errorf("%s: diffs %q, want %q", c.name, d, c.want)
		}
	}
	other := state("b2", 2, nil)
	other.Dir.Receipt.RenderVersion = "r2"
	if d := receiptDiffs(before, other, true); len(d) != 1 {
		t.Errorf("a new render_version: diffs %q", d)
	}
	api := state("b2", 2, nil)
	api.API.Revision = 3
	if d := receiptDiffs(before, api, true); len(d) != 1 {
		t.Errorf("a receipt behind the API: diffs %q", d)
	}
}

func TestTrackSlot(t *testing.T) {
	cases := []struct {
		rel      string
		disc, no int
		ok       bool
	}{
		{"01 - One.flac", 1, 1, true},
		{"Disc 2/03 - Three - Live.flac", 2, 3, true},
		{"cover.jpg", 0, 0, false},
		{"Extras/01 - x.flac", 0, 0, false},
		{"Disc 2/x/01 - y.flac", 0, 0, false},
		{"xx - y.flac", 0, 0, false},
	}
	for _, c := range cases {
		d, n, ok := trackSlot(c.rel)
		if d != c.disc || n != c.no || ok != c.ok {
			t.Errorf("trackSlot(%q) = %d, %d, %v", c.rel, d, n, ok)
		}
	}
}
