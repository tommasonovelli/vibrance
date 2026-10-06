package catalog

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// The playlists of DESIGN.md §8.6 (step S19), on a real database: every
// change is whole or does nothing, leaves the positions dense, and gives
// the playlist a new revision; If-Match is compared in the transaction of
// the change; and a playlist is of its user alone.

// playlistIndex writes the accounts userA and userB and an album with n
// available tracks of one second each, and returns the tracks.
func playlistIndex(t *testing.T, st *store.Store, n int) []string {
	t.Helper()
	putUsers(t, st, userA, userB)
	album := testAlbum{id: uuid.NewString(), artist: testArtist{id: uuid.NewString(), name: "Artist"}, title: "Album",
		firstSeen: 1, available: true}
	putAlbums(t, st, album)
	ids := make([]string, n)
	rows := make([]testTrack, n)
	for i := range n {
		ids[i] = uuid.NewString()
		rows[i] = testTrack{ids[i], 1, int64(i + 1), fmt.Sprintf("Track %d", i), true}
	}
	putTracks(t, st, album.id, rows...)
	return ids
}

// setTrackUnavailable makes a track unavailable, as the scanner does.
func setTrackUnavailable(t *testing.T, st *store.Store, id string) {
	t.Helper()
	err := st.WithWriteTx(t.Context(), func(q *store.Queries) error {
		return q.SetTrackUnavailable(t.Context(), store.SetTrackUnavailableParams{UpdatedAt: 2, ID: id})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// storedItem is a row of playlist_items.
type storedItem struct {
	id, track string
	position  int
	addedAt   int64
}

// storedItems reads the items of a playlist from the database, in the order
// (position, id), with the positions they have there.
func storedItems(t *testing.T, st *store.Store, playlistID string) []storedItem {
	t.Helper()
	var items []storedItem
	err := st.Read(t.Context(), func(q *store.Queries) error {
		rows, err := q.Conn().QueryContext(t.Context(),
			`SELECT id, track_id, position, added_at FROM playlist_items WHERE playlist_id = ? ORDER BY position, id`, playlistID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var it storedItem
			if err := rows.Scan(&it.id, &it.track, &it.position, &it.addedAt); err != nil {
				return errors.Join(err, rows.Close())
			}
			items = append(items, it)
		}
		return errors.Join(rows.Err(), rows.Close())
	})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// playlistState is everything a change could touch: the row of the
// playlist, as the service shows it, and its items as they are stored.
type playlistState struct {
	playlist Playlist
	items    []storedItem
}

func stateOf(t *testing.T, st *store.Store, svc *Service, userID, id string) playlistState {
	t.Helper()
	p, err := svc.GetPlaylist(t.Context(), userID, id)
	if err != nil {
		t.Fatal(err)
	}
	return playlistState{p, storedItems(t, st, id)}
}

func (s playlistState) equal(o playlistState) bool {
	return reflect.DeepEqual(s.playlist, o.playlist) && slices.Equal(s.items, o.items)
}

// allItems reads every page of the items of a playlist, and checks that
// every page but the last is full, that only the last has no cursor and
// that the positions are 0, 1, 2 and so on.
func allItems(t *testing.T, svc *Service, userID, id string, limit int) []PlaylistItem {
	t.Helper()
	var (
		items []PlaylistItem
		after *string
	)
	for pages := 0; pages <= 20000; pages++ {
		page, err := svc.ListPlaylistItems(t.Context(), userID, id, limit, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range page.Items {
			if it.Position != len(items) {
				t.Fatalf("limit %d: the item %d has the position %d", limit, len(items), it.Position)
			}
			items = append(items, it)
		}
		switch {
		case page.Next == "" && len(page.Items) == 0 && pages > 0:
			t.Fatalf("limit %d: an empty last page after a cursor", limit)
		case page.Next == "":
			if page.ItemCount != len(items) {
				t.Fatalf("limit %d: %d items listed, item_count %d", limit, len(items), page.ItemCount)
			}
			return items
		case len(page.Items) != limit:
			t.Fatalf("limit %d: a page of %d items with a cursor", limit, len(page.Items))
		}
		after = &page.Next
	}
	t.Fatal("no end")
	return nil
}

func newPlaylist(t *testing.T, svc *Service, userID, name string) Playlist {
	t.Helper()
	p, err := svc.CreatePlaylist(t.Context(), userID, name, "")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// appendTracks adds tracks at the end of a playlist, without If-Match.
func appendTracks(t *testing.T, svc *Service, userID, id string, tracks ...string) (Playlist, []AddedItem) {
	t.Helper()
	p, added, err := svc.AddPlaylistItems(t.Context(), userID, id, tracks, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p, added
}

// §8.1: the entity tag of a playlist, and what an If-Match names.
func TestPlaylistETag(t *testing.T) {
	const id = "0199a5c3-51d0-7e42-8a6b-9c0d1e2f3a4b"
	p := Playlist{ID: id, Revision: 7}
	const etag = `"playlist:0199a5c3-51d0-7e42-8a6b-9c0d1e2f3a4b:7"`
	if p.ETag() != etag {
		t.Fatalf("the entity tag is %s", p.ETag())
	}
	for header, want := range map[string]bool{
		etag:                             true,
		"W/" + etag:                      true,
		" \t" + etag + " ":               true,
		`"other", ` + etag:               true,
		`"other",W/` + etag + `, "more"`: true,
		`W/"x" , W/` + etag:              true,
		"":                               false,
		"*":                              false,
		",":                              false,
		`"playlist:0199a5c3-51d0-7e42-8a6b-9c0d1e2f3a4b:6"`:  false,
		`"playlist:0199a5c3-51d0-7e42-8a6b-9c0d1e2f3a4b:77"`: false,
		`"playlist:0199a5c3-51d0-7e42-8a6b-9c0d1e2f3a4c:7"`:  false,
		`playlist:0199a5c3-51d0-7e42-8a6b-9c0d1e2f3a4b:7`:    false,
		"w/" + etag:         false,
		"W/W/" + etag:       false,
		"W/ " + etag:        false,
		etag + etag:         false,
		etag + ";":          false,
		"\n" + etag:         false,
		strings.ToUpper(id): false,
		`"playlist:0199a5c3-51d0-7e42-8a6b-9c0d1e2f3a4b:07"`:  false,
		`"playlist:0199a5c3-51d0-7e42-8a6b-9c0d1e2f3a4b:7" x`: false,
	} {
		if got := etagListed(header, etag); got != want {
			t.Errorf("If-Match %q names the playlist: %v, want %v", header, got, want)
		}
	}
	// Without a header a change that needs none goes on, and one that needs
	// it is refused; with one, it must name the revision.
	stale := `"playlist:0199a5c3-51d0-7e42-8a6b-9c0d1e2f3a4b:6"`
	if err := checkRevision(p.ETag(), nil, false); err != nil {
		t.Errorf("no If-Match, not required: %v", err)
	}
	wantCode(t, "no If-Match, required", checkRevision(p.ETag(), nil, true), http.StatusPreconditionRequired, CodePreconditionRequired)
	for _, required := range []bool{false, true} {
		if err := checkRevision(p.ETag(), ptr(etag), required); err != nil {
			t.Errorf("the right If-Match, required %v: %v", required, err)
		}
		wantCode(t, "a stale If-Match", checkRevision(p.ETag(), &stale, required), http.StatusPreconditionFailed, CodePreconditionFailed)
		wantCode(t, "an empty If-Match", checkRevision(p.ETag(), ptr(""), required), http.StatusPreconditionFailed, CodePreconditionFailed)
	}
}

// §8.3: create, read, list, rename and delete, with the revision and the
// moments of each step.
func TestPlaylistCRUD(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 3)
	clock := &testClock{}
	svc := New(st, clock.now)
	ctx := t.Context()

	if got, err := svc.ListPlaylists(ctx, userA); err != nil || len(got) != 0 {
		t.Fatalf("the playlists at first: %v, %v", got, err)
	}
	clock.set(1000)
	p, err := svc.CreatePlaylist(ctx, userA, "Sunday morning", "slow")
	if err != nil {
		t.Fatal(err)
	}
	want := Playlist{ID: p.ID, Name: "Sunday morning", Description: "slow", Revision: 1,
		CreatedAt: time.UnixMilli(1000).UTC(), UpdatedAt: time.UnixMilli(1000).UTC(), Covers: []AlbumCover{}}
	if id, perr := uuid.Parse(p.ID); perr != nil || id.Version() != 7 || !reflect.DeepEqual(p, want) || p.CreatedAt.Location() != time.UTC {
		t.Fatalf("created: %+v, want %+v", p, want)
	}
	if p.ETag() != `"playlist:`+p.ID+`:1"` {
		t.Fatalf("the entity tag of a new playlist: %s", p.ETag())
	}
	if got, err := svc.GetPlaylist(ctx, userA, p.ID); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("read: %+v, %v", got, err)
	}

	// Two playlists may have the same name; the list is the oldest first.
	clock.set(2000)
	second := newPlaylist(t, svc, userA, "Sunday morning")
	clock.set(1500)
	third := newPlaylist(t, svc, userA, "Earlier")
	list, err := svc.ListPlaylists(ctx, userA)
	if err != nil || len(list) != 3 || !reflect.DeepEqual(list[0], want) || list[1].ID != third.ID || list[2].ID != second.ID {
		t.Fatalf("the list: %+v, %v", list, err)
	}

	// Items change the counters, the revision and updated_at.
	clock.set(3000)
	got, added := appendTracks(t, svc, userA, p.ID, tracks[0], tracks[1])
	want.ItemCount, want.DurationMS, want.Revision, want.UpdatedAt = 2, 2000, 2, time.UnixMilli(3000).UTC()
	if !reflect.DeepEqual(got, want) || len(added) != 2 {
		t.Fatalf("after adding: %+v, want %+v", got, want)
	}

	// A rename gives a new revision, also with the values it has.
	clock.set(4000)
	got, err = svc.UpdatePlaylist(ctx, userA, p.ID, "Monday", "", nil)
	want.Name, want.Description, want.Revision, want.UpdatedAt = "Monday", "", 3, time.UnixMilli(4000).UTC()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("renamed: %+v, %v; want %+v", got, err, want)
	}
	clock.set(5000)
	got, err = svc.UpdatePlaylist(ctx, userA, p.ID, "Monday", "", nil)
	want.Revision, want.UpdatedAt = 4, time.UnixMilli(5000).UTC()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("renamed to the same: %+v, %v; want %+v", got, err, want)
	}
	if got, err := svc.GetPlaylist(ctx, userA, p.ID); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("read after the changes: %+v, %v", got, err)
	}

	// Deleting takes the items with it, and only this playlist.
	_, _ = appendTracks(t, svc, userA, second.ID, tracks[2])
	if err := svc.DeletePlaylist(ctx, userA, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	wantNotFound(t, "a deleted playlist", errOf(svc.GetPlaylist(ctx, userA, p.ID)), CodePlaylistNotFound)
	wantNotFound(t, "deleting it again", svc.DeletePlaylist(ctx, userA, p.ID, nil), CodePlaylistNotFound)
	if n := len(storedItems(t, st, p.ID)); n != 0 {
		t.Fatalf("%d items of a deleted playlist", n)
	}
	if n := len(storedItems(t, st, second.ID)); n != 1 {
		t.Fatalf("%d items of the other playlist, want 1", n)
	}
	if list, err := svc.ListPlaylists(ctx, userA); err != nil || len(list) != 2 {
		t.Fatalf("the list after a deletion: %+v, %v", list, err)
	}
	// A playlist that never was.
	missing := uuid.NewString()
	wantNotFound(t, "get", errOf(svc.GetPlaylist(ctx, userA, missing)), CodePlaylistNotFound)
	wantNotFound(t, "update", errOf(svc.UpdatePlaylist(ctx, userA, missing, "x", "", nil)), CodePlaylistNotFound)
	wantNotFound(t, "list items", errOf(svc.ListPlaylistItems(ctx, userA, missing, 50, nil)), CodePlaylistNotFound)
	_, _, err = svc.AddPlaylistItems(ctx, userA, missing, tracks[:1], nil, nil)
	wantNotFound(t, "add", err, CodePlaylistNotFound)
	wantNotFound(t, "remove", errOf(svc.RemovePlaylistItem(ctx, userA, missing, uuid.NewString(), nil)), CodePlaylistNotFound)
	wantNotFound(t, "move", errOf(svc.MovePlaylistItem(ctx, userA, missing, uuid.NewString(), 0, nil)), CodePlaylistNotFound)
}

// errOf is the error of a call that also returns a value.
func errOf[T any](_ T, err error) error { return err }

// §5.2: a name has 1 to 200 characters and a description at most 2000,
// counted as the database counts them; nothing is written otherwise.
func TestPlaylistNameAndDescription(t *testing.T) {
	st := newStore(t)
	playlistIndex(t, st, 1)
	svc := New(st, time.Now)
	ctx := t.Context()
	kept := newPlaylist(t, svc, userA, "kept")
	for _, c := range []struct {
		where, name, description string
		ok                       bool
	}{
		{"one character", "x", "", true},
		{"200 characters", strings.Repeat("x", 200), "", true},
		{"200 characters of two bytes", strings.Repeat("é", 200), "", true},
		{"200 characters of four bytes", strings.Repeat("\U0001F3B5", 200), strings.Repeat("\U0001F3B5", 2000), true},
		{"a description of 2000", "x", strings.Repeat("d", 2000), true},
		{"spaces and line breaks", " \n ", "line one\nline two\t.", true},
		{"no name", "", "", false},
		{"201 characters", strings.Repeat("x", 201), "", false},
		{"201 characters of two bytes", strings.Repeat("é", 201), "", false},
		{"a description of 2001", "x", strings.Repeat("d", 2001), false},
		{"U+0000 in the name", "a\x00b", "", false},
		{"only U+0000", "\x00", "", false},
		{"U+0000 in the description", "x", "\x00", false},
		{"a name that is not UTF-8", "a\xffb", "", false},
		{"a description that is not UTF-8", "x", "\xff", false},
	} {
		before, err := svc.ListPlaylists(ctx, userA)
		if err != nil {
			t.Fatal(err)
		}
		p, err := svc.CreatePlaylist(ctx, userA, c.name, c.description)
		_, uerr := svc.UpdatePlaylist(ctx, userA, kept.ID, c.name, c.description, nil)
		after, lerr := svc.ListPlaylists(ctx, userA)
		if lerr != nil {
			t.Fatal(lerr)
		}
		if c.ok {
			if err != nil || uerr != nil || p.Name != c.name || p.Description != c.description || len(after) != len(before)+1 {
				t.Fatalf("%s: %v, %v", c.where, err, uerr)
			}
			if got, err := svc.GetPlaylist(ctx, userA, kept.ID); err != nil || got.Name != c.name || got.Description != c.description {
				t.Fatalf("%s: read back %v", c.where, err)
			}
			continue
		}
		wantCode(t, c.where+", created", err, http.StatusUnprocessableEntity, CodeInvalidRequest)
		wantCode(t, c.where+", updated", uerr, http.StatusUnprocessableEntity, CodeInvalidRequest)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: a refused name changed the playlists", c.where)
		}
	}
}

// §8.6: where the items are after an insert, a move and a removal. The
// items are named by the order they were first added in.
func TestPlaylistItemOrder(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 8)
	svc := New(st, time.Now)
	ctx := t.Context()
	for _, c := range []struct {
		name string
		// start is how many items the playlist has: 0, 1, 2 and so on.
		start int
		// op changes the playlist; at(n) is the id of the item n, and new
		// items are named from start on.
		op   func(p Playlist, at func(int) string) (Playlist, []AddedItem, error)
		want []int
		// added are the positions the answer gives the new items.
		added []int
	}{
		{"add to an empty playlist", 0, func(p Playlist, _ func(int) string) (Playlist, []AddedItem, error) {
			return svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:2], nil, nil)
		}, []int{0, 1}, []int{0, 1}},
		{"insert at 0 of an empty playlist", 0, func(p Playlist, _ func(int) string) (Playlist, []AddedItem, error) {
			return svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:1], ptr(0), ptr(p.ETag()))
		}, []int{0}, []int{0}},
		{"add at the end", 3, func(p Playlist, _ func(int) string) (Playlist, []AddedItem, error) {
			return svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:2], nil, nil)
		}, []int{0, 1, 2, 3, 4}, []int{3, 4}},
		{"add at the end with If-Match", 3, func(p Playlist, _ func(int) string) (Playlist, []AddedItem, error) {
			return svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:1], nil, ptr(p.ETag()))
		}, []int{0, 1, 2, 3}, []int{3}},
		{"insert at the start", 3, func(p Playlist, _ func(int) string) (Playlist, []AddedItem, error) {
			return svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:2], ptr(0), ptr(p.ETag()))
		}, []int{3, 4, 0, 1, 2}, []int{0, 1}},
		{"insert in the middle", 3, func(p Playlist, _ func(int) string) (Playlist, []AddedItem, error) {
			return svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:3], ptr(1), ptr(p.ETag()))
		}, []int{0, 3, 4, 5, 1, 2}, []int{1, 2, 3}},
		{"insert before the last", 3, func(p Playlist, _ func(int) string) (Playlist, []AddedItem, error) {
			return svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:1], ptr(2), ptr(p.ETag()))
		}, []int{0, 1, 3, 2}, []int{2}},
		{"insert at item_count", 3, func(p Playlist, _ func(int) string) (Playlist, []AddedItem, error) {
			return svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:2], ptr(3), ptr(p.ETag()))
		}, []int{0, 1, 2, 3, 4}, []int{3, 4}},
		{"the same track twice in one request", 2, func(p Playlist, _ func(int) string) (Playlist, []AddedItem, error) {
			return svc.AddPlaylistItems(ctx, userA, p.ID, []string{tracks[5], tracks[5], tracks[0]}, ptr(1), ptr(p.ETag()))
		}, []int{0, 2, 3, 4, 1}, []int{1, 2, 3}},
		{"move forward", 5, func(p Playlist, at func(int) string) (Playlist, []AddedItem, error) {
			p, err := svc.MovePlaylistItem(ctx, userA, p.ID, at(1), 3, ptr(p.ETag()))
			return p, nil, err
		}, []int{0, 2, 3, 1, 4}, nil},
		{"move back", 5, func(p Playlist, at func(int) string) (Playlist, []AddedItem, error) {
			p, err := svc.MovePlaylistItem(ctx, userA, p.ID, at(3), 1, ptr(p.ETag()))
			return p, nil, err
		}, []int{0, 3, 1, 2, 4}, nil},
		{"move the first to the end", 5, func(p Playlist, at func(int) string) (Playlist, []AddedItem, error) {
			p, err := svc.MovePlaylistItem(ctx, userA, p.ID, at(0), 4, ptr(p.ETag()))
			return p, nil, err
		}, []int{1, 2, 3, 4, 0}, nil},
		{"move the last to the start", 5, func(p Playlist, at func(int) string) (Playlist, []AddedItem, error) {
			p, err := svc.MovePlaylistItem(ctx, userA, p.ID, at(4), 0, ptr(p.ETag()))
			return p, nil, err
		}, []int{4, 0, 1, 2, 3}, nil},
		{"move by one", 5, func(p Playlist, at func(int) string) (Playlist, []AddedItem, error) {
			p, err := svc.MovePlaylistItem(ctx, userA, p.ID, at(2), 3, ptr(p.ETag()))
			return p, nil, err
		}, []int{0, 1, 3, 2, 4}, nil},
		{"move to where it is", 5, func(p Playlist, at func(int) string) (Playlist, []AddedItem, error) {
			p, err := svc.MovePlaylistItem(ctx, userA, p.ID, at(2), 2, ptr(p.ETag()))
			return p, nil, err
		}, []int{0, 1, 2, 3, 4}, nil},
		{"move the only item", 1, func(p Playlist, at func(int) string) (Playlist, []AddedItem, error) {
			p, err := svc.MovePlaylistItem(ctx, userA, p.ID, at(0), 0, ptr(p.ETag()))
			return p, nil, err
		}, []int{0}, nil},
		{"remove the first", 4, func(p Playlist, at func(int) string) (Playlist, []AddedItem, error) {
			p, err := svc.RemovePlaylistItem(ctx, userA, p.ID, at(0), nil)
			return p, nil, err
		}, []int{1, 2, 3}, nil},
		{"remove from the middle", 4, func(p Playlist, at func(int) string) (Playlist, []AddedItem, error) {
			p, err := svc.RemovePlaylistItem(ctx, userA, p.ID, at(1), ptr(p.ETag()))
			return p, nil, err
		}, []int{0, 2, 3}, nil},
		{"remove the last", 4, func(p Playlist, at func(int) string) (Playlist, []AddedItem, error) {
			p, err := svc.RemovePlaylistItem(ctx, userA, p.ID, at(3), nil)
			return p, nil, err
		}, []int{0, 1, 2}, nil},
		{"remove the only item", 1, func(p Playlist, at func(int) string) (Playlist, []AddedItem, error) {
			p, err := svc.RemovePlaylistItem(ctx, userA, p.ID, at(0), nil)
			return p, nil, err
		}, nil, nil},
	} {
		p := newPlaylist(t, svc, userA, c.name)
		var names []string
		for i := range c.start {
			var added []AddedItem
			p, added = appendTracks(t, svc, userA, p.ID, tracks[i])
			names = append(names, added[0].ItemID)
		}
		got, added, err := c.op(p, func(n int) string { return names[n] })
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for _, a := range added {
			names = append(names, a.ItemID)
		}
		var order []int
		for i, it := range storedItems(t, st, p.ID) {
			if it.position != i {
				t.Fatalf("%s: the item %d is stored at the position %d", c.name, i, it.position)
			}
			order = append(order, slices.Index(names, it.id))
		}
		if !slices.Equal(order, c.want) {
			t.Fatalf("%s: the order is %v, want %v", c.name, order, c.want)
		}
		var positions []int
		for i, a := range added {
			positions = append(positions, a.Position)
			if a.TrackID != storedItems(t, st, p.ID)[a.Position].track || a.ItemID != storedItems(t, st, p.ID)[a.Position].id {
				t.Fatalf("%s: the added item %d is not at its position: %+v", c.name, i, a)
			}
		}
		if !slices.Equal(positions, c.added) {
			t.Fatalf("%s: the added items are at %v, want %v", c.name, positions, c.added)
		}
		if got.Revision != p.Revision+1 || got.ItemCount != len(c.want) || got.DurationMS != int64(1000*len(c.want)) {
			t.Fatalf("%s: the playlist is %+v after %+v", c.name, got, p)
		}
		if listed := allItems(t, svc, userA, p.ID, 2); len(listed) != len(c.want) {
			t.Fatalf("%s: %d items listed", c.name, len(listed))
		}
	}
}

