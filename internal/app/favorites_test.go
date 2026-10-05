package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"vibrance/internal/api"
	"vibrance/internal/catalog"
	"vibrance/internal/store"
)

// The operations of the favorites (DESIGN.md §8.3, step S18) over the API,
// on the fixture library indexed by the real indexer with the pinned tools.
// The order and the pagination themselves are proved in internal/catalog on
// random favorites; here every answer is also checked against the
// specification (I10).

// tick gives w a clock that is a millisecond later at every reading, from
// a fixed moment on: each favorite has a moment of its own, known to the
// test.
func (w *world) tick() {
	var n atomic.Int64
	w.s.catalog.Store(catalog.New(w.store, func() time.Time { return favoritesEpoch.Add(time.Duration(n.Add(1)) * time.Millisecond) }))
}

var favoritesEpoch = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// favorites reads every page of the favorites of a, limit at a time.
func (w *world) favorites(a *account, limit string) []api.Favorite {
	w.t.Helper()
	var all []api.Favorite
	path := "/me/favorites/tracks?limit=" + limit
	for range 100 {
		rec := w.get(path, a)
		wantStatus(w.t, "GET "+path, rec, http.StatusOK)
		page := decode[api.FavoriteList](w.t, rec)
		all = append(all, page.Favorites...)
		if page.Next == nil {
			return all
		}
		path = "/me/favorites/tracks?limit=" + limit + "&after=" + url.QueryEscape(*page.Next)
	}
	w.t.Fatal("the favorites have no last page")
	return nil
}

func favoriteIDs(favs []api.Favorite) []string {
	ids := make([]string, 0, len(favs))
	for _, f := range favs {
		ids = append(ids, f.Track.Id)
	}
	return ids
}

// setFavorite adds (PUT) or removes (DELETE) a favorite of a and wants 204
// without a body.
func (w *world) setFavorite(method string, a *account, trackID string) {
	w.t.Helper()
	rec := w.do(method, "/me/favorites/tracks/"+trackID, nil, w.as(a))
	wantStatus(w.t, method+" a favorite", rec, http.StatusNoContent)
	if rec.Body.Len() != 0 {
		w.t.Fatalf("%s a favorite: a 204 with the body %q", method, rec.Body)
	}
}

// fixtureTracks are the tracks of every album of the fixture library, as a
// reads them in their albums.
func (w *world) fixtureTracks(a *account) []api.Track {
	w.t.Helper()
	var tracks []api.Track
	for _, album := range decode[api.AlbumList](w.t, w.get("/albums?limit=200", a)).Albums {
		tracks = append(tracks, decode[api.AlbumDetail](w.t, w.get("/albums/"+album.Id, a)).Tracks...)
	}
	if len(tracks) != 14 {
		w.t.Fatalf("%d tracks in the albums of the fixture library, want 14", len(tracks))
	}
	return tracks
}

