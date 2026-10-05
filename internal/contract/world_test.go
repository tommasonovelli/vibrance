//go:build contract

package contract

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"vibrance/internal/api"
)

// mlKey is a track of MusicLib in one album: MusicLib keeps the id of a
// track when it moves it to another album, Vibrance does not (the identity
// of a track holds within an album, DESIGN.md §5.4).
type mlKey struct{ album, track string }

// place is the disc and the number of a track.
type place struct{ disc, no int }

// wantItem is an item of the suite's playlist as it must be.
type wantItem struct {
	id, addedAt, track string
	position           int
}

// follow says that the references to the Vibrance track from move to the
// Vibrance track that the MusicLib track to is now: the references follow
// the audio (DESIGN.md Errata, "i riferimenti seguono l'audio").
type follow struct {
	from string
	to   mlKey
}

// world is what the suite knows and expects of the two products. After
// every scenario check compares all of it with what the APIs answer:
//
//   - Vibrance lists exactly the albums of MusicLib's library, with their
//     titles, artists and tracks;
//   - a track of MusicLib in the same album is always the same Vibrance
//     track (vib), whatever MusicLib changed;
//   - every Vibrance track ever seen still exists, and is available only
//     while it is a track of MusicLib's library (rows are never deleted,
//     I3);
//   - the counters of the index have no row the suite did not see: no
//     duplicate;
//   - the playlist and the favorites are what the scenarios say.
type world struct {
	h *harness

	// albums maps the folders of the import folder to their album id.
	albums map[string]string
	vib    map[mlKey]string
	// seenTracks and seenAlbums are every Vibrance track and album seen.
	seenTracks, seenAlbums map[string]bool

	// ml is the library of MusicLib at the last check, by album id; live
	// maps the Vibrance tracks that were available to their MusicLib key,
	// and at maps an album and a place to its Vibrance track.
	ml   map[string]mlAlbum
	live map[string]mlKey
	at   map[string]map[place]string

	// The references of the admin: one playlist and the favorites, by
	// Vibrance track, with the time each was made a favorite.
	playlist  string
	revision  int64
	items     []wantItem
	favorites map[string]string
}

func newWorld(h *harness) *world {
	return &world{
		h: h, albums: map[string]string{}, vib: map[mlKey]string{},
		seenTracks: map[string]bool{}, seenAlbums: map[string]bool{},
	}
}

// vibOf is the Vibrance track that is now at disc and no of the album of
// the folder folder.
func (w *world) vibOf(t *testing.T, folder string, disc, no int) string {
	t.Helper()
	id, ok := w.at[w.albums[folder]][place{disc, no}]
	if !ok {
		t.Fatalf("album %s has no track %d-%d", folder, disc, no)
	}
	return id
}

// sync waits for MusicLib to write its changes, asks Vibrance for a scan,
// and checks the world, after applying follows to the references.
func (w *world) sync(t *testing.T, follows ...follow) {
	t.Helper()
	w.h.ml.settle(t, w.h.library, nil)
	st := w.h.vb.scan(t)
	if st.State != api.LibraryStatusStateIdle || st.LastScan == nil || !st.LastScan.Ok {
		t.Fatalf("Vibrance: the scan did not go through the library: %+v", st)
	}
	w.check(t, st, follows)
}