// modelItem and playlistModel are a playlist as the design describes it,
// kept in memory: the oracle of the property test.
type modelItem struct {
	id, track string
	addedAt   int64
}

type playlistModel struct {
	items    []modelItem
	revision int64
}

// §8.6, the property: after any sequence of changes, refused ones among
// them, the positions stored are 0..n-1, the items are the ones of a model
// in memory, in its order, with the ids, the tracks and the moments they
// were added with, and the revision counts the changes that were made.
func TestPlaylistAgainstAModel(t *testing.T) {
	for seed := range uint64(3) {
		r := rand.New(rand.NewPCG(seed, 19))
		st := newStore(t)
		tracks := playlistIndex(t, st, 6)
		clock := &testClock{}
		svc := New(st, clock.now)
		ctx := t.Context()
		p := newPlaylist(t, svc, userA, "model")
		other := newPlaylist(t, svc, userA, "another")
		_, foreign := appendTracks(t, svc, userA, other.ID, tracks[0])
		m := playlistModel{revision: 1}
		etag := func() *string { return ptr(Playlist{ID: p.ID, Revision: m.revision}.ETag()) }
		stale := func() *string { return ptr(Playlist{ID: p.ID, Revision: m.revision - 1 + int64(2*r.IntN(2))}.ETag()) }
		// sometimes is the right If-Match or none, for a change that does
		// not need it.
		sometimes := func() *string {
			if r.IntN(2) == 0 {
				return nil
			}
			return etag()
		}
		pick := func(n int) []string {
			ids := make([]string, n)
			for i := range ids {
				ids[i] = tracks[r.IntN(len(tracks))]
			}
			return ids
		}
		done := map[string]int{}
		for step := range 300 {
			now := int64(1000 + step)
			clock.set(now)
			n := len(m.items)
			var (
				kind   string
				err    error
				status int
				code   string
			)
			insert := func(at int, added []AddedItem) {
				block := make([]modelItem, len(added))
				for i, a := range added {
					block[i] = modelItem{a.ItemID, a.TrackID, now}
					if a.Position != at+i {
						t.Fatalf("seed %d, step %d: the added item %d is at %d, want %d", seed, step, i, a.Position, at+i)
					}
				}
				m.items = slices.Insert(m.items, at, block...)
			}
			switch op := r.IntN(20); {
			case op < 4:
				kind = "append"
				ids := pick(1 + r.IntN(3))
				var added []AddedItem
				if _, added, err = svc.AddPlaylistItems(ctx, userA, p.ID, ids, nil, sometimes()); err == nil {
					insert(n, added)
				}
			case op < 8:
				kind = "insert"
				at := r.IntN(n + 1)
				var added []AddedItem
				if _, added, err = svc.AddPlaylistItems(ctx, userA, p.ID, pick(1+r.IntN(3)), &at, etag()); err == nil {
					insert(at, added)
				}
			case op < 11 && n > 0:
				kind = "move"
				from, to := r.IntN(n), r.IntN(n)
				if _, err = svc.MovePlaylistItem(ctx, userA, p.ID, m.items[from].id, to, etag()); err == nil {
					it := m.items[from]
					m.items = slices.Insert(slices.Delete(m.items, from, from+1), to, it)
				}
			case op < 13 && n > 0:
				kind = "remove"
				at := r.IntN(n)
				if _, err = svc.RemovePlaylistItem(ctx, userA, p.ID, m.items[at].id, sometimes()); err == nil {
					m.items = slices.Delete(m.items, at, at+1)
				}
			case op == 13:
				kind = "rename"
				_, err = svc.UpdatePlaylist(ctx, userA, p.ID, fmt.Sprintf("model %d", step), "", sometimes())
			case op == 14:
				kind, status, code = "insert without If-Match", http.StatusPreconditionRequired, CodePreconditionRequired
				_, _, err = svc.AddPlaylistItems(ctx, userA, p.ID, pick(2), ptr(r.IntN(n+1)), nil)
			case op == 15:
				kind, status, code = "a stale If-Match", http.StatusPreconditionFailed, CodePreconditionFailed
				switch r.IntN(4) {
				case 0:
					_, _, err = svc.AddPlaylistItems(ctx, userA, p.ID, pick(2), nil, stale())
				case 1:
					_, _, err = svc.AddPlaylistItems(ctx, userA, p.ID, pick(1), ptr(0), stale())
				case 2:
					_, err = svc.UpdatePlaylist(ctx, userA, p.ID, "never", "", stale())
				default:
					// The item need not exist: the revision is compared first.
					_, err = svc.RemovePlaylistItem(ctx, userA, p.ID, foreign[0].ItemID, stale())
				}
			case op == 16:
				kind, status, code = "a position out of range", http.StatusUnprocessableEntity, CodeInvalidPosition
				switch {
				case r.IntN(2) == 0:
					_, _, err = svc.AddPlaylistItems(ctx, userA, p.ID, pick(1), ptr([]int{-1, n + 1, n + 100}[r.IntN(3)]), etag())
				case n > 0:
					_, err = svc.MovePlaylistItem(ctx, userA, p.ID, m.items[r.IntN(n)].id, []int{-1, n, n + 7}[r.IntN(3)], etag())
				default:
					_, _, err = svc.AddPlaylistItems(ctx, userA, p.ID, pick(1), ptr(1), etag())
				}
			case op == 17:
				kind, status, code = "an item of another playlist", http.StatusNotFound, CodeItemNotFound
				if r.IntN(2) == 0 {
					_, err = svc.MovePlaylistItem(ctx, userA, p.ID, foreign[0].ItemID, 0, etag())
				} else {
					_, err = svc.RemovePlaylistItem(ctx, userA, p.ID, uuid.NewString(), sometimes())
				}
			case op == 18:
				kind, status, code = "an unknown track", http.StatusUnprocessableEntity, CodeUnknownTrack
				ids := append(pick(2), uuid.NewString())
				r.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
				_, _, err = svc.AddPlaylistItems(ctx, userA, p.ID, ids, ptr(r.IntN(n+1)), etag())
			default:
				kind, status, code = "another user", http.StatusNotFound, CodePlaylistNotFound
				_, _, err = svc.AddPlaylistItems(ctx, userB, p.ID, pick(1), nil, nil)
			}
			where := fmt.Sprintf("seed %d, step %d, %s", seed, step, kind)
			if status != 0 {
				wantCode(t, where, err, status, code)
			} else {
				if err != nil {
					t.Fatalf("%s: %v", where, err)
				}
				m.revision++
			}
			done[kind]++
			got := stateOf(t, st, svc, userA, p.ID)
			if got.playlist.Revision != m.revision || got.playlist.ItemCount != len(m.items) ||
				got.playlist.DurationMS != int64(1000*len(m.items)) || len(got.items) != len(m.items) {
				t.Fatalf("%s: the playlist is %+v with %d items; the model has %d at revision %d", where, got.playlist,
					len(got.items), len(m.items), m.revision)
			}
			for i, it := range got.items {
				if want := (storedItem{m.items[i].id, m.items[i].track, i, m.items[i].addedAt}); it != want {
					t.Fatalf("%s: the item %d is %+v, want %+v", where, i, it, want)
				}
			}
		}
		for _, kind := range []string{"append", "insert", "move", "remove", "rename", "insert without If-Match", "a stale If-Match",
			"a position out of range", "an item of another playlist", "an unknown track", "another user"} {
			if done[kind] < 3 {
				t.Fatalf("seed %d: only %d changes of kind %q", seed, done[kind], kind)
			}
		}
		// The list says the same, whatever the size of its pages.
		for _, limit := range []int{1, 2, 7, 50, 200} {
			listed := allItems(t, svc, userA, p.ID, limit)
			if len(listed) != len(m.items) {
				t.Fatalf("seed %d, limit %d: %d items listed, want %d", seed, limit, len(listed), len(m.items))
			}
			for i, it := range listed {
				if it.ID != m.items[i].id || it.Track.ID != m.items[i].track || it.AddedAt.UnixMilli() != m.items[i].addedAt {
					t.Fatalf("seed %d, limit %d: the item %d is %+v, want %+v", seed, limit, i, it, m.items[i])
				}
			}
		}
		// The other playlist was never touched.
		if got := stateOf(t, st, svc, userA, other.ID); got.playlist.Revision != 2 || len(got.items) != 1 {
			t.Fatalf("seed %d: the other playlist is %+v", seed, got)
		}
	}
}