// wantFavoriteEverywhere checks the acceptance of step S18: every answer
// that holds a Track says the same of it for a, a favorite if and only if
// it is one of want. The answers are those of getAlbum, getTrack, search,
// listPlaylistItems (step S19) and listFavoriteTracks; every track of the
// library is seen in each of the first four, and the list of the favorites
// is exactly want.
func (w *world) wantFavoriteEverywhere(where string, a *account, want ...string) {
	w.t.Helper()
	check := func(source string, tr api.Track) {
		w.t.Helper()
		if tr.Favorite != slices.Contains(want, tr.Id) {
			w.t.Errorf("%s, as %s: %s says that %q is a favorite: %v", where, a.name, source, tr.Title, tr.Favorite)
		}
	}
	tracks := w.fixtureTracks(a)
	for _, tr := range tracks {
		check("getAlbum", tr)
		check("getTrack", decode[api.Track](w.t, w.get("/tracks/"+tr.Id, a)))
	}
	found := map[string]bool{}
	for _, q := range []string{"one", "two", "light", "wave", "third", "same"} {
		for _, tr := range w.search("types=track&limit=50&q="+q, a).Tracks {
			check("search", tr)
			found[tr.Id] = true
		}
	}
	if len(found) != len(tracks) {
		w.t.Fatalf("%s: the searches found %d of the %d tracks", where, len(found), len(tracks))
	}
	// A playlist of a with every track of the library, for the time of the
	// check, made with a clock of its own: the tests count the readings of
	// the clock of the server.
	svc, ctx := catalog.New(w.store, time.Now), w.t.Context()
	p, err := svc.CreatePlaylist(ctx, a.id, "Every track", "")
	if err != nil {
		w.t.Fatal(err)
	}
	ids := make([]string, 0, len(tracks))
	for _, tr := range tracks {
		ids = append(ids, tr.Id)
	}
	if _, _, err := svc.AddPlaylistItems(ctx, a.id, p.ID, ids, nil, nil); err != nil {
		w.t.Fatal(err)
	}
	items := w.playlistItems(a, p.ID, "5")
	if len(items) != len(tracks) {
		w.t.Fatalf("%s: the playlist lists %d of the %d tracks", where, len(items), len(tracks))
	}
	for _, it := range items {
		check("listPlaylistItems", it.Track)
	}
	if err := svc.DeletePlaylist(ctx, a.id, p.ID, nil); err != nil {
		w.t.Fatal(err)
	}
	listed := w.favorites(a, "3")
	for _, f := range listed {
		check("listFavoriteTracks", f.Track)
	}
	got := favoriteIDs(listed)
	slices.Sort(got)
	if sorted := slices.Sorted(slices.Values(want)); !slices.Equal(got, sorted) {
		w.t.Fatalf("%s, as %s: the favorites are %v, want %v", where, a.name, got, sorted)
	}
}

