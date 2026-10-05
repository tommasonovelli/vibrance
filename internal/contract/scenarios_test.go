//go:build contract

package contract

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"vibrance/internal/api"
)

// The scenarios of DESIGN.md §12.3, A1–A16, and A17–A20 of the erratum
// "i riferimenti seguono l'audio". Each changes MusicLib through its API,
// then w.sync checks the whole world (see world); what is particular to the
// scenario is checked here.

// a15 imports the first albums while Vibrance's index is empty, then stops
// Vibrance in the middle of the first scan that sees them (a signal while
// it runs ffprobe or ffmpeg), and restarts MusicLib and Vibrance in a random
// order. No album and no track may be lost or doubled: the counters of the
// index have no row the suite did not see.
func a15(t *testing.T, w *world) {
	h := w.h
	for _, f := range []string{"alpha", "beta", "gamma", "delta", "phi"} {
		w.albums[f] = h.ml.importFolder(t, f)
	}
	h.ml.settle(t, h.library, nil)
	if st := h.vb.status(t); st.Albums.Available+st.Albums.Unavailable != 0 {
		t.Fatalf("Vibrance indexed albums before the scan the suite asks for: %+v", st.Albums)
	}
	sig := []string{"KILL", "TERM"}[h.rng.IntN(2)]
	h.act(t, "vibrance-watch", sig)
	h.vb.requestScan(t)
	got := h.act(t, "vibrance-watch-result")
	if !strings.HasPrefix(got, "signal ") {
		t.Fatalf("Vibrance was not stopped during its first scan: %s", got)
	}
	t.Logf("Vibrance: %s", got)
	for range 3 {
		service := []string{"app", "vibrance"}[h.rng.IntN(2)]
		t.Logf("restarting %s", service)
		h.act(t, "restart", service)
	}
	h.act(t, "wait-healthy")
	h.vb.waitReady(t)
	w.sync(t)
}

// references makes one playlist with every track, and every track a
// favorite: the references every later scenario starts from.
func references(t *testing.T, w *world) {
	var p api.Playlist
	err := w.h.vb.call(t.Context(), http.MethodPost, "/api/v1/playlists",
		api.PlaylistInput{Name: "Contract", Description: "Every track of the contract suite."}, http.StatusCreated, &p)
	if err != nil {
		t.Fatal(err)
	}
	w.playlist, w.revision, w.favorites = p.Id, p.Revision, map[string]string{}
	w.addReferences(t, w.tracksOf("alpha", "beta", "gamma", "delta", "phi"))
	w.sync(t)
}

// a1: the title of a track changes; the track keeps its id.
func a1(t *testing.T, w *world) {
	a := w.album(t, "alpha")
	mt := a.track(t, 1, 1)
	id, title := w.vib[key(a, mt)], mt.Title+" (edited)"
	w.h.ml.edit(t, a, setTrack(mt.ID, func(u *trackUpdate) { u.Title = title }))
	w.sync(t)
	if tr := w.h.vb.track(t, id); tr.Title != title || !tr.Available || !tr.Favorite {
		t.Errorf("track %s: %q, available %t, favorite %t; want %q, available, favorite", id, tr.Title, tr.Available, tr.Favorite, title)
	}
	for _, it := range w.h.vb.items(t, w.playlist) {
		if it.Track.Id == id && it.Track.Title != title {
			t.Errorf("item %s shows %q, not %q", it.Id, it.Track.Title, title)
		}
	}
}

// a2: two track numbers are swapped; the ids follow the audio.
func a2(t *testing.T, w *world) {
	a := w.album(t, "alpha")
	one, two := a.track(t, 1, 1), a.track(t, 1, 2)
	v1, v2 := w.vib[key(a, one)], w.vib[key(a, two)]
	w.h.ml.edit(t, a, func(u *albumUpdate) {
		setTrack(one.ID, func(u *trackUpdate) { u.No = 2 })(u)
		setTrack(two.ID, func(u *trackUpdate) { u.No = 1 })(u)
	})
	w.sync(t)
	if n1, n2 := w.h.vb.track(t, v1).Number, w.h.vb.track(t, v2).Number; n1 != 2 || n2 != 1 {
		t.Errorf("after the swap the tracks %s and %s have the numbers %d and %d, want 2 and 1", v1, v2, n1, n2)
	}
}

