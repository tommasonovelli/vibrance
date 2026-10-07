package catalog

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/store"
)

// The playlists that hold a track, GET /tracks/{id}/playlists
// (docs/proposals/web-client-api.md B4), on a real database: those of the
// user that asks, never another's, the oldest first.

func TestListTrackPlaylists(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 3)
	track, other, gone := tracks[0], tracks[1], tracks[2]
	clock := &testClock{}
	svc := New(st, clock.now)
	ctx := t.Context()
	at := int64(1000)
	create := func(user, name string, items ...string) Playlist {
		t.Helper()
		at += 1000
		clock.set(at)
		p := newPlaylist(t, svc, user, name)
		if len(items) != 0 {
			p, _ = appendTracks(t, svc, user, p.ID, items...)
		}
		return p
	}
	// Created in this order, which is the order of the lists.
	twice := create(userA, "twice", track, other, track)
	_ = create(userA, "without", other)
	ofB := create(userB, "of B", track)
	create(userA, "empty")
	last := create(userA, "last", gone, track)
	refs := func(user, id string) []PlaylistRef {
		t.Helper()
		got, err := svc.ListTrackPlaylists(ctx, user, id)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatal("a nil list")
		}
		return got
	}
	ref := func(p Playlist) PlaylistRef { return PlaylistRef{ID: p.ID, Name: p.Name} }

	if got, want := refs(userA, track), []PlaylistRef{ref(twice), ref(last)}; !slices.Equal(got, want) {
		t.Fatalf("the playlists of A with the track:\n got %+v\nwant %+v", got, want)
	}
	if got, want := refs(userB, track), []PlaylistRef{ref(ofB)}; !slices.Equal(got, want) {
		t.Fatalf("the playlists of B with the track:\n got %+v\nwant %+v", got, want)
	}
	// A track that is not available is looked for all the same; an account
	// with no playlist, or none with the track, gets an empty list.
	setTrackUnavailable(t, st, gone)
	if got, want := refs(userA, gone), []PlaylistRef{ref(last)}; !slices.Equal(got, want) {
		t.Fatalf("the playlists of A with a track that is not available: %+v, want %+v", got, want)
	}
	if got := refs(userB, other); len(got) != 0 {
		t.Fatalf("B has no playlist with the track, and got %+v", got)
	}
	if got := refs(uuid.NewString(), track); len(got) != 0 {
		t.Fatalf("a user that is nobody got %+v", got)
	}
	// The list follows the changes: a renamed playlist has its new name, and
	// one whose items of the track are removed is not listed.
	clock.set(at + 1000)
	if _, err := svc.UpdatePlaylist(ctx, userA, last.ID, "renamed", "", nil); err != nil {
		t.Fatal(err)
	}
	items := allItems(t, svc, userA, twice.ID, 50)
	for _, it := range items {
		if it.Track.ID == track {
			if _, err := svc.RemovePlaylistItem(ctx, userA, twice.ID, it.ID, nil); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if got, want := refs(userA, track), []PlaylistRef{ref(twice), {ID: last.ID, Name: "renamed"}}; !slices.Equal(got, want) {
		t.Fatalf("one item of two removed: %+v, want %+v", got, want)
	}
	for _, it := range allItems(t, svc, userA, twice.ID, 50) {
		if it.Track.ID == track {
			if _, err := svc.RemovePlaylistItem(ctx, userA, twice.ID, it.ID, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := svc.DeletePlaylist(ctx, userB, ofB.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := refs(userA, track), []PlaylistRef{{ID: last.ID, Name: "renamed"}}; !slices.Equal(got, want) {
		t.Fatalf("every item of the track removed: %+v, want %+v", got, want)
	}
	if got := refs(userB, track); len(got) != 0 {
		t.Fatalf("a deleted playlist is listed: %+v", got)
	}
	// A track that does not exist: 404 track_not_found.
	_, err := svc.ListTrackPlaylists(ctx, userA, uuid.NewString())
	wantNotFound(t, "a track that does not exist", err, CodeTrackNotFound)
}

// A user with the most playlists, each with the track, gets them all, the
// oldest first, and another user none of them.
func TestListTrackPlaylistsOfAUserWithEveryPlaylist(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 1)
	svc := New(st, time.Now)
	ctx := t.Context()
	// Written as CreatePlaylist and AddPlaylistItems would, in one
	// transaction: 500 changes through the service take seconds.
	var want []PlaylistRef
	err := st.WithWriteTx(ctx, func(q *store.Queries) error {
		for i := range MaxPlaylists {
			ref := PlaylistRef{ID: uuid.NewString(), Name: fmt.Sprintf("Playlist %d", i)}
			if _, err := q.CreatePlaylist(ctx, store.CreatePlaylistParams{ID: ref.ID, Name: ref.Name, CreatedAt: int64(1000 + i), UserID: userA}); err != nil {
				return err
			}
			if err := q.InsertPlaylistItem(ctx, store.InsertPlaylistItemParams{ID: uuid.NewString(), PlaylistID: ref.ID, TrackID: tracks[0],
				Position: 0, AddedAt: 1}); err != nil {
				return err
			}
			want = append(want, ref)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ListTrackPlaylists(ctx, userA, tracks[0])
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("%d playlists of %d, in order: %v (%v)", len(got), len(want), slices.Equal(got, want), err)
	}
	if got, err := svc.ListTrackPlaylists(ctx, userB, tracks[0]); err != nil || len(got) != 0 {
		t.Fatalf("another user: %+v, %v", got, err)
	}
}