func TestFavorites(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.tick()
	rec := w.get("/me/favorites/tracks", w.anna)
	if strings.TrimSpace(rec.Body.String()) != `{"favorites":[],"next":null}` || rec.Code != http.StatusOK {
		t.Fatalf("no favorites: %d %s", rec.Code, rec.Body)
	}
	w.indexFixture()
	a := decode[api.AlbumDetail](t, w.get("/albums/"+albumA, w.anna)).Tracks
	e := decode[api.AlbumDetail](t, w.get("/albums/"+albumE, w.anna)).Tracks
	w.wantFavoriteEverywhere("at first", w.anna)

	// Adding is idempotent: the favorite keeps its moment.
	w.setFavorite("PUT", w.anna, a[0].Id)
	first := w.favorites(w.anna, "50")
	if len(first) != 1 || first[0].Track.Id != a[0].Id || !first[0].Track.Favorite || !first[0].Track.Available ||
		!parseTime(t, first[0].FavoritedAt).Equal(favoritesEpoch.Add(time.Millisecond)) {
		t.Fatalf("one favorite: %+v", first)
	}
	// The track of the list is the track of everywhere else.
	asTrack := decode[api.Track](t, w.get("/tracks/"+a[0].Id, w.anna))
	if got, want := mustJSON(t, first[0].Track), mustJSON(t, asTrack); got != want {
		t.Fatalf("the track of a favorite:\n%s\nthe track itself:\n%s", got, want)
	}
	w.setFavorite("PUT", w.anna, a[0].Id)
	if again := w.favorites(w.anna, "50"); len(again) != 1 || again[0].FavoritedAt != first[0].FavoritedAt {
		t.Fatalf("added again: %+v, was %+v", again, first)
	}

	// The most recent first, whatever the limit of the pages.
	for _, id := range []string{e[2].Id, a[2].Id, e[0].Id} {
		w.setFavorite("PUT", w.anna, id)
	}
	want := []string{e[0].Id, a[2].Id, e[2].Id, a[0].Id}
	for _, limit := range []string{"1", "2", "3", "4", "200"} {
		if got := favoriteIDs(w.favorites(w.anna, limit)); !slices.Equal(got, want) {
			t.Fatalf("limit %s: the favorites are %v, want %v", limit, got, want)
		}
	}
	page := decode[api.FavoriteList](t, w.get("/me/favorites/tracks?limit=4", w.anna))
	if page.Next != nil || len(page.Favorites) != 4 {
		t.Fatalf("a last page that is full: next %v, %d favorites", page.Next, len(page.Favorites))
	}
	w.wantFavoriteEverywhere("four favorites", w.anna, want...)

	// The favorites of bob are his own: he sees none of anna's, and what he
	// adds and removes changes nothing of hers.
	w.wantFavoriteEverywhere("the other user", w.bob)
	w.setFavorite("PUT", w.bob, a[0].Id)
	w.setFavorite("PUT", w.bob, a[1].Id)
	w.wantFavoriteEverywhere("the other user, with two", w.bob, a[0].Id, a[1].Id)
	w.wantFavoriteEverywhere("after the other added", w.anna, want...)
	w.setFavorite("DELETE", w.bob, a[0].Id)
	w.setFavorite("DELETE", w.bob, e[0].Id) // a favorite of anna, not of bob
	w.wantFavoriteEverywhere("the other user, with one", w.bob, a[1].Id)
	w.wantFavoriteEverywhere("after the other removed", w.anna, want...)
	if anna := w.favorites(w.anna, "50"); anna[3].FavoritedAt != first[0].FavoritedAt {
		t.Fatalf("the favorite of anna changed its moment: %+v", anna[3])
	}
	// An admin has favorites like anyone, and sees those of nobody else.
	w.wantFavoriteEverywhere("the admin", w.admin)

	// Removing is idempotent.
	w.setFavorite("DELETE", w.anna, a[2].Id)
	w.setFavorite("DELETE", w.anna, a[2].Id)
	want = []string{e[0].Id, e[2].Id, a[0].Id}
	w.wantFavoriteEverywhere("one removed", w.anna, want...)

	// No such track, and an id that is not one.
	for _, method := range []string{"PUT", "DELETE"} {
		wantCode(t, method+" no track", w.do(method, "/me/favorites/tracks/"+someID, nil, w.as(w.anna)), http.StatusNotFound,
			catalog.CodeTrackNotFound)
		// An album is not a track.
		wantCode(t, method+" an album", w.do(method, "/me/favorites/tracks/"+albumA, nil, w.as(w.anna)), http.StatusNotFound,
			catalog.CodeTrackNotFound)
		wantCode(t, method+" not an id", w.do(method, "/me/favorites/tracks/first", nil, w.as(w.anna)), http.StatusBadRequest,
			"invalid_request")
	}
	if got := favoriteIDs(w.favorites(w.anna, "50")); !slices.Equal(got, want) {
		t.Fatalf("after the refusals: %v, want %v", got, want)
	}

	// Cursors that are not of this list.
	albums := decode[api.AlbumList](t, w.get("/albums?sort=added&order=desc&limit=1", w.anna)).Next
	mine := decode[api.FavoriteList](t, w.get("/me/favorites/tracks?limit=1", w.anna)).Next
	if albums == nil || mine == nil {
		t.Fatal("no cursor")
	}
	for _, path := range []string{
		"/me/favorites/tracks?after=",
		"/me/favorites/tracks?after=x",
		"/me/favorites/tracks?after=" + url.QueryEscape(*albums),
		"/albums?sort=added&order=desc&after=" + url.QueryEscape(*mine),
	} {
		wantCode(t, path, w.get(path, w.anna), http.StatusBadRequest, "invalid_cursor")
	}
	for _, path := range []string{"/me/favorites/tracks?limit=0", "/me/favorites/tracks?limit=201"} {
		wantCode(t, path, w.get(path, w.anna), http.StatusBadRequest, "invalid_request")
	}

	// A track that is no longer available stays in the list, as it was last
	// known, and can still be added and removed.
	w.unavailable(albumE)
	gone := w.favorites(w.anna, "50")
	if !slices.Equal(favoriteIDs(gone), want) {
		t.Fatalf("after an album left: %v, want %v", favoriteIDs(gone), want)
	}
	for i, f := range gone {
		if f.Track.Available != (i == 2) || !f.Track.Favorite {
			t.Errorf("favorite %d after its album left: %+v", i, f.Track)
		}
	}
	if tr := gone[0].Track; tr.Title != e[0].Title || tr.Album.Id != albumE || tr.Album.Title != e[0].Album.Title ||
		tr.Album.Artist != e[0].Album.Artist || mustJSON(t, tr.Format) != mustJSON(t, e[0].Format) {
		t.Fatalf("an unavailable favorite: %+v, was %+v", tr, e[0])
	}
	w.setFavorite("PUT", w.anna, e[1].Id)
	w.setFavorite("DELETE", w.anna, e[0].Id)
	if got, want := favoriteIDs(w.favorites(w.anna, "50")), []string{e[1].Id, e[2].Id, a[0].Id}; !slices.Equal(got, want) {
		t.Fatalf("unavailable tracks added and removed: %v, want %v", got, want)
	}
	if tr := decode[api.Track](t, w.get("/tracks/"+e[1].Id, w.anna)); tr.Available || !tr.Favorite {
		t.Fatalf("an unavailable favorite, read as a track: %+v", tr)
	}
}

