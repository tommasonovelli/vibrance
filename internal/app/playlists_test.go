package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"vibrance/internal/api"
	"vibrance/internal/catalog"
	"vibrance/internal/store"
)

// The operations of the playlists (DESIGN.md §8.3, §8.6, step S19) over the
// API, on the fixture library indexed by the real indexer with the pinned
// tools. The order of the items after any sequence of changes, the limits
// and the concurrency are proved in internal/catalog; here every answer is
// also checked against the specification (I10).

// ifMatch presents a session and the entity tag a change was prepared on.
func ifMatch(c credential, tag string) credential {
	return func(r *http.Request) {
		c(r)
		r.Header.Set("If-Match", tag)
	}
}

// playlistWithItem makes a playlist of owner with one item, of the track of
// catalogEntry, and returns it with the id of the item.
func (w *world) playlistWithItem(owner *account) (catalog.Playlist, string) {
	w.t.Helper()
	w.catalogEntry()
	svc := w.s.catalog.Load()
	p, err := svc.CreatePlaylist(w.t.Context(), owner.id, "A playlist of "+owner.name, "")
	if err != nil {
		w.t.Fatal(err)
	}
	p, added, err := svc.AddPlaylistItems(w.t.Context(), owner.id, p.ID, []string{entryTrack}, nil, nil)
	if err != nil {
		w.t.Fatal(err)
	}
	return p, added[0].ItemID
}

// playlist reads a playlist of a, and checks that its ETag header is the
// etag of its body, the tag of its revision.
func (w *world) playlist(a *account, id string) api.Playlist {
	w.t.Helper()
	rec := w.get("/playlists/"+id, a)
	wantStatus(w.t, "getPlaylist", rec, http.StatusOK)
	p := decode[api.Playlist](w.t, rec)
	if want := fmt.Sprintf(`"playlist:%s:%d"`, id, p.Revision); p.Etag != want || rec.Header().Get("ETag") != want {
		w.t.Fatalf("the playlist has the etag %s and the header %s, want %s", p.Etag, rec.Header().Get("ETag"), want)
	}
	return p
}

// playlistItems reads every page of the items of a playlist of a, limit at
// a time, and checks the positions and the ETag of each page.
func (w *world) playlistItems(a *account, id, limit string) []api.PlaylistItem {
	w.t.Helper()
	var all []api.PlaylistItem
	etag := w.playlist(a, id).Etag
	path := "/playlists/" + id + "/items?limit=" + limit
	for range 100 {
		rec := w.get(path, a)
		wantStatus(w.t, "GET "+path, rec, http.StatusOK)
		if got := rec.Header().Get("ETag"); got != etag {
			w.t.Fatalf("GET %s: the ETag %s, want the one of the playlist, %s", path, got, etag)
		}
		page := decode[api.PlaylistItemList](w.t, rec)
		for _, it := range page.Items {
			if it.Position != len(all) {
				w.t.Fatalf("the item %d has the position %d", len(all), it.Position)
			}
			all = append(all, it)
		}
		if page.Next == nil {
			return all
		}
		path = "/playlists/" + id + "/items?limit=" + limit + "&after=" + url.QueryEscape(*page.Next)
	}
	w.t.Fatal("the items have no last page")
	return nil
}

func itemTracks(items []api.PlaylistItem) []string {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.Track.Id)
	}
	return ids
}

// wantRefusal checks an error of a playlist operation and returns the track
// ids of its details, nil when it has none.
func wantRefusal(t *testing.T, where string, rec *httptest.ResponseRecorder, status int, code string) []string {
	t.Helper()
	wantCode(t, where, rec, status, code)
	var ids []string
	if listed, ok := decode[api.Error](t, rec).Details["track_ids"].([]any); ok {
		for _, id := range listed {
			ids = append(ids, id.(string))
		}
	}
	return ids
}