// §8.1, §8.6: where If-Match is required its absence is 428, an old
// revision is 412 everywhere, and a refused change changes nothing; a W/
// in front of the tag is ignored.
func TestPlaylistPreconditions(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 4)
	svc := New(st, time.Now)
	ctx := t.Context()
	p := newPlaylist(t, svc, userA, "preconditions")
	old := p.ETag()
	p, added := appendTracks(t, svc, userA, p.ID, tracks[0], tracks[1], tracks[2])
	item := added[1].ItemID

	type change func(ifMatch *string) error
	changes := map[string]struct {
		do       change
		required bool
	}{
		"rename": {func(m *string) error { return errOf(svc.UpdatePlaylist(ctx, userA, p.ID, "renamed", "d", m)) }, false},
		"delete": {func(m *string) error { return svc.DeletePlaylist(ctx, userA, p.ID, m) }, false},
		"append": {func(m *string) error {
			_, _, err := svc.AddPlaylistItems(ctx, userA, p.ID, tracks[3:], nil, m)
			return err
		}, false},
		"insert": {func(m *string) error {
			_, _, err := svc.AddPlaylistItems(ctx, userA, p.ID, tracks[3:], ptr(1), m)
			return err
		}, true},
		"remove": {func(m *string) error { return errOf(svc.RemovePlaylistItem(ctx, userA, p.ID, item, m)) }, false},
		"move":   {func(m *string) error { return errOf(svc.MovePlaylistItem(ctx, userA, p.ID, item, 0, m)) }, true},
	}
	before := stateOf(t, st, svc, userA, p.ID)
	unchanged := func(where string) {
		t.Helper()
		if after := stateOf(t, st, svc, userA, p.ID); !after.equal(before) {
			t.Fatalf("%s changed the playlist:\n got %+v\nwant %+v", where, after, before)
		}
	}
	for name, c := range changes {
		for _, stale := range []string{old, "W/" + old, "*", "", `"playlist:` + uuid.NewString() + `:2"`, strings.Trim(p.ETag(), `"`)} {
			wantCode(t, name+" with the If-Match "+stale, c.do(&stale), http.StatusPreconditionFailed, CodePreconditionFailed)
			unchanged(name + " with the If-Match " + stale)
		}
		if c.required {
			wantCode(t, name+" without If-Match", c.do(nil), http.StatusPreconditionRequired, CodePreconditionRequired)
			unchanged(name + " without If-Match")
		}
	}
	// Each change is made with the tag of the revision, weak or strong, or
	// without one where none is needed.
	for _, name := range []string{"rename", "append", "insert", "remove", "delete"} {
		c := changes[name]
		if name == "remove" {
			// The move first, on the item the removal takes away.
			for _, prefix := range []string{"", "W/", `"x", `} {
				tag := prefix + p.ETag()
				if err := changes["move"].do(&tag); err != nil {
					t.Fatalf("move with the If-Match %s: %v", tag, err)
				}
				p.Revision++
			}
		}
		tag := p.ETag()
		if name == "append" {
			tag = "W/" + tag
		}
		if err := c.do(&tag); err != nil {
			t.Fatalf("%s with the If-Match %s: %v", name, tag, err)
		}
		p.Revision++
		if name == "rename" || name == "append" {
			if err := c.do(nil); err != nil {
				t.Fatalf("%s without If-Match: %v", name, err)
			}
			p.Revision++
		}
		if name == "delete" {
			break
		}
		if got, err := svc.GetPlaylist(ctx, userA, p.ID); err != nil || got.Revision != p.Revision {
			t.Fatalf("after %s: revision %d, %v; want %d", name, got.Revision, err, p.Revision)
		}
	}
	wantNotFound(t, "the deleted playlist", errOf(svc.GetPlaylist(ctx, userA, p.ID)), CodePlaylistNotFound)
}

