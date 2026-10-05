package app

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"vibrance/internal/api"
	"vibrance/internal/catalog"
	"vibrance/internal/httpx"
	"vibrance/internal/library"
	"vibrance/internal/media"
	"vibrance/internal/names"
	"vibrance/internal/store"
)

// The operations of the catalog (DESIGN.md §8.3, step S15) over the API, on
// the fixture library indexed by the real indexer with the pinned tools.
// The orders and the pagination themselves are proved in internal/catalog
// on random indexes; here every answer is also checked against the
// specification (I10).

// The albums of the fixture library (testdata/FIXTURE.md).
const (
	albumA = "01a0f459-ebe7-73fe-9598-e85475ef5cca" // FLAC, cover, lyrics, ReplayGain
	albumB = "01a0f459-ebc8-7081-991c-2b1c9331e156" // MP3
	albumE = "01a0f459-eca5-7127-a52f-ab0f69357431" // FLAC on two discs
)

// indexFixture indexes a copy of the fixture library into the database of
// w, with the indexer and the tools the scanner uses, and serves its files.
func (w *world) indexFixture() {
	w.t.Helper()
	ctx := w.t.Context()
	w.indexed = true
	w.musiclib = fixtureLibrary(w.t)
	w.rescans = publishMedia(w.t, w.s, w.store, w.musiclib)
	root, err := library.OpenRoot(w.musiclib)
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() {
		if err := root.Close(); err != nil {
			w.t.Error(err)
		}
	})
	tools, err := media.NewTools(ctx, media.NewRunner(testWorkers), media.FFmpegPath, media.FFprobePath)
	if err != nil {
		w.t.Fatal(err)
	}
	d, err := library.Discover(ctx, root, nil)
	if err != nil || len(d.Candidates) != 6 {
		w.t.Fatalf("discovering the fixture library: %d albums, %v", len(d.Candidates), err)
	}
	ix := library.NewIndexer(root, w.store, tools, library.NoCoverWarmer{}, time.Now)
	for _, c := range d.Candidates {
		if problems, err := ix.IndexAlbum(ctx, c); err != nil || len(problems) != 0 {
			w.t.Fatalf("indexing %s: %v %v", c.RelPath, problems, err)
		}
	}
}

// get reads an operation of the catalog as a; the answer conforms.
func (w *world) get(path string, a *account) *httptest.ResponseRecorder {
	w.t.Helper()
	return w.do(http.MethodGet, path, nil, w.as(a))
}