func TestPlaylists(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	w.tick()
	anna := w.as(w.anna)
	tracks := w.fixtureTracks(w.anna)
	ids := make([]string, len(tracks))
	for i, tr := range tracks {
		ids[i] = tr.Id
	}

	// Nothing at first; a new playlist is empty, at its first revision.
	if list := decode[api.PlaylistList](t, w.get("/playlists", w.anna)); list.Playlists == nil || len(list.Playlists) != 0 {
		t.Fatalf("the playlists at first: %+v", list)
	}
	rec := w.do("POST", "/playlists", map[string]any{"name": "Sunday morning", "description": "slow"}, anna)
	wantStatus(t, "createPlaylist", rec, http.StatusCreated)
	p := decode[api.Playlist](t, rec)
	first := `"playlist:` + p.Id + `:1"`
	if p.Name != "Sunday morning" || p.Description != "slow" || p.ItemCount != 0 || p.DurationMs != 0 || p.Revision != 1 ||
		p.Etag != first || rec.Header().Get("ETag") != first || p.CreatedAt != p.UpdatedAt {
		t.Fatalf("created: %+v, ETag %s", p, rec.Header().Get("ETag"))
	}
	if got := w.playlist(w.anna, p.Id); got != p {
		t.Fatalf("read: %+v, want %+v", got, p)
	}
	if list := decode[api.PlaylistList](t, w.get("/playlists", w.anna)); len(list.Playlists) != 1 || list.Playlists[0] != p {
		t.Fatalf("listed: %+v", list)
	}
	items := "/playlists/" + p.Id + "/items"

	// Adding at the end is one request, without If-Match; a track may be
	// there twice.
	rec = w.do("POST", items, map[string]any{"track_ids": []string{ids[0], ids[1], ids[0]}, "position": nil}, anna)
	wantStatus(t, "addPlaylistItems at the end", rec, http.StatusOK)
	added := decode[api.AddPlaylistItemsResult](t, rec)
	if len(added.Added) != 3 || added.Playlist.ItemCount != 3 || added.Playlist.Revision != 2 ||
		added.Playlist.Etag != `"playlist:`+p.Id+`:2"` || added.Playlist.UpdatedAt == p.UpdatedAt {
		t.Fatalf("added: %+v", added)
	}
	for i, a := range added.Added {
		if a.Position != i || a.TrackId != []string{ids[0], ids[1], ids[0]}[i] || a.ItemId == "" {
			t.Fatalf("the added item %d: %+v", i, a)
		}
	}
	if added.Added[0].ItemId == added.Added[2].ItemId {
		t.Fatal("the same track twice is one item")
	}
	p = w.playlist(w.anna, p.Id)
	if p != added.Playlist {
		t.Fatalf("the playlist is %+v, the answer said %+v", p, added.Playlist)
	}

	// A position needs If-Match (428); an old revision is 412; neither
	// changes anything.
	insert := map[string]any{"track_ids": []string{ids[2], ids[3]}, "position": 1}
	wantRefusal(t, "a position without If-Match", w.do("POST", items, insert, anna), http.StatusPreconditionRequired, "precondition_required")
	for _, stale := range []string{first, "W/" + first, "*", `"playlist:` + p.Id + `:3"`, "playlist:" + p.Id + ":2"} {
		wantRefusal(t, "a position with the If-Match "+stale, w.do("POST", items, insert, ifMatch(anna, stale)),
			http.StatusPreconditionFailed, "precondition_failed")
		wantRefusal(t, "an append with the If-Match "+stale,
			w.do("POST", items, map[string]any{"track_ids": []string{ids[2]}, "position": nil}, ifMatch(anna, stale)),
			http.StatusPreconditionFailed, "precondition_failed")
	}
	if got := w.playlist(w.anna, p.Id); got != p {
		t.Fatalf("refused changes changed the playlist: %+v, want %+v", got, p)
	}
	if got := itemTracks(w.playlistItems(w.anna, p.Id, "50")); !slices.Equal(got, []string{ids[0], ids[1], ids[0]}) {
		t.Fatalf("refused changes changed the items: %v", got)
	}

	// With the tag of the revision, weak as a proxy may make it, the block
	// goes before the item at the position.
	rec = w.do("POST", items, insert, ifMatch(anna, "W/"+p.Etag))
	wantStatus(t, "addPlaylistItems at a position", rec, http.StatusOK)
	inserted := decode[api.AddPlaylistItemsResult](t, rec)
	if inserted.Playlist.Revision != 3 || inserted.Playlist.ItemCount != 5 || inserted.Added[0].Position != 1 || inserted.Added[1].Position != 2 {
		t.Fatalf("inserted: %+v", inserted)
	}
	p = inserted.Playlist
	want := []string{ids[0], ids[2], ids[3], ids[1], ids[0]}
	for _, limit := range []string{"1", "2", "50"} {
		if got := itemTracks(w.playlistItems(w.anna, p.Id, limit)); !slices.Equal(got, want) {
			t.Fatalf("limit %s: the items are %v, want %v", limit, got, want)
		}
	}

	// An item shows its track as every other answer does, favorite
	// included (step S18), and each user sees their own favorites.
	w.setFavorite("PUT", w.anna, ids[2])
	listed := w.playlistItems(w.anna, p.Id, "50")
	var duration int64
	for i, it := range listed {
		alone := decode[api.Track](t, w.get("/tracks/"+it.Track.Id, w.anna))
		if !reflect.DeepEqual(it.Track, alone) || it.Track.Favorite != (i == 1) || it.AddedAt == "" {
			t.Fatalf("the item %d shows %+v, the track alone is %+v", i, it.Track, alone)
		}
		duration += *it.Track.DurationMs
	}
	if p.DurationMs != duration || duration == 0 {
		t.Fatalf("duration_ms %d, the items add up to %d", p.DurationMs, duration)
	}
	if listed[0].Id != added.Added[0].ItemId || listed[1].Id != inserted.Added[0].ItemId || listed[4].Id != added.Added[2].ItemId {
		t.Fatalf("the ids of the items changed: %+v", listed)
	}
	if got := w.playlist(w.anna, p.Id); got != p {
		t.Fatalf("a favorite changed the playlist: %+v", got)
	}

	// A move needs If-Match too, and puts the item at its final position.
	move := items + "/" + listed[0].Id + "/move"
	wantRefusal(t, "a move without If-Match", w.do("POST", move, map[string]any{"position": 3}, anna),
		http.StatusPreconditionRequired, "precondition_required")
	wantRefusal(t, "a move with an old If-Match", w.do("POST", move, map[string]any{"position": 3}, ifMatch(anna, first)),
		http.StatusPreconditionFailed, "precondition_failed")
	for _, position := range []int{-1, 5, 1 << 40} {
		wantRefusal(t, fmt.Sprintf("a move to %d", position), w.do("POST", move, map[string]any{"position": position}, ifMatch(anna, p.Etag)),
			http.StatusUnprocessableEntity, "invalid_position")
	}
	rec = w.do("POST", move, map[string]any{"position": 3}, ifMatch(anna, p.Etag))
	wantStatus(t, "movePlaylistItem", rec, http.StatusOK)
	if p = decode[api.Playlist](t, rec); p.Revision != 4 || p.ItemCount != 5 {
		t.Fatalf("moved: %+v", p)
	}
	want = []string{ids[2], ids[3], ids[1], ids[0], ids[0]}
	listed = w.playlistItems(w.anna, p.Id, "2")
	if got := itemTracks(listed); !slices.Equal(got, want) || listed[3].Id != added.Added[0].ItemId {
		t.Fatalf("after the move the items are %v, want %v", got, want)
	}
	missing := "0199a5c0-0000-7000-8000-00000000dead"
	wantRefusal(t, "a move of no item", w.do("POST", items+"/"+missing+"/move", map[string]any{"position": 0}, ifMatch(anna, p.Etag)),
		http.StatusNotFound, "item_not_found")

	// Positions out of range, and tracks that cannot be added: all or
	// nothing.
	for _, position := range []int{-1, 6} {
		wantRefusal(t, fmt.Sprintf("an insert at %d", position),
			w.do("POST", items, map[string]any{"track_ids": []string{ids[4]}, "position": position}, ifMatch(anna, p.Etag)),
			http.StatusUnprocessableEntity, "invalid_position")
	}
	got := wantRefusal(t, "an unknown track",
		w.do("POST", items, map[string]any{"track_ids": []string{ids[4], missing, ids[5], missing}, "position": nil}, anna),
		http.StatusUnprocessableEntity, "unknown_track")
	if !slices.Equal(got, []string{missing}) {
		t.Fatalf("the details of unknown_track: %v", got)
	}
	if got := w.playlist(w.anna, p.Id); got != p {
		t.Fatalf("refused changes changed the playlist: %+v, want %+v", got, p)
	}

	// What the schema refuses is 400, before the operation.
	for where, body := range map[string]any{
		"no track":              map[string]any{"track_ids": []string{}, "position": nil},
		"1001 tracks":           map[string]any{"track_ids": slices.Repeat([]string{ids[0]}, 1001), "position": nil},
		"no position":           map[string]any{"track_ids": []string{ids[0]}},
		"a position as text":    map[string]any{"track_ids": []string{ids[0]}, "position": "0"},
		"an id that is no UUID": map[string]any{"track_ids": []string{"so-what"}, "position": nil},
		"an unknown key":        map[string]any{"track_ids": []string{ids[0]}, "position": nil, "at": 1},
	} {
		wantRefusal(t, where, w.do("POST", items, body, anna), http.StatusBadRequest, "invalid_request")
	}
	// 1000 tracks are one request.
	rec = w.do("POST", items, map[string]any{"track_ids": slices.Repeat([]string{ids[6]}, 1000), "position": 5}, ifMatch(anna, p.Etag))
	wantStatus(t, "1000 tracks", rec, http.StatusOK)
	if p = decode[api.AddPlaylistItemsResult](t, rec).Playlist; p.ItemCount != 1005 || p.Revision != 5 {
		t.Fatalf("after 1000 tracks: %+v", p)
	}
	if all := w.playlistItems(w.anna, p.Id, "200"); len(all) != 1005 {
		t.Fatalf("%d items listed", len(all))
	}

	// A cursor is one of this list.
	w.setFavorite("PUT", w.anna, ids[3])
	favorites := decode[api.FavoriteList](t, w.get("/me/favorites/tracks?limit=1", w.anna))
	if favorites.Next == nil {
		t.Fatal("the favorites have one page")
	}
	for _, after := range []string{"", "x", url.QueryEscape(*favorites.Next)} {
		wantRefusal(t, "the cursor "+after, w.get(items+"?after="+after, w.anna), http.StatusBadRequest, "invalid_cursor")
	}

	// A rename takes both fields, checks them, and gives a new revision.
	path := "/playlists/" + p.Id
	for where, body := range map[string]any{
		"no name":               map[string]any{"name": "", "description": ""},
		"a name of 201":         map[string]any{"name": strings.Repeat("é", 201), "description": ""},
		"a description of 2001": map[string]any{"name": "x", "description": strings.Repeat("d", 2001)},
		"U+0000 in the name":    map[string]any{"name": "a\x00b", "description": ""},
	} {
		wantRefusal(t, where+", updated", w.do("PUT", path, body, anna), http.StatusUnprocessableEntity, "invalid_request")
		wantRefusal(t, where+", created", w.do("POST", "/playlists", body, anna), http.StatusUnprocessableEntity, "invalid_request")
	}
	for where, body := range map[string]any{
		"no description": map[string]any{"name": "x"},
		"an unknown key": map[string]any{"name": "x", "description": "", "public": true},
		"a null name":    map[string]any{"name": nil, "description": ""},
	} {
		wantRefusal(t, where, w.do("PUT", path, body, anna), http.StatusBadRequest, "invalid_request")
	}
	wantRefusal(t, "a rename with an old If-Match", w.do("PUT", path, map[string]any{"name": "x", "description": ""}, ifMatch(anna, first)),
		http.StatusPreconditionFailed, "precondition_failed")
	if got := w.playlist(w.anna, p.Id); got != p {
		t.Fatalf("refused renames changed the playlist: %+v, want %+v", got, p)
	}
	rec = w.do("PUT", path, map[string]any{"name": strings.Repeat("é", 200), "description": "two\nlines"}, anna)
	wantStatus(t, "updatePlaylist", rec, http.StatusOK)
	if p = decode[api.Playlist](t, rec); p.Name != strings.Repeat("é", 200) || p.Description != "two\nlines" || p.Revision != 6 || p.ItemCount != 1005 {
		t.Fatalf("renamed: %+v", p)
	}
	rec = w.do("PUT", path, map[string]any{"name": "Sunday", "description": ""}, ifMatch(anna, p.Etag))
	wantStatus(t, "updatePlaylist with If-Match", rec, http.StatusOK)
	if p = decode[api.Playlist](t, rec); p.Name != "Sunday" || p.Revision != 7 || p != w.playlist(w.anna, p.Id) {
		t.Fatalf("renamed: %+v", p)
	}

	// An item is removed by its id, with or without If-Match, once.
	listed = w.playlistItems(w.anna, p.Id, "200")
	wantRefusal(t, "a removal with an old If-Match", w.do("DELETE", items+"/"+listed[0].Id, nil, ifMatch(anna, first)),
		http.StatusPreconditionFailed, "precondition_failed")
	rec = w.do("DELETE", items+"/"+listed[0].Id, nil, anna)
	wantStatus(t, "removePlaylistItem", rec, http.StatusOK)
	if p = decode[api.Playlist](t, rec); p.Revision != 8 || p.ItemCount != 1004 {
		t.Fatalf("removed: %+v", p)
	}
	wantRefusal(t, "the same removal again", w.do("DELETE", items+"/"+listed[0].Id, nil, anna), http.StatusNotFound, "item_not_found")
	rec = w.do("DELETE", items+"/"+listed[1].Id, nil, ifMatch(anna, p.Etag))
	wantStatus(t, "removePlaylistItem with If-Match", rec, http.StatusOK)
	if p = decode[api.Playlist](t, rec); p.Revision != 9 || p.ItemCount != 1003 {
		t.Fatalf("removed: %+v", p)
	}
	after := w.playlistItems(w.anna, p.Id, "200")
	if len(after) != 1003 || after[0].Id != listed[2].Id || after[0].Track.Id != ids[1] {
		t.Fatalf("after two removals the first item is %+v", after[0])
	}

	// Deleting: an old tag refuses, then the playlist and its items go.
	wantRefusal(t, "a deletion with an old If-Match", w.do("DELETE", path, nil, ifMatch(anna, first)),
		http.StatusPreconditionFailed, "precondition_failed")
	rec = w.do("DELETE", path, nil, ifMatch(anna, p.Etag))
	wantStatus(t, "deletePlaylist", rec, http.StatusNoContent)
	if rec.Body.Len() != 0 {
		t.Fatalf("a 204 with the body %q", rec.Body)
	}
	for where, rec := range map[string]*httptest.ResponseRecorder{
		"get":        w.get(path, w.anna),
		"list items": w.get(items, w.anna),
		"delete":     w.do("DELETE", path, nil, anna),
		"update":     w.do("PUT", path, map[string]any{"name": "x", "description": ""}, anna),
		"add":        w.do("POST", items, map[string]any{"track_ids": []string{ids[0]}, "position": nil}, anna),
		"remove":     w.do("DELETE", items+"/"+listed[2].Id, nil, anna),
		"move":       w.do("POST", items+"/"+listed[2].Id+"/move", map[string]any{"position": 0}, anna),
	} {
		wantRefusal(t, where+" of a deleted playlist", rec, http.StatusNotFound, "playlist_not_found")
	}
	if n := w.playlistRows(w.anna.id); n != [2]int{0, 0} {
		t.Fatalf("%v playlists and items left", n)
	}
}