// check compares the world with what the two APIs answer.
func (w *world) check(t *testing.T, st api.LibraryStatus, follows []follow) {
	t.Helper()
	vb := w.h.vb
	ml := make(map[string]mlAlbum)
	for _, a := range w.h.ml.albums(t, false) {
		ml[a.ID] = a
	}
	listed := make(map[string]bool)
	for _, a := range vb.albums(t) {
		listed[a.Id] = true
	}
	if !maps.Equal(listed, mapOf(slices.Collect(maps.Keys(ml)))) {
		t.Errorf("Vibrance lists the albums %v, MusicLib has %v", sortedKeys(listed), sortedKeys(ml))
	}

	live := make(map[string]mlKey)
	at := make(map[string]map[place]string)
	for _, a := range ml {
		w.seenAlbums[a.ID] = true
		at[a.ID] = w.checkAlbum(t, a, live)
	}
	w.checkArtists(t, ml)

	// Every track seen and gone from MusicLib's library is still there,
	// not available.
	for id := range w.seenTracks {
		if _, ok := live[id]; !ok {
			if tr := vb.track(t, id); tr.Available {
				t.Errorf("track %s (%q) is available, and is not in MusicLib's library", id, tr.Title)
			}
		}
	}
	for id := range live {
		w.seenTracks[id] = true
	}
	want := api.LibraryStatus{
		Albums: api.LibraryCounts{Available: int64(len(ml)), Unavailable: int64(len(w.seenAlbums) - len(ml))},
		Tracks: api.LibraryCounts{Available: int64(len(live)), Unavailable: int64(len(w.seenTracks) - len(live))},
	}
	if st.Albums != want.Albums || st.Tracks != want.Tracks {
		t.Errorf("the index has albums %+v and tracks %+v, want %+v and %+v: a row the suite never saw",
			st.Albums, st.Tracks, want.Albums, want.Tracks)
	}
	w.ml, w.live, w.at = ml, live, at
	if t.Failed() {
		t.FailNow()
	}
	if w.playlist != "" {
		w.checkReferences(t, live, w.apply(t, follows))
	}
}

// checkAlbum compares album a of MusicLib with the same album in Vibrance,
// records its tracks in live and returns them by place.
func (w *world) checkAlbum(t *testing.T, a mlAlbum, live map[string]mlKey) map[place]string {
	t.Helper()
	d := w.h.vb.mustAlbum(t, a.ID)
	if d.Title != a.Title || d.Artist.Name != a.ArtistName {
		t.Errorf("album %s: Vibrance has %q by %q, MusicLib %q by %q", a.ID, d.Title, d.Artist.Name, a.Title, a.ArtistName)
	}
	if len(d.Tracks) != len(a.Tracks) {
		t.Errorf("album %q: Vibrance has %d tracks, MusicLib %d", a.Title, len(d.Tracks), len(a.Tracks))
	}
	byPlace := make(map[place]api.Track, len(d.Tracks))
	for _, tr := range d.Tracks {
		byPlace[place{tr.Disc, tr.Number}] = tr
	}
	out := make(map[place]string, len(a.Tracks))
	for _, mt := range a.Tracks {
		p := place{mt.Disc, mt.No}
		vt, ok := byPlace[p]
		if !ok {
			t.Errorf("album %q: Vibrance has no track %d-%d (%q)", a.Title, p.disc, p.no, mt.Title)
			continue
		}
		if vt.Title != mt.Title || vt.Artist != artistOf(a, mt) || !vt.Available || vt.Album.Id != a.ID {
			t.Errorf("album %q, track %d-%d: Vibrance has %q by %q (available %t, album %s), MusicLib %q by %q",
				a.Title, p.disc, p.no, vt.Title, vt.Artist, vt.Available, vt.Album.Id, mt.Title, artistOf(a, mt))
		}
		key := mlKey{a.ID, mt.ID}
		if old, ok := w.vib[key]; ok && old != vt.Id {
			t.Errorf("album %q, track %q: it was the Vibrance track %s and is now %s", a.Title, mt.Title, old, vt.Id)
		}
		w.vib[key] = vt.Id
		live[vt.Id] = key
		out[p] = vt.Id
	}
	return out
}

// checkArtists compares the artists Vibrance lists with those of the
// albums of MusicLib's library.
func (w *world) checkArtists(t *testing.T, ml map[string]mlAlbum) {
	t.Helper()
	want := make(map[string]int)
	for _, a := range ml {
		want[a.ArtistName]++
	}
	got := make(map[string]int)
	for _, ar := range w.h.vb.artists(t) {
		got[ar.Name] = ar.AlbumCount
	}
	if !maps.Equal(got, want) {
		t.Errorf("Vibrance lists the artists %v, MusicLib's albums have %v", got, want)
	}
}