// favoriteRows counts the rows of the favorites of the account with that id.
func (w *world) favoriteRows(userID string) int {
	w.t.Helper()
	var n int
	err := w.store.Read(w.t.Context(), func(q *store.Queries) error {
		return q.Conn().QueryRowContext(w.t.Context(), `SELECT count(*) FROM favorites WHERE user_id = ?`, userID).Scan(&n)
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return n
}

// §7.5: deleting an account deletes its favorites, and no one else's. The
// tracks stay.
func TestDeleteUserDeletesItsFavorites(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.catalogEntry()
	w.setFavorite("PUT", w.anna, entryTrack)
	w.setFavorite("PUT", w.bob, entryTrack)
	if anna, bob := w.favoriteRows(w.anna.id), w.favoriteRows(w.bob.id); anna != 1 || bob != 1 {
		t.Fatalf("%d and %d rows before the deletion", anna, bob)
	}
	wantStatus(t, "deleteUser", w.do("DELETE", "/admin/users/"+w.anna.id, nil, w.as(w.admin)), http.StatusNoContent)
	if anna, bob := w.favoriteRows(w.anna.id), w.favoriteRows(w.bob.id); anna != 0 || bob != 1 {
		t.Fatalf("%d rows of the deleted account and %d of the other, want 0 and 1", anna, bob)
	}
	if got := favoriteIDs(w.favorites(w.bob, "50")); !slices.Equal(got, []string{entryTrack}) {
		t.Fatalf("the favorites of the other: %v", got)
	}
	if tr := decode[api.Track](t, w.get("/tracks/"+entryTrack, w.bob)); !tr.Favorite || !tr.Available {
		t.Fatalf("the track: %+v", tr)
	}
	// An account with the same name is another account: it has no favorites.
	rec := w.do("POST", "/admin/users", map[string]any{"username": "anna", "password": w.anna.password, "role": "user"}, w.as(w.admin))
	wantStatus(t, "anna again", rec, http.StatusCreated)
	w.anna.id = decode[api.User](t, rec).Id
	if got := w.favorites(w.anna, "50"); len(got) != 0 {
		t.Fatalf("the favorites of a new account: %+v", got)
	}
}

// §14 S18, with the real server, scanner and tools: a favorite stays on its
// track when MusicLib edits the track (another title, the same audio), when
// the album leaves the library, where it is listed as not available, and
// when the album comes back.
func TestFavoriteFollowsItsTrack(t *testing.T) {
	musiclib := fixtureLibrary(t)
	folderB := filepath.Join(musiclib, "library", "Bravo Tones", "Beta MP3")
	r := startServer(t, func(s *server) { s.musiclibDir = musiclib })
	waitReady(t, "http://"+r.addr)
	waitScanned(t, r)
	c := newClient(t, r)
	read := func(path string, v any) {
		t.Helper()
		resp, body := c.get(path)
		if resp.StatusCode != http.StatusOK || json.Unmarshal(body, v) != nil {
			t.Fatalf("GET %s: %d %s", path, resp.StatusCode, body)
		}
	}
	favorites := func() []api.Favorite {
		t.Helper()
		var list api.FavoriteList
		read("/me/favorites/tracks", &list)
		if list.Next != nil {
			t.Fatalf("a second page: %+v", list)
		}
		return list.Favorites
	}
	var album api.AlbumDetail
	read("/albums/"+albumB, &album)
	one, two := album.Tracks[0], album.Tracks[1]
	for _, id := range []string{one.Id, two.Id} {
		if resp, body := c.send(http.MethodPut, "/me/favorites/tracks/"+id); resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("PUT a favorite: %d %s", resp.StatusCode, body)
		}
		// The clock is the real one: the second favorite is later than the
		// first by more than its millisecond.
		time.Sleep(5 * time.Millisecond)
	}
	before := favorites()
	if len(before) != 2 || before[0].Track.Id != two.Id || before[1].Track.Id != one.Id || before[1].Track.Title != "One" {
		t.Fatalf("the favorites: %+v", before)
	}
	// What the favorite must show after each change: the same track, since
	// the same moment.
	wantFavorites := func(where, title string, available bool) {
		t.Helper()
		got := favorites()
		if len(got) != 2 || got[0].Track.Id != two.Id || got[1].Track.Id != one.Id || got[0].FavoritedAt != before[0].FavoritedAt ||
			got[1].FavoritedAt != before[1].FavoritedAt {
			t.Fatalf("%s: the favorites are %+v, were %+v", where, got, before)
		}
		for _, f := range got {
			if f.Track.Available != available || !f.Track.Favorite || f.Track.Album.Id != albumB {
				t.Fatalf("%s: %+v", where, f.Track)
			}
		}
		var tr api.Track
		read("/tracks/"+one.Id, &tr)
		if got[1].Track.Title != title || tr.Title != title || !tr.Favorite || tr.Available != available {
			t.Fatalf("%s: the favorite shows %q and the track %+v, want the title %q", where, got[1].Track.Title, tr, title)
		}
	}

	// MusicLib edits the title: the favorite is on the same track, with the
	// new title.
	retag(t, filepath.Join(folderB, "01 - One.mp3"), "Uno Edited")
	rerender(t, folderB)
	rescan(t, r)
	wantFavorites("after the edit", "Uno Edited", true)

	// The album leaves: its tracks stay favorites, not available.
	away := filepath.Join(t.TempDir(), "Beta MP3")
	if err := os.Rename(folderB, away); err != nil {
		t.Fatal(err)
	}
	rescan(t, r)
	wantFavorites("after the album left", "Uno Edited", false)
	if resp, body := c.get("/tracks/" + one.Id + "/audio"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the audio of an unavailable favorite: %d %s", resp.StatusCode, body)
	}

	// And comes back: the same favorites, available again.
	if err := os.Rename(away, folderB); err != nil {
		t.Fatal(err)
	}
	rescan(t, r)
	wantFavorites("after the album came back", "Uno Edited", true)

	// Removed with the real server too.
	if resp, body := c.send(http.MethodDelete, "/me/favorites/tracks/"+one.Id); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE a favorite: %d %s", resp.StatusCode, body)
	}
	if got := favorites(); len(got) != 1 || got[0].Track.Id != two.Id {
		t.Fatalf("after the removal: %+v", got)
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}