// §8.1, erratum R2: every answer that carries one playlist has its entity
// tag in the ETag header, once, and it is the etag of the body: the tag of
// the revision the playlist has after the request. A client that changes a
// playlist reads its next If-Match where it reads any other.
func TestPlaylistAnswersCarryTheirETag(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.catalogEntry()
	anna := w.as(w.anna)
	revision := 0
	var id string
	// tagged checks the answer of an operation that made a revision, or
	// that read the last one, and returns the tag.
	tagged := func(where string, rec *httptest.ResponseRecorder, status int, changed bool, bodyTag func() (string, string)) string {
		t.Helper()
		wantStatus(t, where, rec, status)
		if changed {
			revision++
		}
		gotID, tag := bodyTag()
		if id == "" {
			id = gotID
		}
		want := fmt.Sprintf(`"playlist:%s:%d"`, id, revision)
		if headers := rec.Header().Values("ETag"); len(headers) != 1 || headers[0] != want || tag != want {
			t.Fatalf("%s: the ETag header is %q and the etag of the body %s, want both %s", where, headers, tag, want)
		}
		return want
	}
	playlist := func(rec *httptest.ResponseRecorder) func() (string, string) {
		return func() (string, string) {
			p := decode[api.Playlist](t, rec)
			return p.Id, p.Etag
		}
	}
	added := func(rec *httptest.ResponseRecorder) func() (string, string) {
		return func() (string, string) {
			p := decode[api.AddPlaylistItemsResult](t, rec).Playlist
			return p.Id, p.Etag
		}
	}

	rec := w.do("POST", "/playlists", map[string]any{"name": "tags", "description": ""}, anna)
	tag := tagged("createPlaylist", rec, http.StatusCreated, true, playlist(rec))
	path := "/playlists/" + id
	rec = w.do("GET", path, nil, anna)
	tagged("getPlaylist", rec, http.StatusOK, false, playlist(rec))

	rec = w.do("POST", path+"/items", map[string]any{"track_ids": []string{entryTrack, entryTrack}, "position": nil}, anna)
	tag = tagged("addPlaylistItems at the end", rec, http.StatusOK, true, added(rec))
	rec = w.do("POST", path+"/items", map[string]any{"track_ids": []string{entryTrack}, "position": 0}, ifMatch(anna, tag))
	tag = tagged("addPlaylistItems at a position", rec, http.StatusOK, true, added(rec))

	// The header of a change is the If-Match of the next one.
	rec = w.do("GET", path+"/items", nil, anna)
	wantStatus(t, "listPlaylistItems", rec, http.StatusOK)
	if got := rec.Header().Values("ETag"); len(got) != 1 || got[0] != tag {
		t.Fatalf("listPlaylistItems: the ETag header is %q, want %s", got, tag)
	}
	items := decode[api.PlaylistItemList](t, rec).Items
	if len(items) != 3 {
		t.Fatalf("%d items, want 3", len(items))
	}

	rec = w.do("PUT", path, map[string]any{"name": "tags", "description": "again"}, ifMatch(anna, rec.Header().Get("ETag")))
	tagged("updatePlaylist", rec, http.StatusOK, true, playlist(rec))
	rec = w.do("POST", path+"/items/"+items[0].Id+"/move", map[string]any{"position": 2}, ifMatch(anna, rec.Header().Get("ETag")))
	tagged("movePlaylistItem", rec, http.StatusOK, true, playlist(rec))
	rec = w.do("DELETE", path+"/items/"+items[1].Id, nil, ifMatch(anna, rec.Header().Get("ETag")))
	tagged("removePlaylistItem", rec, http.StatusOK, true, playlist(rec))
	rec = w.do("DELETE", path+"/items/"+items[2].Id, nil, anna)
	tag = tagged("removePlaylistItem without If-Match", rec, http.StatusOK, true, playlist(rec))
	if revision != 7 {
		t.Fatalf("%d revisions, want 7", revision)
	}

	// A change that is refused made no revision, and carries no tag: the
	// one the client holds is still the last it was given.
	stale := `"playlist:` + id + `:1"`
	for where, rec := range map[string]*httptest.ResponseRecorder{
		"an update with an old tag": w.do("PUT", path, map[string]any{"name": "x", "description": ""}, ifMatch(anna, stale)),
		"a move without a tag":      w.do("POST", path+"/items/"+items[0].Id+"/move", map[string]any{"position": 0}, anna),
		"an update not valid":       w.do("PUT", path, map[string]any{"name": "", "description": ""}, anna),
	} {
		if rec.Code < 400 || len(rec.Header().Values("ETag")) != 0 {
			t.Errorf("%s: status %d with the ETag header %q", where, rec.Code, rec.Header().Values("ETag"))
		}
	}
	rec = w.do("GET", path, nil, anna)
	if got := tagged("getPlaylist at the end", rec, http.StatusOK, false, playlist(rec)); got != tag {
		t.Fatalf("the playlist has the tag %s, the last change said %s", got, tag)
	}
}