// coverPNG is a cover none of the albums has.
func coverPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 300, 300))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 200, G: 40, B: 90, A: 255}}, image.Point{}, draw.Src)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// a3: a new cover. The ids stay, the hash changes, and a URL with the hash
// of the old cover serves the current one, to be revalidated.
func a3(t *testing.T, w *world) {
	vb := w.h.vb
	id := w.albums["alpha"]
	before := vb.mustAlbum(t, id)
	if before.Cover == nil {
		t.Fatalf("album %q has no cover in Vibrance: MusicLib did not take the cover.jpg of its import", before.Title)
	}
	w.h.ml.do(t, http.MethodPut, "/api/albums/"+id+"/cover", w.album(t, "alpha"), rawBody(coverPNG(t)))
	w.sync(t)
	after := vb.mustAlbum(t, id)
	if after.Cover == nil || after.Cover.Hash == before.Cover.Hash {
		t.Fatalf("the cover is %+v, was %+v: the hash did not change", after.Cover, before.Cover)
	}
	for _, c := range []struct {
		path, etag, cache string
	}{
		{before.Cover.Url, `"` + after.Cover.Hash + `-640"`, "private, no-cache"},
		{before.Cover.Url + "&size=original", `"` + after.Cover.Hash + `-original"`, "private, no-cache"},
		{after.Cover.Url + "&size=original", `"` + after.Cover.Hash + `-original"`, "private, max-age=31536000, immutable"},
	} {
		resp, body, err := vb.send(t.Context(), http.MethodGet, c.path, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || resp.Header.Get("ETag") != c.etag || resp.Header.Get("Cache-Control") != c.cache {
			t.Errorf("GET %s: %d, ETag %s, Cache-Control %q; want 200, %s, %q",
				c.path, resp.StatusCode, resp.Header.Get("ETag"), resp.Header.Get("Cache-Control"), c.etag, c.cache)
		}
		if strings.HasSuffix(c.path, "size=original") && sha256Hex(body) != after.Cover.Hash {
			t.Errorf("GET %s: the bytes are not the current cover", c.path)
		}
	}
}

// a4: the album is renamed. While MusicLib writes it, two folders with the
// same album_id can exist for a moment: Vibrance never lists the album
// twice, nor more albums than there are.
func a4(t *testing.T, w *world) {
	vb := w.h.vb
	a := w.album(t, "alpha")
	title := a.Title + " Renamed"
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- watchListings(ctx, vb, a.ID, len(w.ml)) }()
	w.h.ml.edit(t, a, func(u *albumUpdate) { u.Title = title })
	w.h.ml.settle(t, w.h.library, nil)
	stop()
	if err := <-done; err != nil {
		t.Error(err)
	}
	w.sync(t)
	if got := vb.mustAlbum(t, a.ID).Title; got != title {
		t.Errorf("album %s is %q, want %q", a.ID, got, title)
	}
}