// §8.6: tracks that do not exist, or are not available, refuse the whole
// request, with their ids; nothing is added.
func TestAddPlaylistItemsIsAllOrNothing(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 5)
	setTrackUnavailable(t, st, tracks[3])
	setTrackUnavailable(t, st, tracks[4])
	svc := New(st, time.Now)
	ctx := t.Context()
	p := newPlaylist(t, svc, userA, "whole")
	p, _ = appendTracks(t, svc, userA, p.ID, tracks[0], tracks[1])
	before := stateOf(t, st, svc, userA, p.ID)
	unknownA, unknownB := uuid.NewString(), uuid.NewString()

	for _, c := range []struct {
		where string
		ids   []string
		code  string
		want  []string
	}{
		{"one unknown track among known ones", []string{tracks[0], unknownA, tracks[1]}, CodeUnknownTrack, []string{unknownA}},
		{"only an unknown track", []string{unknownA}, CodeUnknownTrack, []string{unknownA}},
		{"unknown tracks, one twice", []string{unknownB, tracks[2], unknownA, unknownB}, CodeUnknownTrack, []string{unknownB, unknownA}},
		{"an unavailable track", []string{tracks[0], tracks[3]}, CodeTrackUnavailable, []string{tracks[3]}},
		{"unavailable tracks, one twice", []string{tracks[4], tracks[3], tracks[4], tracks[2]}, CodeTrackUnavailable, []string{tracks[4], tracks[3]}},
		{"unknown and unavailable", []string{tracks[3], unknownA, tracks[0]}, CodeUnknownTrack, []string{unknownA}},
	} {
		for _, position := range []*int{nil, ptr(1)} {
			_, added, err := svc.AddPlaylistItems(ctx, userA, p.ID, c.ids, position, ptr(p.ETag()))
			wantCode(t, c.where, err, http.StatusUnprocessableEntity, c.code)
			var e *httpx.Error
			if !errors.As(err, &e) || len(e.Details) != 1 || !slices.Equal(e.Details["track_ids"].([]string), c.want) || added != nil {
				t.Fatalf("%s: details %v, want the track ids %v", c.where, e.Details, c.want)
			}
			if after := stateOf(t, st, svc, userA, p.ID); !after.equal(before) {
				t.Fatalf("%s changed the playlist:\n got %+v\nwant %+v", c.where, after, before)
			}
		}
	}
}

