package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestClaimOrder(t *testing.T) {
	names := []string{"H10.2 b", "H2.1 a", "H12", "H1 image", "H11b", "H7a", "H10.1 a", "H9", "H11a", "meta", "H2.5 e"}
	slices.SortFunc(names, claimOrder)
	want := []string{"meta", "H1 image", "H2.1 a", "H2.5 e", "H7a", "H9", "H10.1 a", "H10.2 b", "H11a", "H11b", "H12"}
	if !slices.Equal(names, want) {
		t.Fatalf("order %q, want %q", names, want)
	}
}

func TestTrackChanges(t *testing.T) {
	before := state("b1", 1, map[string]string{"t1": "x", "t2": "y", "t3": "z"})
	after := state("b2", 2, map[string]string{"t1": "x", "t2": "Y", "t4": "w", "t5": "v"})
	changed, added, removed := trackChanges(before, after)
	if len(changed) != 1 || !strings.Contains(changed[0], "t2: Y, was y") {
		t.Errorf("changed %q", changed)
	}
	if !slices.Equal(added, []string{"t4", "t5"}) || !slices.Equal(removed, []string{"t3"}) {
		t.Errorf("added %q, removed %q", added, removed)
	}
}

func TestMoveOrder(t *testing.T) {
	ms := time.Millisecond
	cases := []struct {
		name    string
		states  []diskState
		trashed bool
		want    string
	}{
		{"source first", []diskState{{0, 3, 5}, {10 * ms, 4, 5}, {25 * ms, 4, 6}}, false, "the source first: for 15ms the moved tracks were in neither"},
		{"destination first", []diskState{{0, 3, 5}, {10 * ms, 3, 6}, {40 * ms, 4, 6}}, false, "the destination first: for 30ms the moved tracks were in both"},
		{"one pass", []diskState{{0, 3, 5}, {10 * ms, 4, 6}}, false, "both between two passes"},
		{"source trashed first", []diskState{{0, 3, 5}, {10 * ms, 0, 5}, {12 * ms, 0, 6}}, true, "the source first: for 2ms"},
		{"source trashed last", []diskState{{0, 3, 5}, {10 * ms, 3, 6}, {12 * ms, 0, 6}}, true, "the destination first: for 2ms"},
		// A folder that a pass does not find while MusicLib exchanges it is
		// not the publication of a source that keeps tracks.
		{"source missed by a pass", []diskState{{0, 3, 5}, {5 * ms, 0, 5}, {10 * ms, 3, 6}, {20 * ms, 4, 6}}, false, "the destination first: for 10ms"},
		{"nothing seen", []diskState{{0, 3, 5}}, false, "not observed"},
		{"destination not seen", []diskState{{0, 3, 5}, {10 * ms, 4, 5}}, false, "not observed"},
	}
	for _, c := range cases {
		if got := moveOrder(c.states, 3, 5, c.trashed); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// stateOf is an album read from disk whose receipt lists one audio file
// per track, and nothing wrong.
func stateOf(fps map[string]string) albumState {
	s := state("b", 1, fps)
	for i := range s.Tracks {
		s.Tracks[i].Rel = s.Tracks[i].ID + ".flac"
		s.Dir.Receipt.Files = append(s.Dir.Receipt.Files, receiptFile{RelativePath: s.Tracks[i].Rel})
	}
	return s
}

// goodMove is the move of track t2 from an album of two tracks to an album
// of one, as it must look on disk.
func goodMove() move {
	return move{
		Tracks:   []trackState{{ID: "t2", FP: "y"}},
		Src:      stateOf(map[string]string{"t1": "x", "t2": "y"}),
		Dst:      stateOf(map[string]string{"t9": "q"}),
		SrcAfter: stateOf(map[string]string{"t1": "x"}),
		DstAfter: stateOf(map[string]string{"t9": "q", "t2": "y"}),
	}
}

func TestMoveProblems(t *testing.T) {
	if m := goodMove(); m.fingerprintProblems() != nil || m.receiptProblems() != nil {
		t.Fatalf("a good move: %q, %q", m.fingerprintProblems(), m.receiptProblems())
	}
	cases := []struct {
		name            string
		change          func(m *move)
		wantFP, wantRec string // "" for no problem
	}{
		{"the moved track has another fingerprint", func(m *move) {
			m.DstAfter = stateOf(map[string]string{"t9": "q", "t2": "Y"})
		}, "moved track t2: Y, was y", ""},
		{"the moved track is not in the destination", func(m *move) {
			m.DstAfter = stateOf(map[string]string{"t9": "q"})
		}, "moved track t2 is not in the destination", "the destination gained the tracks [] and lost []"},
		{"another track of the destination changed", func(m *move) {
			m.DstAfter = stateOf(map[string]string{"t9": "Q", "t2": "y"})
		}, "track t9: Q, was q", ""},
		{"another track of the source changed", func(m *move) {
			m.SrcAfter = stateOf(map[string]string{"t1": "X"})
		}, "track t1: X, was x", ""},
		{"the source still has the track", func(m *move) {
			m.SrcAfter = stateOf(map[string]string{"t1": "x", "t2": "y"})
		}, "", "the source gained the tracks [] and lost []"},
		{"the source still lists a file with that audio", func(m *move) {
			m.SrcAfter = stateOf(map[string]string{"t1": "y"})
		}, "track t1: y, was x", "the source still lists t1.flac, with the audio of the moved track t2"},
		{"the destination lost a track", func(m *move) {
			m.DstAfter = stateOf(map[string]string{"t2": "y"})
		}, "", "the destination gained the tracks [t2] and lost [t9]"},
		{"the destination's receipt lists a file too many", func(m *move) {
			m.DstAfter.Dir.Receipt.Files = append(m.DstAfter.Dir.Receipt.Files, receiptFile{RelativePath: "stray.mp3"})
		}, "", "the destination: the receipt lists 3 audio files, the album has 2 tracks"},
		{"the source's receipt differs from its folder", func(m *move) {
			m.SrcAfter.Problems = []string{"x.flac: listed, not on disk"}
		}, "", "the source: x.flac: listed, not on disk"},
		{"a trashed source left something", func(m *move) {
			m.Trashed, m.SrcAfter, m.Left = true, albumState{}, []string{"its folder is still there"}
			m.Tracks = []trackState{{ID: "t1", FP: "x"}, {ID: "t2", FP: "y"}}
			m.DstAfter = stateOf(map[string]string{"t9": "q", "t1": "x", "t2": "y"})
		}, "", "its folder is still there"},
	}
	for _, c := range cases {
		m := goodMove()
		c.change(&m)
		fp, rec := strings.Join(m.fingerprintProblems(), ";"), strings.Join(m.receiptProblems(), ";")
		if (c.wantFP == "") != (fp == "") || !strings.Contains(fp, c.wantFP) {
			t.Errorf("%s: fingerprint problems %q, want %q", c.name, fp, c.wantFP)
		}
		if (c.wantRec == "") != (rec == "") || !strings.Contains(rec, c.wantRec) {
			t.Errorf("%s: receipt problems %q, want %q", c.name, rec, c.wantRec)
		}
	}
	// A move that takes every track: nothing is asked of the source's state.
	m := goodMove()
	m.Trashed, m.SrcAfter = true, albumState{}
	m.Tracks = []trackState{{ID: "t1", FP: "x"}, {ID: "t2", FP: "y"}}
	m.DstAfter = stateOf(map[string]string{"t9": "q", "t1": "x", "t2": "y"})
	if m.fingerprintProblems() != nil || m.receiptProblems() != nil {
		t.Fatalf("a good move of every track: %q, %q", m.fingerprintProblems(), m.receiptProblems())
	}
}

// publishRevision gives the album at rel the next album_revision, in one
// atomic step as a publication of MusicLib is: here the receipt alone is
// replaced (os.Rename cannot exchange two folders), which is all the
// poller reads.
func publishRevision(t *testing.T, lib, work, rel string) {
	t.Helper()
	name := filepath.Join(lib, filepath.FromSlash(rel), receiptName)
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parseReceipt(raw)
	if err != nil {
		t.Fatal(err)
	}
	r.AlbumRevision++
	next := filepath.Join(work, receiptName)
	if err := os.WriteFile(next, canonicalReceipt(r), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, name); err != nil {
		t.Fatal(err)
	}
}

// The poller of a move runs while one album leaves the library and the
// other is replaced by its next revision: it reports the revisions before,
// the ones after, and between them nothing but the one intermediate state;
// never an incomplete folder.
func TestPollMove(t *testing.T) {
	const from, to = "Bravo Tones/Beta MP3", "Charlie Waves/Gamma AAC"
	lib := newLibrary(t, from)
	if err := os.Mkdir(filepath.Join(lib, "Charlie Waves"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(fixtureRoot, filepath.FromSlash(to)), filepath.Join(lib, filepath.FromSlash(to))); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(filepath.Dir(lib), "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	dirs, err := scanLibrary(lib)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]albumDir{}
	for _, d := range dirs {
		ids[d.Rel] = d
	}
	fromRev, toRev := ids[from].Receipt.AlbumRevision, ids[to].Receipt.AlbumRevision

	ctx, cancel := context.WithCancel(t.Context())
	type out struct {
		poll movePoll
		err  error
	}
	done := make(chan out, 1)
	go func() {
		p, err := pollMove(ctx, lib, ids[from].Receipt.AlbumID, ids[to].Receipt.AlbumID)
		done <- out{p, err}
	}()
	time.Sleep(20 * time.Millisecond)
	// The source loses its last track: its folder and its artist's go.
	if err := os.Rename(filepath.Join(lib, "Bravo Tones"), filepath.Join(work, "gone")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	publishRevision(t, lib, work, to)
	cancel()
	o := <-done
	if o.err != nil {
		t.Fatal(o.err)
	}
	s := o.poll.States
	if len(o.poll.Partial) != 0 {
		t.Fatalf("incomplete folders: %q", o.poll.Partial)
	}
	if len(s) < 2 || len(s) > 3 || s[0].From != fromRev || s[0].To != toRev || s[len(s)-1].From != 0 || s[len(s)-1].To != toRev+1 {
		t.Fatalf("states %+v, want from %d/%d to 0/%d", s, fromRev, toRev, toRev+1)
	}
	if len(s) == 3 && (s[1].From != 0 || s[1].To != toRev) {
		t.Fatalf("states %+v: the state in between is not 0/%d", s, toRev)
	}
	if o.poll.Passes < len(s) {
		t.Fatalf("%d passes for %d states", o.poll.Passes, len(s))
	}
	if got := moveOrder(s, fromRev, toRev, true); strings.HasPrefix(got, "the destination first") || strings.HasPrefix(got, "not observed") {
		t.Fatalf("order %q for states %+v", got, s)
	}
}

// A poller whose context is already over still reads the library once: its
// last state is the one after the move.
func TestPollMoveReadsOnceMore(t *testing.T) {
	lib := newLibrary(t, watchedAlbum)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	dirs, err := scanLibrary(lib)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pollMove(ctx, lib, dirs[0].Receipt.AlbumID, "no-such-album")
	if err != nil {
		t.Fatal(err)
	}
	if p.Passes != 1 || len(p.States) != 1 || p.States[0].From != dirs[0].Receipt.AlbumRevision || p.States[0].To != 0 {
		t.Fatalf("poll %+v", p)
	}
}

func TestFixtureRenderVersion(t *testing.T) {
	dirs, err := scanLibrary(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	same, err := fixtureRenderVersion(fixtureRoot, dirs[0].Receipt.RenderVersion)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(same, "the 6 receipts") || !strings.Contains(same, "this same `render_version`") {
		t.Errorf("for the fixture's own render_version: %q", same)
	}
	other, err := fixtureRenderVersion(fixtureRoot, "musiclib-render/99")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(other, "another `render_version` (`"+dirs[0].Receipt.RenderVersion+"`)") || !strings.Contains(other, "make-fixture-library.sh") {
		t.Errorf("for another render_version: %q", other)
	}
	if _, err := fixtureRenderVersion(filepath.Join(t.TempDir(), "absent"), "r"); err == nil {
		t.Error("no error for a fixture that is not there")
	}
	empty, err := fixtureRenderVersion(t.TempDir(), "r")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(empty, "same") {
		t.Errorf("an empty fixture is called the same: %q", empty)
	}
}