// unavailable makes an album and its tracks unavailable, as the scanner
// does when its folder is gone (§6.4).
func (w *world) unavailable(albumID string) {
	w.t.Helper()
	ctx := w.t.Context()
	err := w.store.WithWriteTx(ctx, func(q *store.Queries) error {
		if err := q.SetTracksOfAlbumUnavailable(ctx, store.SetTracksOfAlbumUnavailableParams{UpdatedAt: 1, AlbumID: albumID}); err != nil {
			return err
		}
		return q.SetAlbumUnavailable(ctx, store.SetAlbumUnavailableParams{UpdatedAt: 1, ID: albumID})
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

// favorite makes a track a favorite of a. The favorites are the business of
// step S18: the row is written by hand.
func (w *world) favorite(a *account, trackID string) {
	w.t.Helper()
	err := w.store.WithWriteTx(w.t.Context(), func(q *store.Queries) error {
		_, err := q.Conn().ExecContext(w.t.Context(), `INSERT INTO favorites (user_id, track_id, created_at) VALUES (?, ?, 1)`, a.id, trackID)
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

func TestListArtists(t *testing.T) {
	w := newWorld(t, apiOrigin)
	rec := w.get("/artists", w.anna)
	wantStatus(t, "an empty index", rec, http.StatusOK)
	if got := decode[api.ArtistList](t, rec); len(got.Artists) != 0 || got.Next != nil {
		t.Fatalf("an empty index: %+v", got)
	}

	w.indexFixture()
	want := []string{"Aurora Sines", "Bravo Tones", "Charlie Waves", "Delta Pulse", "Écho Café", "Foxtrot Twins"}
	all := decode[api.ArtistList](t, w.get("/artists", w.anna))
	var got []string
	for _, a := range all.Artists {
		got = append(got, a.Name)
		if a.AlbumCount != 1 {
			t.Errorf("%s has %d albums", a.Name, a.AlbumCount)
		}
	}
	if !slices.Equal(got, want) || all.Next != nil {
		t.Fatalf("artists %q (next %v), want %q", got, all.Next, want)
	}

	// One at a time, following next.
	var paged []api.ArtistSummary
	path := "/artists?limit=1"
	for range 10 {
		page := decode[api.ArtistList](t, w.get(path, w.anna))
		paged = append(paged, page.Artists...)
		if page.Next == nil {
			break
		}
		path = "/artists?limit=1&after=" + url.QueryEscape(*page.Next)
	}
	if !slices.Equal(paged, all.Artists) {
		t.Fatalf("one at a time: %+v, want %+v", paged, all.Artists)
	}

	// An artist whose only album is gone leaves the list.
	w.unavailable(albumB)
	all = decode[api.ArtistList](t, w.get("/artists", w.anna))
	for _, a := range all.Artists {
		if a.Name == "Bravo Tones" {
			t.Fatalf("an artist without an available album is listed: %+v", a)
		}
	}
	if len(all.Artists) != 5 {
		t.Fatalf("%d artists", len(all.Artists))
	}
}

func TestListAlbums(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	for _, sort := range []string{"title", "artist", "year", "added"} {
		for _, order := range []string{"asc", "desc"} {
			query := "?sort=" + sort + "&order=" + order
			whole := decode[api.AlbumList](t, w.get("/albums"+query+"&limit=200", w.anna))
			if len(whole.Albums) != 6 || whole.Next != nil {
				t.Fatalf("%s: %d albums, next %v", query, len(whole.Albums), whole.Next)
			}
			var paged []api.AlbumSummary
			path := "/albums" + query + "&limit=4"
			for range 3 {
				page := decode[api.AlbumList](t, w.get(path, w.bob))
				paged = append(paged, page.Albums...)
				if page.Next == nil {
					break
				}
				path = "/albums" + query + "&limit=4&after=" + url.QueryEscape(*page.Next)
			}
			if !slices.EqualFunc(paged, whole.Albums, func(a, b api.AlbumSummary) bool { return a.Id == b.Id }) {
				t.Fatalf("%s: pages of 4 differ from the whole list", query)
			}
		}
	}
	// The default order is by title, ascending; the titles of the fixture
	// are all different.
	def := decode[api.AlbumList](t, w.get("/albums", w.anna))
	byTitle := decode[api.AlbumList](t, w.get("/albums?sort=title&order=asc", w.anna))
	if !slices.EqualFunc(def.Albums, byTitle.Albums, func(a, b api.AlbumSummary) bool { return a.Id == b.Id }) {
		t.Fatal("the default order is not by title, ascending")
	}

	// The albums of one artist, and those of no artist.
	echo := decode[api.AlbumList](t, w.get("/albums?artist="+def.Albums[0].Artist.Id, w.anna))
	if len(echo.Albums) != 1 || echo.Albums[0].Id != def.Albums[0].Id {
		t.Fatalf("the albums of %s: %+v", def.Albums[0].Artist.Name, echo.Albums)
	}
	if none := decode[api.AlbumList](t, w.get("/albums?artist="+someID, w.anna)); len(none.Albums) != 0 || none.Next != nil {
		t.Fatalf("the albums of no artist: %+v", none)
	}

	// An album that is not available is in no list.
	w.unavailable(albumB)
	for _, sort := range []string{"title", "artist", "year", "added"} {
		list := decode[api.AlbumList](t, w.get("/albums?sort="+sort, w.anna))
		if len(list.Albums) != 5 || slices.ContainsFunc(list.Albums, func(a api.AlbumSummary) bool { return a.Id == albumB }) {
			t.Fatalf("%s: %d albums, B among them", sort, len(list.Albums))
		}
	}
}

// §8.4: a cursor that is not one of the list, with its sort and order, is
// 400 invalid_cursor, and says nothing of the cursor.
func TestInvalidCursors(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	title := decode[api.AlbumList](t, w.get("/albums?limit=1", w.anna)).Next
	artists := decode[api.ArtistList](t, w.get("/artists?limit=1", w.anna)).Next
	if title == nil || artists == nil {
		t.Fatal("no cursor")
	}
	for _, path := range []string{
		"/albums?after=x",
		"/albums?after=",
		"/albums?sort=year&after=" + url.QueryEscape(*title),
		"/albums?order=desc&after=" + url.QueryEscape(*title),
		"/albums?after=" + url.QueryEscape(*artists),
		"/artists?after=" + url.QueryEscape(*title),
		"/artists?after=",
		"/artists?after=%27%20OR%201%3D1%20--",
	} {
		rec := w.get(path, w.anna)
		wantCode(t, path, rec, http.StatusBadRequest, "invalid_cursor")
		if _, message := errorBody(t, path, rec); message != httpx.InvalidCursor().Message {
			t.Errorf("%s: message %q", path, message)
		}
	}
}

func TestGetArtist(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	artists := decode[api.ArtistList](t, w.get("/artists", w.anna)).Artists
	for _, a := range artists {
		d := decode[api.ArtistDetail](t, w.get("/artists/"+a.Id, w.anna))
		if d.Id != a.Id || d.Name != a.Name || len(d.Albums) != 1 || d.Albums[0].Artist.Id != a.Id {
			t.Fatalf("artist %+v: %+v", a, d)
		}
	}
	wantCode(t, "no such artist", w.get("/artists/"+someID, w.anna), http.StatusNotFound, catalog.CodeArtistNotFound)
	bravo := artists[1]
	w.unavailable(albumB)
	wantCode(t, "an artist whose albums are gone", w.get("/artists/"+bravo.Id, w.anna), http.StatusNotFound,
		catalog.CodeArtistNotFound)
}

func TestGetAlbum(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()

	// A: a cover, whose URL carries its hash; lyrics and ReplayGain on the
	// first track.
	a := decode[api.AlbumDetail](t, w.get("/albums/"+albumA, w.anna))
	cover, err := os.ReadFile(filepath.Join("..", "..", "testdata", "library-v1", "Aurora Sines", "Alpha_ Light_", "cover.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cover)
	hash := hex.EncodeToString(sum[:])
	if a.Cover == nil || a.Cover.Hash != hash || a.Cover.Url != "/api/v1/albums/"+albumA+"/cover?v="+hash {
		t.Fatalf("the cover of A: %+v, want the hash %s", a.Cover, hash)
	}
	if a.TrackCount != 3 || len(a.Tracks) != 3 || a.DiscCount != 1 || a.Artist.Name != "Aurora Sines" {
		t.Fatalf("A: %+v", a)
	}
	var duration int64
	for i, tr := range a.Tracks {
		if tr.Number != i+1 || tr.Disc != 1 || !tr.Available || tr.Favorite || tr.Album.Id != albumA || tr.Album.Cover == nil ||
			*tr.Album.Cover != *a.Cover || tr.Format.Codec != api.AudioFormatCodecFlac || tr.ReplayGain == nil {
			t.Errorf("track %d of A: %+v", i, tr)
		}
		duration += *tr.DurationMs
	}
	if a.DurationMs != duration || !a.Tracks[0].HasLyrics || a.Tracks[1].HasLyrics {
		t.Fatalf("A: duration %d of %d, lyrics %v %v", a.DurationMs, duration, a.Tracks[0].HasLyrics, a.Tracks[1].HasLyrics)
	}

	// E: two discs, its tracks by disc and number, no cover.
	e := decode[api.AlbumDetail](t, w.get("/albums/"+albumE, w.anna))
	var places [][2]int
	for _, tr := range e.Tracks {
		places = append(places, [2]int{tr.Disc, tr.Number})
	}
	if !slices.Equal(places, [][2]int{{1, 1}, {1, 2}, {2, 1}}) || e.DiscCount != 2 || e.Cover != nil || e.Artist.Name != "Écho Café" {
		t.Fatalf("E: %v, %d discs, cover %v, artist %q", places, e.DiscCount, e.Cover, e.Artist.Name)
	}

	// The favorites are those of the user who asks.
	w.favorite(w.anna, e.Tracks[1].Id)
	for who, want := range map[*account]bool{w.anna: true, w.bob: false} {
		e := decode[api.AlbumDetail](t, w.get("/albums/"+albumE, who))
		if e.Tracks[1].Favorite != want || e.Tracks[0].Favorite || e.Tracks[2].Favorite {
			t.Errorf("as %s: favorites %v %v %v", who.name, e.Tracks[0].Favorite, e.Tracks[1].Favorite, e.Tracks[2].Favorite)
		}
	}

	wantCode(t, "no such album", w.get("/albums/"+someID, w.anna), http.StatusNotFound, catalog.CodeAlbumNotFound)
	w.unavailable(albumB)
	wantCode(t, "an album that is not available", w.get("/albums/"+albumB, w.anna), http.StatusNotFound,
		catalog.CodeAlbumNotFound)
}

func TestGetTrack(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	b := decode[api.AlbumDetail](t, w.get("/albums/"+albumB, w.anna))
	first := b.Tracks[0]
	w.favorite(w.bob, first.Id)

	tr := decode[api.Track](t, w.get("/tracks/"+first.Id, w.anna))
	if !reflect.DeepEqual(tr, first) || tr.Album.Id != albumB || tr.Format.Codec != api.AudioFormatCodecMp3 || !tr.Available || tr.Favorite {
		t.Fatalf("the track: %+v, in its album %+v", tr, first)
	}
	if tr := decode[api.Track](t, w.get("/tracks/"+first.Id, w.bob)); !tr.Favorite {
		t.Fatal("a favorite of bob is not one for bob")
	}

	// Gone: the last data known, its album included.
	w.unavailable(albumB)
	gone := decode[api.Track](t, w.get("/tracks/"+first.Id, w.anna))
	if gone.Available || gone.Title != first.Title || gone.Album.Id != albumB || gone.Album.Title != b.Title ||
		gone.Album.Artist != b.Artist || !reflect.DeepEqual(gone.Format, first.Format) {
		t.Fatalf("an unavailable track: %+v", gone)
	}
	wantCode(t, "no such track", w.get("/tracks/"+someID, w.anna), http.StatusNotFound, catalog.CodeTrackNotFound)
}

// The ids of the entry that catalogEntry writes.
const (
	entryArtist = "0199a5c0-0000-5000-8000-0000000000a1"
	entryAlbum  = "0199a5c0-0000-7000-8000-0000000000b1"
	entryTrack  = "0199a5c0-0000-7000-8000-0000000000c1"
)

// catalogEntry writes one available artist, album and track, as the indexer
// would, without the tools: what an operation of the catalog needs to
// succeed. It can be written again.
func (w *world) catalogEntry() {
	w.t.Helper()
	ctx := w.t.Context()
	err := w.store.WithWriteTx(ctx, func(q *store.Queries) error {
		key := names.SortKey("Artist")
		if err := q.UpsertArtist(ctx, store.UpsertArtistParams{ID: entryArtist, Name: "Artist", SortKey: key}); err != nil {
			return err
		}
		if err := q.UpsertAlbum(ctx, store.UpsertAlbumParams{ID: entryAlbum, ArtistID: entryArtist, ArtistKey: key,
			Title: "Album", TitleKey: names.SortKey("Album"), YearKey: 10000, RelPath: "Artist/Album", AlbumRevision: 1,
			RenderVersion: "r", ReceiptHash: "h", FirstSeenAt: 1, UpdatedAt: 1}); err != nil {
			return err
		}
		if err := q.UpsertTrack(ctx, store.UpsertTrackParams{ID: entryTrack, AlbumID: entryAlbum, Fingerprint: "f", FpVersion: "v",
			Occurrence: 1, Disc: 1, No: 1, Title: "Track", Artist: "Artist", RelPath: "01 - Track.flac", FileSize: 1, FileMtimeNs: 1,
			FileSha256: "s", Codec: "flac", SampleRate: 44100, Channels: 2, UpdatedAt: 1}); err != nil {
			return err
		}
		return q.UpdateAlbumCounters(ctx, entryAlbum)
	})
	if err != nil {
		w.t.Fatal(err)
	}
}