// §8.6, erratum of 2026-10-04: a track that is no longer available stays
// in the playlist, where it was; it counts as an item and for the
// positions, and not in the duration.
func TestUnavailableTracksStayInAPlaylist(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 4)
	svc := New(st, time.Now)
	ctx := t.Context()
	p := newPlaylist(t, svc, userA, "grey")
	p, added := appendTracks(t, svc, userA, p.ID, tracks[0], tracks[1], tracks[2], tracks[1])
	if p.ItemCount != 4 || p.DurationMS != 4000 {
		t.Fatalf("at first: %+v", p)
	}
	if err := svc.AddFavorite(ctx, userA, tracks[1]); err != nil {
		t.Fatal(err)
	}
	setTrackUnavailable(t, st, tracks[1])

	got, err := svc.GetPlaylist(ctx, userA, p.ID)
	if err != nil || got.ItemCount != 4 || got.DurationMS != 2000 || got.Revision != p.Revision {
		t.Fatalf("with two unavailable items: %+v, %v", got, err)
	}
	list, err := svc.ListPlaylists(ctx, userA)
	if err != nil || len(list) != 1 || !reflect.DeepEqual(list[0], got) {
		t.Fatalf("the list says %+v, the playlist %+v (%v)", list, got, err)
	}
	items := allItems(t, svc, userA, p.ID, 3)
	for i, it := range items {
		gone := i == 1 || i == 3
		if it.ID != added[i].ItemID || it.Track.ID != added[i].TrackID || it.Track.Available == gone || it.Track.Favorite != gone ||
			it.Track.Title == "" || it.Track.Album.Title != "Album" || it.Track.Album.Artist.Name != "Artist" {
			t.Fatalf("the item %d: %+v", i, it)
		}
	}
	// The same track says the same as a track read alone, for both users.
	for _, user := range []string{userA, userB} {
		alone, err := svc.GetTrack(ctx, user, tracks[1])
		if err != nil {
			t.Fatal(err)
		}
		if user == userA && !(reflect.DeepEqual(alone, items[1].Track) && reflect.DeepEqual(alone, items[3].Track)) {
			t.Fatalf("the track in the playlist is %+v, alone %+v", items[1].Track, alone)
		}
		if alone.Favorite != (user == userA) {
			t.Fatalf("favorite for %s: %v", user, alone.Favorite)
		}
	}

	// The positions count the unavailable items: item_count is the end, and
	// the last position is item_count less one.
	wantCode(t, "insert after the end", errOf2(svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:1], ptr(5), ptr(got.ETag()))),
		http.StatusUnprocessableEntity, CodeInvalidPosition)
	wantCode(t, "move after the end", errOf(svc.MovePlaylistItem(ctx, userA, p.ID, added[0].ItemID, 4, ptr(got.ETag()))),
		http.StatusUnprocessableEntity, CodeInvalidPosition)
	got, err = svc.MovePlaylistItem(ctx, userA, p.ID, added[0].ItemID, 3, ptr(got.ETag()))
	if err != nil || got.ItemCount != 4 || got.DurationMS != 2000 {
		t.Fatalf("moved to the last position: %+v, %v", got, err)
	}
	got, newItems, err := svc.AddPlaylistItems(ctx, userA, p.ID, tracks[3:], ptr(4), ptr(got.ETag()))
	if err != nil || got.ItemCount != 5 || got.DurationMS != 3000 || newItems[0].Position != 4 {
		t.Fatalf("inserted at item_count: %+v, %v", got, err)
	}
	// An unavailable item is moved and removed like any other; it cannot be
	// added again.
	got, err = svc.MovePlaylistItem(ctx, userA, p.ID, added[3].ItemID, 0, ptr(got.ETag()))
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, "adding an unavailable track", errOf2(svc.AddPlaylistItems(ctx, userA, p.ID, tracks[1:2], nil, nil)),
		http.StatusUnprocessableEntity, CodeTrackUnavailable)
	got, err = svc.RemovePlaylistItem(ctx, userA, p.ID, added[1].ItemID, nil)
	if err != nil || got.ItemCount != 4 || got.DurationMS != 3000 {
		t.Fatalf("an unavailable item removed: %+v, %v", got, err)
	}
	var order []string
	for _, it := range allItems(t, svc, userA, p.ID, 50) {
		order = append(order, it.ID)
	}
	if want := []string{added[3].ItemID, added[2].ItemID, added[0].ItemID, newItems[0].ItemID}; !slices.Equal(order, want) {
		t.Fatalf("the order is %v, want %v", order, want)
	}
}

