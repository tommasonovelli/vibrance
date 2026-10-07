package app

import (
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"vibrance/internal/api"
	"vibrance/internal/auth"
	"vibrance/internal/catalog"
)

// The operations of step W3 (docs/proposals/web-client-api.md B1 to B4)
// over the API, on the fixture library indexed by the real indexer: the
// tracks chosen at random, several favorites at once, signing out
// everywhere else, and the playlists that hold a track. What each one
// chooses, keeps and refuses is proved in internal/catalog and
// internal/auth; here every answer is also checked against the
// specification (I10).

// randomTracks asks a for tracks chosen at random, with the query.
func (w *world) randomTracks(a *account, query string) []api.Track {
	w.t.Helper()
	rec := w.get("/tracks/random"+query, a)
	wantStatus(w.t, "GET /tracks/random"+query, rec, http.StatusOK)
	return decode[api.RandomTrackList](w.t, rec).Tracks
}

func TestRandomTracksOverTheAPI(t *testing.T) {
	w := newWorld(t, apiOrigin)
	if got := w.randomTracks(w.anna, ""); len(got) != 0 {
		t.Fatalf("an empty index chose %d tracks", len(got))
	}
	w.indexFixture()
	// The tracks of the album B are not available, and never chosen.
	var tracks []api.Track
	for _, tr := range w.fixtureTracks(w.anna) {
		if tr.Album.Id != albumB {
			tracks = append(tracks, tr)
		}
	}
	w.unavailable(albumB)
	byID := map[string]api.Track{}
	for _, tr := range tracks {
		byID[tr.Id] = tr
	}
	w.favorite(w.anna, tracks[0].Id)
	tracks[0].Favorite = true
	byID[tracks[0].Id] = tracks[0]

	// /tracks/random is this operation, not GET /tracks/{id} with an id
	// that is not one (400 invalid_request): the more specific pattern
	// wins.
	all := w.randomTracks(w.anna, "?limit=200")
	if len(all) != len(tracks) {
		t.Fatalf("a limit over the tracks chose %d of the %d available tracks", len(all), len(tracks))
	}
	seen := map[string]bool{}
	for _, tr := range all {
		if seen[tr.Id] {
			t.Fatalf("%s twice in one answer", tr.Id)
		}
		seen[tr.Id] = true
		// The track is the one the album shows, favorite included: none of
		// the album that is not available.
		if want, ok := byID[tr.Id]; !ok || !slices.Equal([]string{want.Title, want.Album.Id}, []string{tr.Title, tr.Album.Id}) ||
			want.Favorite != tr.Favorite || !tr.Available {
			t.Fatalf("a track chosen at random is not one of the available ones: %+v", tr)
		}
	}
	if got := w.randomTracks(w.anna, ""); len(got) != len(tracks) {
		t.Fatalf("the default limit, 50, chose %d of the %d tracks", len(got), len(tracks))
	}
	if got := w.randomTracks(w.anna, "?limit=3"); len(got) != 3 {
		t.Fatalf("a limit of 3 chose %d tracks", len(got))
	}

	// The filter by the artist of the album.
	artists := map[string][]string{}
	for _, tr := range tracks {
		artists[tr.Album.Artist.Id] = append(artists[tr.Album.Artist.Id], tr.Id)
	}
	for artist, want := range artists {
		var got []string
		for _, tr := range w.randomTracks(w.anna, "?limit=200&artist="+artist) {
			got = append(got, tr.Id)
		}
		if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
			t.Fatalf("the tracks of the artist %s: %v, want %v", artist, got, want)
		}
	}
	if got := w.randomTracks(w.anna, "?artist="+someID); len(got) != 0 {
		t.Fatalf("an id that is no artist's chose %d tracks", len(got))
	}

	// What the specification refuses.
	for _, query := range []string{"?limit=0", "?limit=201", "?limit=x", "?artist=x", "?artist=" + strings.ToUpper(someID)} {
		wantCode(t, query, w.get("/tracks/random"+query, w.anna), http.StatusBadRequest, "invalid_request")
	}
}