// apply makes the references of follows move as Vibrance must move them,
// and says whether an item of the playlist changed track.
func (w *world) apply(t *testing.T, follows []follow) bool {
	t.Helper()
	moved := false
	for _, f := range follows {
		to, ok := w.vib[f.to]
		if !ok {
			t.Fatalf("follow: the MusicLib track %v is not in Vibrance", f.to)
		}
		for i := range w.items {
			if w.items[i].track == f.from && to != f.from {
				w.items[i].track, moved = to, true
			}
		}
		if at, ok := w.favorites[f.from]; ok && to != f.from {
			delete(w.favorites, f.from)
			if _, ok := w.favorites[to]; !ok {
				w.favorites[to] = at
			}
		}
	}
	return moved
}

// checkReferences compares the playlist and the favorites with what they
// must be. live is the set of the available tracks. A playlist whose items
// changed track (moved) has a new revision; any other keeps it.
func (w *world) checkReferences(t *testing.T, live map[string]mlKey, moved bool) {
	t.Helper()
	vb := w.h.vb
	items := vb.items(t, w.playlist)
	if len(items) != len(w.items) {
		t.Errorf("the playlist has %d items, want %d", len(items), len(w.items))
	}
	for i := range min(len(items), len(w.items)) {
		got, want := items[i], w.items[i]
		if got.Id != want.id || got.Position != want.position || got.AddedAt != want.addedAt || got.Track.Id != want.track {
			t.Errorf("item %d of the playlist: %s at %d (added %s) with track %s, want %s at %d (added %s) with track %s",
				i, got.Id, got.Position, got.AddedAt, got.Track.Id, want.id, want.position, want.addedAt, want.track)
		}
		_, available := live[got.Track.Id]
		if got.Track.Available != available {
			t.Errorf("item %d of the playlist: track %s (%q) available %t, want %t", i, got.Track.Id, got.Track.Title, got.Track.Available, available)
		}
	}
	p := vb.playlist(t, w.playlist)
	switch {
	case moved && p.Revision <= w.revision:
		t.Errorf("items of the playlist changed track and its revision is still %d", p.Revision)
	case !moved && p.Revision != w.revision:
		t.Errorf("no item of the playlist changed track and its revision went from %d to %d", w.revision, p.Revision)
	}
	w.revision = p.Revision

	got := make(map[string]string)
	for _, f := range vb.favorites(t) {
		got[f.Track.Id] = f.FavoritedAt
		if _, available := live[f.Track.Id]; f.Track.Available != available {
			t.Errorf("favorite %s (%q): available %t, want %t", f.Track.Id, f.Track.Title, f.Track.Available, available)
		}
		if !f.Track.Favorite {
			t.Errorf("favorite %s: favorite is false", f.Track.Id)
		}
	}
	if !maps.Equal(got, w.favorites) {
		t.Errorf("the favorites are %v, want %v", got, w.favorites)
	}
	if t.Failed() {
		t.FailNow()
	}
}

// snapshot is the index as Vibrance lists it, in a form to compare: every
// album with its artist and every track with its place, title and state.
func (w *world) snapshot(t *testing.T) string {
	t.Helper()
	var lines []string
	for _, s := range w.h.vb.albums(t) {
		a := w.h.vb.mustAlbum(t, s.Id)
		lines = append(lines, fmt.Sprintf("%s %q %s %q", a.Id, a.Title, a.Artist.Id, a.Artist.Name))
		for _, tr := range a.Tracks {
			lines = append(lines, fmt.Sprintf("%s track %s %d-%d %q %q %t", a.Id, tr.Id, tr.Disc, tr.Number, tr.Title, tr.Artist, tr.Available))
		}
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

func mapOf(keys []string) map[string]bool {
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.SortedFunc(maps.Keys(m), cmp.Compare[string])
}