// errOf2 is the error of a call that also returns two values.
func errOf2[T, U any](_ T, _ U, err error) error { return err }

// §5.2: a playlist has at most 10000 items and a user at most 500
// playlists; the request that would pass a limit does nothing.
func TestPlaylistLimits(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 3)
	svc := New(st, time.Now)
	ctx := t.Context()

	p := newPlaylist(t, svc, userA, "full")
	block := make([]string, 1000)
	for i := range block {
		block[i] = tracks[i%len(tracks)]
	}
	for range 9 {
		p, _ = appendTracks(t, svc, userA, p.ID, block...)
	}
	p, _ = appendTracks(t, svc, userA, p.ID, block[:999]...)
	if p.ItemCount != MaxPlaylistItems-1 {
		t.Fatalf("%d items", p.ItemCount)
	}
	// Two more are one too many, at the end and at a position: none is
	// added.
	wantCode(t, "10001 items", errOf2(svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:2], nil, nil)),
		http.StatusUnprocessableEntity, CodeTooManyItems)
	wantCode(t, "10001 items, at a position", errOf2(svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:2], ptr(0), ptr(p.ETag()))),
		http.StatusUnprocessableEntity, CodeTooManyItems)
	if got, err := svc.GetPlaylist(ctx, userA, p.ID); err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("after the refusals: %+v, %v; want %+v", got, err, p)
	}
	// The item 10000 is added, in the middle, and the positions are dense.
	p, added, err := svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:1], ptr(5000), ptr(p.ETag()))
	if err != nil || p.ItemCount != MaxPlaylistItems || p.DurationMS != 1000*MaxPlaylistItems {
		t.Fatalf("the item 10000: %+v, %v", p, err)
	}
	stored := storedItems(t, st, p.ID)
	for i, it := range stored {
		if it.position != i {
			t.Fatalf("the item %d is stored at %d", i, it.position)
		}
	}
	if len(stored) != MaxPlaylistItems || stored[5000].id != added[0].ItemID {
		t.Fatalf("%d items stored", len(stored))
	}
	wantCode(t, "one more", errOf2(svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:1], nil, nil)),
		http.StatusUnprocessableEntity, CodeTooManyItems)
	// A full playlist can still lose an item, be reordered, and take one
	// again.
	p, err = svc.MovePlaylistItem(ctx, userA, p.ID, added[0].ItemID, MaxPlaylistItems-1, ptr(p.ETag()))
	if err != nil {
		t.Fatal(err)
	}
	if p, err = svc.RemovePlaylistItem(ctx, userA, p.ID, stored[0].id, nil); err != nil || p.ItemCount != MaxPlaylistItems-1 {
		t.Fatalf("an item removed from a full playlist: %+v, %v", p, err)
	}
	if listed := allItems(t, svc, userA, p.ID, 200); len(listed) != MaxPlaylistItems-1 || listed[len(listed)-1].ID != added[0].ItemID {
		t.Fatalf("%d items listed", len(listed))
	}
	if p, _ = appendTracks(t, svc, userA, p.ID, tracks[0]); p.ItemCount != MaxPlaylistItems {
		t.Fatalf("%d items", p.ItemCount)
	}

	// The playlists: the one above and 499 more, then no more; the other
	// user has their own count.
	for i := 1; i < MaxPlaylists; i++ {
		newPlaylist(t, svc, userA, fmt.Sprintf("playlist %d", i))
	}
	wantCode(t, "playlist 501", errOf(svc.CreatePlaylist(ctx, userA, "one too many", "")),
		http.StatusUnprocessableEntity, CodeTooManyPlaylists)
	list, err := svc.ListPlaylists(ctx, userA)
	if err != nil || len(list) != MaxPlaylists || !reflect.DeepEqual(list[0], p) {
		t.Fatalf("%d playlists, %v", len(list), err)
	}
	newPlaylist(t, svc, userB, "of the other user")
	if err := svc.DeletePlaylist(ctx, userA, list[1].ID, nil); err != nil {
		t.Fatal(err)
	}
	newPlaylist(t, svc, userA, "room again")
	wantCode(t, "playlist 501 again", errOf(svc.CreatePlaylist(ctx, userA, "one too many", "")),
		http.StatusUnprocessableEntity, CodeTooManyPlaylists)
}