// playlistRows counts the playlists of a user and their items in the
// database.
func (w *world) playlistRows(userID string) (n [2]int) {
	w.t.Helper()
	err := w.store.Read(w.t.Context(), func(q *store.Queries) error {
		return q.Conn().QueryRowContext(w.t.Context(), `SELECT (SELECT count(*) FROM playlists WHERE user_id = ?1),
			(SELECT count(*) FROM playlist_items JOIN playlists ON playlists.id = playlist_items.playlist_id
			 WHERE playlists.user_id = ?1)`, userID).Scan(&n[0], &n[1])
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return n
}

// §8.6, I6: a playlist is of its user. Another user, and an admin, get 404
// playlist_not_found from every operation, with and without If-Match, and
// change nothing; they do not see it in their list.
func TestPlaylistsOfAnotherUser(t *testing.T) {
	w := newWorld(t, apiOrigin)
	p, itemID := w.playlistWithItem(w.anna)
	mine, mineItem := w.playlistWithItem(w.bob)
	before := w.playlist(w.anna, p.ID)
	path := "/playlists/" + p.ID
	for _, other := range []*account{w.bob, w.admin} {
		for _, tag := range []string{"", p.ETag(), mine.ETag()} {
			c := w.as(other)
			if tag != "" {
				c = ifMatch(c, tag)
			}
			for where, rec := range map[string]*httptest.ResponseRecorder{
				"getPlaylist":         w.do("GET", path, nil, c),
				"updatePlaylist":      w.do("PUT", path, map[string]any{"name": "taken", "description": ""}, c),
				"an update not valid": w.do("PUT", path, map[string]any{"name": "", "description": ""}, c),
				"listPlaylistItems":   w.do("GET", path+"/items", nil, c),
				"addPlaylistItems":    w.do("POST", path+"/items", map[string]any{"track_ids": []string{entryTrack}, "position": nil}, c),
				"an insert":           w.do("POST", path+"/items", map[string]any{"track_ids": []string{entryTrack}, "position": 0}, c),
				"removePlaylistItem":  w.do("DELETE", path+"/items/"+itemID, nil, c),
				"removing their item": w.do("DELETE", path+"/items/"+mineItem, nil, c),
				"movePlaylistItem":    w.do("POST", path+"/items/"+itemID+"/move", map[string]any{"position": 0}, c),
				"deletePlaylist":      w.do("DELETE", path, nil, c),
			} {
				wantRefusal(t, fmt.Sprintf("%s as %s with the If-Match %q", where, other.name, tag), rec, http.StatusNotFound, "playlist_not_found")
			}
		}
		for _, listed := range decode[api.PlaylistList](t, w.get("/playlists", other)).Playlists {
			if listed.Id == p.ID {
				t.Fatalf("%s sees the playlist of another user", other.name)
			}
		}
	}
	if got := w.playlist(w.anna, p.ID); got != before {
		t.Fatalf("the playlist was changed by another user: %+v, want %+v", got, before)
	}
	if items := w.playlistItems(w.anna, p.ID, "50"); len(items) != 1 || items[0].Id != itemID {
		t.Fatalf("the items were changed by another user: %+v", items)
	}
	// The item of anna is not reached through a playlist of bob.
	bob := w.as(w.bob)
	wantRefusal(t, "an item of another playlist, removed", w.do("DELETE", "/playlists/"+mine.ID+"/items/"+itemID, nil, bob),
		http.StatusNotFound, "item_not_found")
	wantRefusal(t, "an item of another playlist, moved",
		w.do("POST", "/playlists/"+mine.ID+"/items/"+itemID+"/move", map[string]any{"position": 0}, ifMatch(bob, mine.ETag())),
		http.StatusNotFound, "item_not_found")
	if items := w.playlistItems(w.anna, p.ID, "50"); len(items) != 1 {
		t.Fatalf("the items of anna: %+v", items)
	}
}

// §8.6, erratum of 2026-10-04: the tracks of an album that left the library
// stay in the playlist, not available, with the last data known; they count
// in item_count and for the positions, not in duration_ms; they cannot be
// added.
func TestPlaylistWithUnavailableTracks(t *testing.T) {
	w := newWorld(t, apiOrigin)
	a1, b1, a2 := w.fixtureTrack(albumA, 1), w.fixtureTrack(albumB, 1), w.fixtureTrack(albumA, 2)
	anna := w.as(w.anna)
	rec := w.do("POST", "/playlists", map[string]any{"name": "grey", "description": ""}, anna)
	wantStatus(t, "createPlaylist", rec, http.StatusCreated)
	p := decode[api.Playlist](t, rec)
	items := "/playlists/" + p.Id + "/items"
	rec = w.do("POST", items, map[string]any{"track_ids": []string{a1, b1, a2}, "position": nil}, anna)
	wantStatus(t, "addPlaylistItems", rec, http.StatusOK)
	whole := decode[api.AddPlaylistItemsResult](t, rec).Playlist
	before := w.playlistItems(w.anna, p.Id, "50")

	w.unavailable(albumA)
	got := w.playlist(w.anna, p.Id)
	if got.ItemCount != 3 || got.DurationMs != *before[1].Track.DurationMs || got.DurationMs >= whole.DurationMs || got.Revision != whole.Revision {
		t.Fatalf("with two unavailable items: %+v, before %+v", got, whole)
	}
	if list := decode[api.PlaylistList](t, w.get("/playlists", w.anna)); len(list.Playlists) != 1 || list.Playlists[0] != got {
		t.Fatalf("the list says %+v", list)
	}
	after := w.playlistItems(w.anna, p.Id, "2")
	for i, it := range after {
		gone := i != 1
		want := before[i]
		want.Track.Available = !gone
		if !reflect.DeepEqual(it, want) || it.Track.Album.Title == "" {
			t.Fatalf("the item %d is %+v, want %+v", i, it, want)
		}
	}
	// The positions count them: the end is item_count.
	wantRefusal(t, "an insert after the end", w.do("POST", items, map[string]any{"track_ids": []string{b1}, "position": 4}, ifMatch(anna, got.Etag)),
		http.StatusUnprocessableEntity, "invalid_position")
	rec = w.do("POST", items+"/"+after[1].Id+"/move", map[string]any{"position": 2}, ifMatch(anna, got.Etag))
	wantStatus(t, "a move to the last position", rec, http.StatusOK)
	got = decode[api.Playlist](t, rec)
	rec = w.do("POST", items, map[string]any{"track_ids": []string{b1}, "position": 3}, ifMatch(anna, got.Etag))
	wantStatus(t, "an insert at item_count", rec, http.StatusOK)
	if got = decode[api.AddPlaylistItemsResult](t, rec).Playlist; got.ItemCount != 4 || got.DurationMs != 2**before[1].Track.DurationMs {
		t.Fatalf("after the insert: %+v", got)
	}
	if order := itemTracks(w.playlistItems(w.anna, p.Id, "50")); !slices.Equal(order, []string{a1, a2, b1, b1}) {
		t.Fatalf("the order is %v", order)
	}
	// A track that is not available is not added, alone or with others.
	ids := wantRefusal(t, "an unavailable track", w.do("POST", items, map[string]any{"track_ids": []string{b1, a2, a1, a2}, "position": nil}, anna),
		http.StatusUnprocessableEntity, "track_unavailable")
	if !slices.Equal(ids, []string{a2, a1}) {
		t.Fatalf("the details of track_unavailable: %v", ids)
	}
	if again := w.playlist(w.anna, p.Id); again != got {
		t.Fatalf("a refused change changed the playlist: %+v", again)
	}
}

// §7.5: deleting an account deletes its playlists and their items, and no
// one else's. The tracks stay.
func TestDeleteUserDeletesItsPlaylists(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.playlistWithItem(w.anna)
	w.playlistWithItem(w.anna)
	kept, keptItem := w.playlistWithItem(w.bob)
	if anna, bob := w.playlistRows(w.anna.id), w.playlistRows(w.bob.id); anna != [2]int{2, 2} || bob != [2]int{1, 1} {
		t.Fatalf("%v and %v rows before the deletion", anna, bob)
	}
	wantStatus(t, "deleteUser", w.do("DELETE", "/admin/users/"+w.anna.id, nil, w.as(w.admin)), http.StatusNoContent)
	if anna, bob := w.playlistRows(w.anna.id), w.playlistRows(w.bob.id); anna != [2]int{0, 0} || bob != [2]int{1, 1} {
		t.Fatalf("%v rows of the deleted account and %v of the other, want none and one", anna, bob)
	}
	if items := w.playlistItems(w.bob, kept.ID, "50"); len(items) != 1 || items[0].Id != keptItem || !items[0].Track.Available {
		t.Fatalf("the playlist of the other: %+v", items)
	}
	// An account with the same name is another account: it has no playlists.
	rec := w.do("POST", "/admin/users", map[string]any{"username": "anna", "password": w.anna.password, "role": "user"}, w.as(w.admin))
	wantStatus(t, "anna again", rec, http.StatusCreated)
	w.anna.id = decode[api.User](t, rec).Id
	if list := decode[api.PlaylistList](t, w.get("/playlists", w.anna)); len(list.Playlists) != 0 {
		t.Fatalf("the playlists of a new account: %+v", list)
	}
}