func TestAddFavoriteTracks(t *testing.T) {
	w := newWorld(t, apiOrigin)
	// A clock the test moves: the new favorites of a request are dated
	// now, now-1 ms and so on, which must not reach the favorite of before.
	var now atomic.Int64
	now.Store(favoritesEpoch.UnixMilli())
	w.s.catalog.Store(catalog.New(w.store, func() time.Time { return time.UnixMilli(now.Load()) }))
	w.indexFixture()
	tracks := w.fixtureTracks(w.anna)
	ids := func(tr ...api.Track) []string {
		out := make([]string, 0, len(tr))
		for _, t := range tr {
			out = append(out, t.Id)
		}
		return out
	}
	add := func(a *account, trackIDs []string) {
		t.Helper()
		rec := w.do(http.MethodPost, "/me/favorites/tracks", map[string]any{"track_ids": trackIDs}, w.as(a))
		wantStatus(t, "POST /me/favorites/tracks", rec, http.StatusNoContent)
		if rec.Body.Len() != 0 {
			t.Fatalf("a 204 with the body %q", rec.Body)
		}
	}

	// A favorite of before keeps its place; the album, in its order, comes
	// first, and a repeated id counts once.
	w.setFavorite(http.MethodPut, w.anna, tracks[5].Id)
	now.Add(1000)
	album := ids(tracks[0], tracks[1], tracks[2])
	add(w.anna, append(slices.Clone(album), tracks[1].Id, tracks[5].Id))
	want := append(slices.Clone(album), tracks[5].Id)
	if got := favoriteIDs(w.favorites(w.anna, "2")); !slices.Equal(got, want) {
		t.Fatalf("the favorites:\n got %v\nwant %v", got, want)
	}
	favs := w.favorites(w.anna, "50")
	for i := 1; i < 3; i++ {
		if favs[i-1].FavoritedAt <= favs[i].FavoritedAt {
			t.Fatalf("the favorites of one request are not one millisecond apart, the first the most recent: %v", favs)
		}
	}
	// Again: nothing changes.
	add(w.anna, album)
	if got := w.favorites(w.anna, "50"); !slices.Equal(favoriteIDs(got), want) || got[0].FavoritedAt != favs[0].FavoritedAt {
		t.Fatalf("the same request again changed the favorites: %v", got)
	}
	w.wantFavoriteEverywhere("after POST /me/favorites/tracks", w.anna, want...)
	w.wantFavoriteEverywhere("another user", w.bob)

	// Unknown tracks: 422 unknown_track, the ids once each, and nothing
	// changes.
	rec := w.do(http.MethodPost, "/me/favorites/tracks",
		map[string]any{"track_ids": []string{tracks[9].Id, someID, someID, entryTrack}}, w.as(w.anna))
	if got := wantRefusal(t, "unknown tracks", rec, http.StatusUnprocessableEntity, catalog.CodeUnknownTrack); !slices.Equal(got, []string{someID, entryTrack}) {
		t.Fatalf("the unknown tracks listed: %v", got)
	}
	if got := favoriteIDs(w.favorites(w.anna, "50")); !slices.Equal(got, want) {
		t.Fatalf("a refused request changed the favorites: %v", got)
	}

	// The body the specification refuses: 400, and 403 without the header.
	tooMany := make([]string, 1001)
	for i := range tooMany {
		tooMany[i] = tracks[0].Id
	}
	for where, body := range map[string]any{
		"no track":            map[string]any{"track_ids": []string{}},
		"1001 tracks":         map[string]any{"track_ids": tooMany},
		"an id that is not":   map[string]any{"track_ids": []string{"x"}},
		"no track_ids":        map[string]any{},
		"an unknown key":      map[string]any{"track_ids": []string{tracks[0].Id}, "position": nil},
		"track_ids not lists": map[string]any{"track_ids": tracks[0].Id},
	} {
		wantCode(t, where, w.do(http.MethodPost, "/me/favorites/tracks", body, w.as(w.anna)), http.StatusBadRequest, "invalid_request")
	}
	req := w.request(http.MethodPost, "/me/favorites/tracks", map[string]any{"track_ids": []string{tracks[0].Id}})
	req.Header.Del("X-Vibrance-Request")
	w.as(w.anna)(req)
	wantCode(t, "no X-Vibrance-Request", send(w.s.http.Handler, req), http.StatusForbidden, "request_header_required")
	if got := favoriteIDs(w.favorites(w.anna, "50")); !slices.Equal(got, want) {
		t.Fatalf("a refused request changed the favorites: %v", got)
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	for _, kind := range []string{auth.KindCookie, auth.KindToken} {
		t.Run(kind, func(t *testing.T) {
			w := newWorld(t, apiOrigin)
			present := func(in auth.SignIn) credential {
				if in.Session.Kind == auth.KindCookie {
					return sessionCookie(in.Token)
				}
				return bearer(in.Token)
			}
			current := w.cookie(w.anna)
			if kind == auth.KindToken {
				current = w.token(w.anna)
			}
			others := []auth.SignIn{w.cookie(w.anna), w.token(w.anna), w.token(w.anna)}
			bob := []auth.SignIn{w.cookie(w.bob), w.token(w.bob)}
			admin := w.token(w.admin)

			for range 2 {
				rec := w.do(http.MethodDelete, "/me/sessions", nil, present(current))
				wantStatus(t, "DELETE /me/sessions", rec, http.StatusNoContent)
				for _, in := range others {
					wantCode(t, "another session of the user", w.do(http.MethodGet, "/me", nil, present(in)),
						http.StatusUnauthorized, auth.CodeLoginRequired)
				}
				for _, in := range append([]auth.SignIn{current, admin}, bob...) {
					wantStatus(t, "a session that stays", w.do(http.MethodGet, "/me", nil, present(in)), http.StatusOK)
				}
				list := decode[api.SessionList](t, w.do(http.MethodGet, "/me/sessions", nil, present(current))).Sessions
				if len(list) != 1 || list[0].Id != current.Session.ID || !list[0].Current {
					t.Fatalf("the sessions left: %+v", list)
				}
			}
			// Without the header, nothing is revoked.
			again := w.token(w.anna)
			req := w.request(http.MethodDelete, "/me/sessions", nil)
			req.Header.Del("X-Vibrance-Request")
			present(current)(req)
			wantCode(t, "no X-Vibrance-Request", send(w.s.http.Handler, req), http.StatusForbidden, "request_header_required")
			wantStatus(t, "a session after a refused request", w.do(http.MethodGet, "/me", nil, bearer(again.Token)), http.StatusOK)
		})
	}
}

func TestTrackPlaylists(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	tracks := w.fixtureTracks(w.anna)
	track := tracks[0].Id
	create := func(a *account, name string, items ...string) api.Playlist {
		t.Helper()
		rec := w.do(http.MethodPost, "/playlists", map[string]any{"name": name, "description": ""}, w.as(a))
		wantStatus(t, "POST /playlists", rec, http.StatusCreated)
		p := decode[api.Playlist](t, rec)
		if len(items) != 0 {
			rec = w.do(http.MethodPost, "/playlists/"+p.Id+"/items", map[string]any{"track_ids": items, "position": nil}, w.as(a))
			wantStatus(t, "POST /playlists/{id}/items", rec, http.StatusOK)
		}
		return p
	}
	list := func(a *account, id string) []api.PlaylistRef {
		t.Helper()
		rec := w.get("/tracks/"+id+"/playlists", a)
		wantStatus(t, "GET /tracks/{id}/playlists", rec, http.StatusOK)
		return decode[api.PlaylistRefList](t, rec).Playlists
	}
	ref := func(p api.Playlist) api.PlaylistRef { return api.PlaylistRef{Id: p.Id, Name: p.Name} }

	if got := list(w.anna, track); len(got) != 0 {
		t.Fatalf("no playlist yet: %+v", got)
	}
	first := create(w.anna, "First", track, tracks[1].Id)
	create(w.anna, "Without", tracks[1].Id)
	ofBob := create(w.bob, "Of bob", track)
	ofAdmin := create(w.admin, "Of the admin", track)
	second := create(w.anna, "Second", tracks[2].Id, track, track)

	// Each sees their own playlists with the track, and never another's:
	// not even an admin.
	for _, c := range []struct {
		who  *account
		want []api.PlaylistRef
	}{
		{w.anna, []api.PlaylistRef{ref(first), ref(second)}},
		{w.bob, []api.PlaylistRef{ref(ofBob)}},
		{w.admin, []api.PlaylistRef{ref(ofAdmin)}},
		{w.newAccount(auth.RoleUser), []api.PlaylistRef{}},
	} {
		if got := list(c.who, track); !slices.Equal(got, c.want) {
			t.Fatalf("the playlists of %s with the track:\n got %+v\nwant %+v", c.who.name, got, c.want)
		}
	}
	// A track that is not available is looked for all the same.
	w.unavailable(tracks[0].Album.Id)
	if got := list(w.anna, track); !slices.Equal(got, []api.PlaylistRef{ref(first), ref(second)}) {
		t.Fatalf("a track that is not available: %+v", got)
	}
	wantCode(t, "a track that does not exist", w.get("/tracks/"+someID+"/playlists", w.anna), http.StatusNotFound, catalog.CodeTrackNotFound)
	wantCode(t, "an id that is not one", w.get("/tracks/x/playlists", w.anna), http.StatusBadRequest, "invalid_request")
}