// §8.6, I6: the playlist of another user is one that does not exist, for
// every operation, and nothing of it is read or changed. An item is of its
// playlist: it cannot be reached through another one.
func TestPlaylistsArePrivate(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 3)
	svc := New(st, time.Now)
	ctx := t.Context()
	p := newPlaylist(t, svc, userA, "private")
	p, added := appendTracks(t, svc, userA, p.ID, tracks[0], tracks[1])
	mine := newPlaylist(t, svc, userB, "of B")
	mine, mineAdded := appendTracks(t, svc, userB, mine.ID, tracks[2])
	before := stateOf(t, st, svc, userA, p.ID)
	tag := ptr(p.ETag())

	for where, err := range map[string]error{
		"get":                    errOf(svc.GetPlaylist(ctx, userB, p.ID)),
		"update":                 errOf(svc.UpdatePlaylist(ctx, userB, p.ID, "taken", "", nil)),
		"update with If-Match":   errOf(svc.UpdatePlaylist(ctx, userB, p.ID, "taken", "", tag)),
		"update, not valid":      errOf(svc.UpdatePlaylist(ctx, userB, p.ID, "", "", nil)),
		"delete":                 svc.DeletePlaylist(ctx, userB, p.ID, nil),
		"delete with If-Match":   svc.DeletePlaylist(ctx, userB, p.ID, tag),
		"list items":             errOf(svc.ListPlaylistItems(ctx, userB, p.ID, 50, nil)),
		"add":                    errOf2(svc.AddPlaylistItems(ctx, userB, p.ID, tracks[:1], nil, nil)),
		"insert":                 errOf2(svc.AddPlaylistItems(ctx, userB, p.ID, tracks[:1], ptr(0), tag)),
		"insert, no If-Match":    errOf2(svc.AddPlaylistItems(ctx, userB, p.ID, tracks[:1], ptr(0), nil)),
		"insert, stale If-Match": errOf2(svc.AddPlaylistItems(ctx, userB, p.ID, tracks[:1], ptr(0), ptr(mine.ETag()))),
		"add an unknown track":   errOf2(svc.AddPlaylistItems(ctx, userB, p.ID, []string{uuid.NewString()}, nil, nil)),
		"remove":                 errOf(svc.RemovePlaylistItem(ctx, userB, p.ID, added[0].ItemID, nil)),
		"remove their own item":  errOf(svc.RemovePlaylistItem(ctx, userB, p.ID, mineAdded[0].ItemID, nil)),
		"move":                   errOf(svc.MovePlaylistItem(ctx, userB, p.ID, added[0].ItemID, 1, tag)),
		"move, no If-Match":      errOf(svc.MovePlaylistItem(ctx, userB, p.ID, added[0].ItemID, 1, nil)),
		"a user that is nobody":  errOf(svc.GetPlaylist(ctx, uuid.NewString(), p.ID)),
	} {
		wantNotFound(t, where, err, CodePlaylistNotFound)
	}
	if after := stateOf(t, st, svc, userA, p.ID); !after.equal(before) {
		t.Fatalf("another user changed the playlist:\n got %+v\nwant %+v", after, before)
	}
	// Each sees their own playlists, and no others.
	for user, want := range map[string]Playlist{userA: p, userB: mine} {
		if list, err := svc.ListPlaylists(ctx, user); err != nil || len(list) != 1 || !reflect.DeepEqual(list[0], want) {
			t.Fatalf("the playlists of %s: %+v, %v", user, list, err)
		}
	}
	// The item of A cannot be moved or removed through a playlist of B.
	wantNotFound(t, "an item of another playlist, removed", errOf(svc.RemovePlaylistItem(ctx, userB, mine.ID, added[0].ItemID, nil)),
		CodeItemNotFound)
	wantNotFound(t, "an item of another playlist, moved",
		errOf(svc.MovePlaylistItem(ctx, userB, mine.ID, added[0].ItemID, 0, ptr(mine.ETag()))), CodeItemNotFound)
	if after := stateOf(t, st, svc, userA, p.ID); !after.equal(before) {
		t.Fatalf("an item was reached through another playlist: %+v", after)
	}
	if got := stateOf(t, st, svc, userB, mine.ID); !reflect.DeepEqual(got.playlist, mine) || len(got.items) != 1 {
		t.Fatalf("the playlist of B: %+v", got)
	}
}

// §5.2: the playlists of an account, and their items, go with the account.
func TestDeletedUserLosesItsPlaylists(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 2)
	svc := New(st, time.Now)
	ctx := t.Context()
	gone := newPlaylist(t, svc, userA, "of A")
	_, _ = appendTracks(t, svc, userA, gone.ID, tracks...)
	kept := newPlaylist(t, svc, userB, "of B")
	_, _ = appendTracks(t, svc, userB, kept.ID, tracks[0])
	if err := st.WithWriteTx(ctx, func(q *store.Queries) error { return q.DeleteUser(ctx, userA) }); err != nil {
		t.Fatal(err)
	}
	wantNotFound(t, "the playlist of a deleted account", errOf(svc.GetPlaylist(ctx, userA, gone.ID)), CodePlaylistNotFound)
	if n := len(storedItems(t, st, gone.ID)); n != 0 {
		t.Fatalf("%d items of a deleted account", n)
	}
	if got := stateOf(t, st, svc, userB, kept.ID); len(got.items) != 1 || got.playlist.ItemCount != 1 {
		t.Fatalf("the playlist of the other account: %+v", got)
	}
	// The tracks are still there (I3).
	for _, id := range tracks {
		if _, err := svc.GetTrack(ctx, userB, id); err != nil {
			t.Fatal(err)
		}
	}
	// A request of the deleted account that was authenticated before:
	// nothing is created.
	wantCode(t, "a playlist of a deleted account", errOf(svc.CreatePlaylist(ctx, userA, "late", "")), http.StatusUnauthorized, "login_required")
}

// §8.5: a cursor is one of this list or is refused, and it stays valid when
// the item it names is removed.
func TestPlaylistItemCursors(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 5)
	svc := New(st, time.Now)
	ctx := t.Context()
	p := newPlaylist(t, svc, userA, "cursors")
	p, added := appendTracks(t, svc, userA, p.ID, tracks...)
	page, err := svc.ListPlaylistItems(ctx, userA, p.ID, 2, nil)
	if err != nil || len(page.Items) != 2 || page.Next == "" || page.ETag != p.ETag() || page.ItemCount != p.ItemCount {
		t.Fatalf("the first page: %+v, %v", page, err)
	}
	// The cursors of the other lists, and what is no cursor at all.
	if err := svc.AddFavorite(ctx, userA, tracks[0]); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddFavorite(ctx, userA, tracks[1]); err != nil {
		t.Fatal(err)
	}
	favorites, err := svc.ListFavorites(ctx, userA, 1, nil)
	if err != nil || favorites.Next == "" {
		t.Fatalf("the favorites: %v", err)
	}
	other, err := httpx.EncodeCursor(sortPosition, OrderDesc, httpx.IntKey(1), httpx.IDKey(added[1].ItemID))
	if err != nil {
		t.Fatal(err)
	}
	short, err := httpx.EncodeCursor(sortPosition, OrderAsc, httpx.IntKey(1))
	if err != nil {
		t.Fatal(err)
	}
	for where, cursor := range map[string]string{"of the favorites": favorites.Next, "of another order": other, "with one key": short,
		"empty": "", "not base64": "!!", "cut": page.Next[:len(page.Next)-2], "with more": page.Next + "AA"} {
		_, err := svc.ListPlaylistItems(ctx, userA, p.ID, 2, &cursor)
		wantCode(t, "a cursor "+where, err, http.StatusBadRequest, "invalid_cursor")
	}
	// And the other lists refuse a cursor of this one.
	if _, err := svc.ListFavorites(ctx, userA, 1, &page.Next); err == nil {
		t.Fatal("the favorites took a cursor of a playlist")
	}

	// The item the cursor names, the second, is removed: the page after it
	// begins where it was.
	removed, err := svc.RemovePlaylistItem(ctx, userA, p.ID, added[1].ItemID, nil)
	if err != nil || removed.Revision != p.Revision+1 {
		t.Fatalf("removing an item: %+v, %v", removed, err)
	}
	next, err := svc.ListPlaylistItems(ctx, userA, p.ID, 2, &page.Next)
	if err != nil || len(next.Items) != 2 || next.Items[0].ID != added[2].ItemID || next.Items[1].ID != added[3].ItemID ||
		next.ETag != removed.ETag() || next.ItemCount != removed.ItemCount {
		t.Fatalf("the page after a removed item: %+v, %v", next, err)
	}
	// A cursor past the end is an empty last page.
	last, err := httpx.EncodeCursor(sortPosition, OrderAsc, httpx.IntKey(99), httpx.IDKey(added[0].ItemID))
	if err != nil {
		t.Fatal(err)
	}
	if end, err := svc.ListPlaylistItems(ctx, userA, p.ID, 2, &last); err != nil || len(end.Items) != 0 || end.Next != "" {
		t.Fatalf("a page past the end: %+v, %v", end, err)
	}
}