// watchListings asks Vibrance for scans and lists its albums until ctx
// ends; it fails on a list with more than n albums or with album id more
// than once, and on a request that fails for another reason than the end
// of ctx.
func watchListings(ctx context.Context, vb *vibrance, id string, n int) error {
	lists := 0
	for ctx.Err() == nil {
		if err := vb.call(ctx, http.MethodPost, "/api/v1/admin/library/scan", nil, http.StatusAccepted, nil); err != nil {
			if ctx.Err() != nil {
				break
			}
			return fmt.Errorf("during the rename: %w", err)
		}
		var l api.AlbumList
		if err := vb.call(ctx, http.MethodGet, "/api/v1/albums?limit=200", nil, http.StatusOK, &l); err != nil {
			if ctx.Err() != nil {
				break
			}
			return fmt.Errorf("during the rename: %w", err)
		}
		lists++
		count := 0
		for _, a := range l.Albums {
			if a.Id == id {
				count++
			}
		}
		if len(l.Albums) > n || count > 1 {
			return fmt.Errorf("during the rename Vibrance listed %d albums (at most %d), the renamed one %d times", len(l.Albums), n, count)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if lists == 0 {
		return fmt.Errorf("Vibrance was not listed during the rename: %w", ctx.Err())
	}
	return nil
}

// a5: the artist is renamed. Album and tracks stay; the artist has a new id
// and the lists agree.
func a5(t *testing.T, w *world) {
	vb := w.h.vb
	a := w.album(t, "beta")
	old := vb.mustAlbum(t, a.ID).Artist
	name := a.ArtistName + " Renamed"
	w.h.ml.renameArtist(t, a.ArtistID, name)
	w.sync(t)
	d := vb.mustAlbum(t, a.ID)
	if d.Artist.Id == old.Id || d.Artist.Name != name {
		t.Errorf("the artist of album %q is %+v, was %+v: want a new id named %q", a.Title, d.Artist, old, name)
	}
	checkArtist(t, vb, old.Id, d.Artist.Id, a.ID)
}

// checkArtist checks that the artist old is gone and that the artist now
// lists the albums ids, in its page and as the filter of the albums.
func checkArtist(t *testing.T, vb *vibrance, old, now string, ids ...string) {
	t.Helper()
	vb.wantError(t, "/api/v1/artists/"+old, http.StatusNotFound, "artist_not_found")
	var ad api.ArtistDetail
	vb.get(t, "/api/v1/artists/"+now, &ad)
	var l api.AlbumList
	vb.get(t, "/api/v1/albums?limit=200&artist="+now, &l)
	for _, list := range [][]api.AlbumSummary{ad.Albums, l.Albums} {
		var got []string
		for _, a := range list {
			got = append(got, a.Id)
		}
		slices.Sort(got)
		want := slices.Sorted(slices.Values(ids))
		if !slices.Equal(got, want) {
			t.Errorf("the artist %s lists the albums %v, want %v", now, got, want)
		}
	}
}

// a6: the album moves to another artist: as a5.
func a6(t *testing.T, w *world) {
	vb := w.h.vb
	c, d := w.album(t, "gamma"), w.album(t, "delta")
	old := vb.mustAlbum(t, c.ID).Artist
	w.h.ml.edit(t, c, func(u *albumUpdate) { u.ArtistID = &d.ArtistID })
	w.sync(t)
	vc, vd := vb.mustAlbum(t, c.ID), vb.mustAlbum(t, d.ID)
	if vc.Artist != vd.Artist || vc.Artist.Id == old.Id {
		t.Errorf("album %q has the artist %+v, want %+v of album %q", c.Title, vc.Artist, vd.Artist, d.Title)
	}
	checkArtist(t, vb, old.Id, vd.Artist.Id, c.ID, d.ID)
}

// a7: a track is deleted: it becomes unavailable and stays in the playlist
// and the favorites; the others do not change.
func a7(t *testing.T, w *world) {
	a := w.album(t, "alpha")
	mt := a.track(t, 1, 3)
	id := w.vib[key(a, mt)]
	w.h.ml.do(t, http.MethodDelete, "/api/albums/"+a.ID+"/tracks/"+mt.ID, a, nil)
	w.sync(t)
	if tr := w.h.vb.track(t, id); tr.Available || !tr.Favorite || tr.Title != mt.Title {
		t.Errorf("the deleted track %s: %q, available %t, favorite %t; want %q, not available, favorite", id, tr.Title, tr.Available, tr.Favorite, mt.Title)
	}
	w.h.vb.wantError(t, "/api/v1/tracks/"+id+"/audio", http.StatusNotFound, "track_unavailable")
}

// a8: the album goes to the trash: out of the lists, its tracks
// unavailable, shown in grey in the playlist and the favorites.
func a8(t *testing.T, w *world) {
	d := w.album(t, "delta")
	ids := w.tracksOf("delta")
	w.h.ml.do(t, http.MethodDelete, "/api/albums/"+d.ID, d, nil)
	w.sync(t)
	w.h.vb.wantError(t, "/api/v1/albums/"+d.ID, http.StatusNotFound, "album_not_found")
	for _, id := range ids {
		w.h.vb.wantError(t, "/api/v1/tracks/"+id+"/audio", http.StatusNotFound, "track_unavailable")
	}
}

// a9: the album comes back from the trash with the same album id and the
// same track ids, available again.
func a9(t *testing.T, w *world) {
	d := w.album(t, "delta")
	if !d.Trashed {
		t.Fatalf("album %q is not in the trash", d.Title)
	}
	w.h.ml.do(t, http.MethodPost, "/api/albums/"+d.ID+"/restore", d, nil)
	w.sync(t)
	for _, tr := range d.Tracks {
		if id := w.vib[key(d, tr)]; !w.h.vb.track(t, id).Available {
			t.Errorf("track %s of the restored album is not available", id)
		}
	}
}

// a10: MusicLib writes every album again ("Rebuild the library folder"
// online): the index does not change.
func a10(t *testing.T, w *world) {
	before := w.snapshot(t)
	rendered := builds(t, w.h.library)
	if err := w.h.ml.call(t.Context(), http.MethodPost, "/api/render-all", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	w.h.ml.settle(t, w.h.library, rendered)
	w.sync(t)
	if after := w.snapshot(t); after != before {
		t.Errorf("the index changed with the render of every album:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// a11: MusicLib's offline rebuild, with the app stopped. While it runs
// (held on its marker), the state is maintenance and the index is intact.
// When the command ends library/ is empty until the app renders again
// (NOTES.md N-014): every album is unavailable and nothing is lost. Once
// MusicLib has rendered again every album is back with the same ids.
func a11(t *testing.T, w *world) {
	vb := w.h.vb
	before := w.snapshot(t)
	rendered := builds(t, w.h.library)
	w.h.act(t, "rebuild-freeze")
	st := vb.scanMaintenance(t)
	if !st.MusiclibMaintenance {
		t.Errorf("the state is maintenance and musiclib_maintenance is false")
	}
	if during := w.snapshot(t); during != before {
		t.Errorf("the index changed during the rebuild:\nbefore:\n%s\nduring:\n%s", before, during)
	}
	w.checkReferences(t, w.live, false)
	t.Logf("MusicLib: %s", w.h.act(t, "rebuild-continue"))

	st = vb.scan(t)
	if st.Albums.Available != 0 || st.Tracks.Available != 0 {
		t.Errorf("with library/ empty after the rebuild the index has %+v albums and %+v tracks available", st.Albums, st.Tracks)
	}
	if st.Albums.Unavailable != int64(len(w.seenAlbums)) || st.Tracks.Unavailable != int64(len(w.seenTracks)) {
		t.Errorf("with library/ empty the index has %+v albums and %+v tracks: a row was lost", st.Albums, st.Tracks)
	}
	w.checkReferences(t, nil, false)

	w.h.act(t, "start-app")
	w.h.ml.settle(t, w.h.library, rendered)
	w.sync(t)
	if after := w.snapshot(t); after != before {
		t.Errorf("the index is not as before the rebuild:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// a12: a new album appears within one cycle of the scanner. Its tracks join
// the references, for a20.
func a12(t *testing.T, w *world) {
	id := w.h.ml.importFolder(t, "epsilon")
	w.albums["epsilon"] = id
	w.h.ml.settle(t, w.h.library, nil)
	w.h.vb.scan(t)
	a, err := w.h.vb.album(t.Context(), id)
	if err != nil || len(a.Tracks) != len(w.h.ml.album(t, id).Tracks) {
		t.Fatalf("one cycle after MusicLib published it, the new album is %+v, %v", a, err)
	}
	w.sync(t)
	w.addReferences(t, w.tracksOf("epsilon"))
	w.sync(t)
}

// a13: MusicLib writes an album again while a client plays one of its
// tracks. A response opened before has the old file whole; a Range with an
// If-Range of the old ETag gets the whole new file (200), or 503
// library_changing and then the whole new file after the scan: never bytes
// of two versions.
func a13(t *testing.T, w *world) {
	ctx, vb := t.Context(), w.h.vb
	b := w.album(t, "beta")
	mt := b.track(t, 1, 1)
	id := w.vib[key(b, mt)]
	path := "/api/v1/tracks/" + id + "/audio"
	resp, first, err := vb.send(ctx, http.MethodGet, path, http.Header{"Range": {"bytes=0-999"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	old := resp.Header.Get("ETag")
	if resp.StatusCode != http.StatusPartialContent || len(first) != 1000 || old == "" {
		t.Fatalf("GET %s with a Range: %d, %d bytes, ETag %q", path, resp.StatusCode, len(first), old)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, vb.base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	playing, err := vb.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 1000)
	if _, err := io.ReadFull(playing.Body, head); err != nil || playing.StatusCode != http.StatusOK || playing.Header.Get("ETag") != old {
		t.Fatalf("GET %s: %d, ETag %s, %v", path, playing.StatusCode, playing.Header.Get("ETag"), err)
	}

	// The track's own genre: its file changes and keeps its path.
	genre := "Contract Genre"
	w.h.ml.edit(t, b, setTrack(mt.ID, func(u *trackUpdate) { u.Genre = &genre }))
	w.h.ml.settle(t, w.h.library, nil)

	rest, err := io.ReadAll(playing.Body)
	if cerr := playing.Body.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := `"` + sha256Hex(append(head, rest...)) + `"`; got != old {
		t.Errorf("a response opened before MusicLib wrote the album has bytes of another file: %s, ETag %s", got, old)
	}

	ask := func() (*http.Response, []byte) {
		resp, body, err := vb.send(ctx, http.MethodGet, path, http.Header{"Range": {"bytes=1000-"}, "If-Range": {old}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return resp, body
	}
	resp, body := ask()
	if resp.StatusCode == http.StatusServiceUnavailable {
		if err := failure(http.MethodGet, path, resp, body); !isError(err, http.StatusServiceUnavailable, "library_changing") || resp.Header.Get("Retry-After") == "" {
			t.Fatalf("GET %s: %v, Retry-After %q; want 503 library_changing with Retry-After", path, err, resp.Header.Get("Retry-After"))
		}
		t.Logf("503 library_changing before the scan")
		vb.scan(t)
		resp, body = ask()
	}
	etag := resp.Header.Get("ETag")
	switch {
	case resp.StatusCode != http.StatusOK:
		t.Fatalf("GET %s with the Range and If-Range of the old file: %d; want the whole new file (200)", path, resp.StatusCode)
	case etag == old || `"`+sha256Hex(body)+`"` != etag:
		t.Errorf("GET %s with If-Range of the old file: ETag %s (old %s), the bytes are not the whole file of that ETag", path, etag, old)
	}
	w.sync(t)
	if tr := w.h.vb.track(t, id); tr.Genre == nil || *tr.Genre != genre {
		t.Errorf("track %s has the genre %v, want %q", id, tr.Genre, genre)
	}
}

// a14: two tracks of an album have the same audio, and the first is
// deleted. The second keeps its id; the references of the first follow the
// audio to it (one favorite stays).
func a14(t *testing.T, w *world) {
	f := w.album(t, "phi")
	one, two := f.track(t, 1, 1), f.track(t, 1, 2)
	v1, v2 := w.vib[key(f, one)], w.vib[key(f, two)]
	w.h.ml.do(t, http.MethodDelete, "/api/albums/"+f.ID+"/tracks/"+one.ID, f, nil)
	w.sync(t, follow{from: v1, to: key(f, two)})
	if got := w.vib[key(f, two)]; got != v2 {
		t.Errorf("the second track was %s and is %s", v2, got)
	}
	if w.h.vb.track(t, v1).Available {
		t.Errorf("the deleted track %s is available", v1)
	}
}

// a16: library/ cannot be read for a while (renamed from inside MusicLib's
// container). The state is unavailable and the index stays as it was (the
// erratum of 2026-10-04 to A16): the files answer 404; when library/ is
// back everything is as it was.
func a16(t *testing.T, w *world) {
	vb := w.h.vb
	before := w.snapshot(t)
	w.h.act(t, "hide-library")
	st := vb.scan(t)
	if st.State != api.LibraryStatusStateUnavailable {
		t.Errorf("with library/ gone the state is %s, want unavailable", st.State)
	}
	if during := w.snapshot(t); during != before {
		t.Errorf("the index changed with library/ gone:\nbefore:\n%s\nduring:\n%s", before, during)
	}
	w.checkReferences(t, w.live, false)
	vb.wantError(t, "/api/v1/tracks/"+w.tracksOf("beta")[0]+"/audio", http.StatusNotFound, "track_unavailable")
	// The original: a thumbnail made before is in Vibrance's cache, not in library/.
	vb.wantError(t, "/api/v1/albums/"+w.albums["alpha"]+"/cover?size=original", http.StatusNotFound, "cover_not_found")
	w.h.act(t, "show-library")
	w.sync(t)
	if after := w.snapshot(t); after != before {
		t.Errorf("the index is not as before:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// a17: a track moves to another album, then its title and artist change
// there. Its item stays at its place with the new data, its favorite stays,
// both on the new track with the same audio.
func a17(t *testing.T, w *world) {
	vb := w.h.vb
	b, a := w.album(t, "beta"), w.album(t, "alpha")
	mt := b.track(t, 1, 2)
	old := w.vib[key(b, mt)]
	w.h.ml.moveTracks(t, b, []string{mt.ID}, a.ID)
	w.sync(t, follow{from: old, to: mlKey{a.ID, mt.ID}})

	a = w.album(t, "alpha")
	title, artist := "Moved Away", "Someone Else"
	w.h.ml.edit(t, a, setTrack(mt.ID, func(u *trackUpdate) { u.Title, u.Artist = title, &artist }))
	w.sync(t)
	now := w.vib[mlKey{a.ID, mt.ID}]
	found := false
	for _, it := range vb.items(t, w.playlist) {
		if it.Track.Id == now {
			found = true
			if it.Track.Title != title || it.Track.Artist != artist || it.Track.Album.Id != a.ID || !it.Track.Available {
				t.Errorf("item %s shows %q by %q in album %s (available %t), want %q by %q in %s",
					it.Id, it.Track.Title, it.Track.Artist, it.Track.Album.Id, it.Track.Available, title, artist, a.ID)
			}
		}
	}
	if !found || vb.track(t, old).Available {
		t.Errorf("no item follows the moved track to %s, or the old track %s is still available", now, old)
	}
}

// a18: every track of an album moves to another: each follows, and the
// album of origin leaves the lists.
func a18(t *testing.T, w *world) {
	c, d := w.album(t, "gamma"), w.album(t, "delta")
	var ids []string
	var follows []follow
	for _, tr := range c.Tracks {
		ids = append(ids, tr.ID)
		follows = append(follows, follow{from: w.vib[key(c, tr)], to: mlKey{d.ID, tr.ID}})
	}
	w.h.ml.moveTracks(t, c, ids, d.ID)
	w.sync(t, follows...)
	w.h.vb.wantError(t, "/api/v1/albums/"+c.ID, http.StatusNotFound, "album_not_found")
}

// a19: two tracks are swapped between two albums: both follow.
func a19(t *testing.T, w *world) {
	a, b := w.album(t, "alpha"), w.album(t, "beta")
	x, y := a.Tracks[0], b.Tracks[0]
	follows := []follow{
		{from: w.vib[key(a, x)], to: mlKey{b.ID, x.ID}},
		{from: w.vib[key(b, y)], to: mlKey{a.ID, y.ID}},
	}
	w.h.ml.moveTracks(t, a, []string{x.ID}, b.ID)
	w.h.ml.moveTracks(t, w.album(t, "beta"), []string{y.ID}, a.ID)
	w.sync(t, follows...)
}

// a20: the album goes to the trash, the trash is emptied, and the same
// folder is imported again: a new album, and the playlist and the
// favorites move to its tracks.
func a20(t *testing.T, w *world) {
	ml := w.h.ml
	e := w.album(t, "epsilon")
	olds := make(map[place]string)
	for _, tr := range e.Tracks {
		olds[place{tr.Disc, tr.No}] = w.vib[key(e, tr)]
	}
	ml.do(t, http.MethodDelete, "/api/albums/"+e.ID, e, nil)
	w.sync(t)

	deadline := time.Now().Add(settleTimeout)
	for {
		if err := ml.call(t.Context(), http.MethodPost, "/api/trash/empty", "", nil, nil); err != nil {
			t.Fatal(err)
		}
		err := ml.call(t.Context(), http.MethodGet, "/api/albums/"+e.ID, "", nil, nil)
		if me := (*mlError)(nil); errors.As(err, &me) && me.status == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("emptying the trash did not delete album %q: %v", e.Title, err)
		}
		time.Sleep(pollEvery)
	}

	id := ml.importFolder(t, "epsilon")
	if id == e.ID {
		t.Fatalf("the album imported again has the album_id it had before the trash was emptied")
	}
	w.albums["epsilon"] = id
	ne := ml.album(t, id)
	var follows []follow
	for _, tr := range ne.Tracks {
		from, ok := olds[place{tr.Disc, tr.No}]
		if !ok {
			t.Fatalf("the album imported again has a track %d-%d it did not have", tr.Disc, tr.No)
		}
		follows = append(follows, follow{from: from, to: mlKey{id, tr.ID}})
	}
	w.sync(t, follows...)
}