// §8.1: two clients change a playlist with the same entity tag at once: one
// change is made, and every other is 412 and changes nothing.
func TestSamePlaylistETagAtOnce(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 4)
	svc := New(st, time.Now)
	ctx := t.Context()
	p := newPlaylist(t, svc, userA, "race")
	p, added := appendTracks(t, svc, userA, p.ID, tracks...)
	tag := p.ETag()

	const clients = 8
	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		results = make([]error, clients)
	)
	for c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			switch c % 4 {
			case 0:
				_, results[c] = svc.MovePlaylistItem(ctx, userA, p.ID, added[c%len(added)].ItemID, (c+1)%len(added), &tag)
			case 1:
				_, _, results[c] = svc.AddPlaylistItems(ctx, userA, p.ID, tracks[:2], ptr(1), &tag)
			case 2:
				_, results[c] = svc.RemovePlaylistItem(ctx, userA, p.ID, added[c%len(added)].ItemID, &tag)
			default:
				_, results[c] = svc.UpdatePlaylist(ctx, userA, p.ID, fmt.Sprintf("client %d", c), "", &tag)
			}
		}()
	}
	close(start)
	wg.Wait()
	won := 0
	for c, err := range results {
		if err == nil {
			won++
			continue
		}
		wantCode(t, fmt.Sprintf("the client %d", c), err, http.StatusPreconditionFailed, CodePreconditionFailed)
	}
	got := stateOf(t, st, svc, userA, p.ID)
	if won != 1 || got.playlist.Revision != p.Revision+1 {
		t.Fatalf("%d clients won, and the revision went from %d to %d", won, p.Revision, got.playlist.Revision)
	}
	for i, it := range got.items {
		if it.position != i {
			t.Fatalf("the item %d is stored at %d", i, it.position)
		}
	}
}

// The playlists are changed and read at once: appends without If-Match all
// arrive, each block whole and in its order; every page a reader sees is
// one state, with dense positions; and an account that is deleted while it
// makes playlists leaves nothing, and gets no error but that it is gone.
func TestPlaylistsAtOnce(t *testing.T) {
	st := newStore(t)
	const leaving = "0199a5c0-0000-7000-8000-0000000000cc"
	tracks := playlistIndex(t, st, 6)
	putUsers(t, st, leaving)
	svc := New(st, time.Now)
	ctx := t.Context()
	p := newPlaylist(t, svc, userA, "shared by its clients")

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	run := func(f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(); err != nil {
				errs <- err
			}
		}()
	}
	const writers, rounds = 4, 9
	for w := range writers {
		// Each writer appends pairs of its own track, and sometimes moves
		// or removes with the tag it just got: that one may be stale.
		run(func() error {
			for round := range rounds {
				got, added, err := svc.AddPlaylistItems(ctx, userA, p.ID, []string{tracks[w], tracks[w]}, nil, nil)
				if err != nil {
					return fmt.Errorf("appending: %w", err)
				}
				if round%3 != 0 {
					continue
				}
				_, err = svc.MovePlaylistItem(ctx, userA, p.ID, added[0].ItemID, 0, ptr(got.ETag()))
				var refusal *httpx.Error
				if err != nil && (!errors.As(err, &refusal) || refusal.Code != CodePreconditionFailed) {
					return fmt.Errorf("moving: %w", err)
				}
			}
			return nil
		})
		run(func() error {
			for range 8 {
				if err := readDensePages(ctx, svc, userA, p.ID, 3+7*w); err != nil {
					return err
				}
				if _, err := svc.ListPlaylists(ctx, userA); err != nil {
					return err
				}
			}
			return nil
		})
	}
	run(func() error {
		for i := range 20 {
			created, err := svc.CreatePlaylist(ctx, leaving, fmt.Sprintf("leaving %d", i), "")
			var refusal *httpx.Error
			switch {
			case err == nil:
				_, _, err = svc.AddPlaylistItems(ctx, leaving, created.ID, tracks[:1], nil, nil)
				if err != nil && (!errors.As(err, &refusal) || refusal.Code != CodePlaylistNotFound) {
					return fmt.Errorf("adding for an account that is being deleted: %w", err)
				}
			case !errors.As(err, &refusal) || refusal.Status != http.StatusUnauthorized:
				return fmt.Errorf("creating for an account that is being deleted: %w", err)
			}
		}
		return nil
	})
	run(func() error {
		return st.WithWriteTx(ctx, func(q *store.Queries) error { return q.DeleteUser(ctx, leaving) })
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	got := stateOf(t, st, svc, userA, p.ID)
	if len(got.items) != writers*rounds*2 || got.playlist.ItemCount != len(got.items) {
		t.Fatalf("%d items, want %d", len(got.items), writers*rounds*2)
	}
	perTrack := map[string]int{}
	for i, it := range got.items {
		if it.position != i {
			t.Fatalf("the item %d is stored at %d", i, it.position)
		}
		perTrack[it.track]++
	}
	for w := range writers {
		if perTrack[tracks[w]] != rounds*2 {
			t.Fatalf("the writer %d left %d items, want %d", w, perTrack[tracks[w]], rounds*2)
		}
	}
	if list, err := svc.ListPlaylists(ctx, leaving); err != nil || len(list) != 0 {
		t.Fatalf("the playlists of the deleted account: %v, %v", list, err)
	}
	var left int
	err := st.Read(ctx, func(q *store.Queries) error {
		return q.Conn().QueryRowContext(ctx, `SELECT count(*) FROM playlists WHERE user_id = ?`, leaving).Scan(&left)
	})
	if err != nil || left != 0 {
		t.Fatalf("%d playlists of the deleted account, %v", left, err)
	}
}

// readDensePages reads the items of a playlist that is being changed: each
// page is one state of the playlist, so its positions follow one another,
// and the first page begins at 0.
func readDensePages(ctx context.Context, svc *Service, userID, id string, limit int) error {
	var after *string
	for pages := 0; pages <= 10000; pages++ {
		page, err := svc.ListPlaylistItems(ctx, userID, id, limit, after)
		if err != nil {
			return err
		}
		for i, it := range page.Items {
			switch {
			case after == nil && it.Position != i:
				return fmt.Errorf("limit %d: the first page has the position %d at %d", limit, it.Position, i)
			case it.Position != page.Items[0].Position+i:
				return fmt.Errorf("limit %d: the positions of a page are not dense: %d after %d", limit, it.Position, page.Items[0].Position)
			case it.Position >= page.ItemCount:
				return fmt.Errorf("limit %d: the position %d in a playlist of %d items", limit, it.Position, page.ItemCount)
			}
		}
		if page.Next == "" {
			return nil
		}
		after = &page.Next
	}
	return errors.New("no end")
}
