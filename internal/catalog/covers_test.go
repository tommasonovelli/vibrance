package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/store"
)

// The covers of a playlist (docs/proposals/web-client-api.md A2, step W2):
// at most four, of distinct albums, from the items whose track is
// available, in the order of the items, skipping the albums without a
// cover; on every answer that carries a playlist.

// coverOfName is a SHA-256 for a cover of a test.
func coverOfName(name string) string {
	sum := sha256.Sum256([]byte("cover " + name))
	return hex.EncodeToString(sum[:])
}

// coverAlbum is an album of a test of the covers, with its tracks.
type coverAlbum struct {
	album  testAlbum
	tracks []string
}

// coverIndex writes the accounts userA and userB and one available album
// for each name, of two available tracks of one second each, with a cover
// unless the name is in coverless. The artist of every album is the same.
func coverIndex(t *testing.T, st *store.Store, coverless []string, names ...string) map[string]coverAlbum {
	t.Helper()
	putUsers(t, st, userA, userB)
	artist := testArtist{id: uuid.NewString(), name: "Artist"}
	albums := make(map[string]coverAlbum, len(names))
	for i, name := range names {
		a := testAlbum{id: uuid.NewString(), artist: artist, title: name, firstSeen: int64(i + 1), available: true,
			cover: coverOfName(name)}
		for _, c := range coverless {
			if c == name {
				a.cover = ""
			}
		}
		putAlbums(t, st, a)
		tracks := []string{uuid.NewString(), uuid.NewString()}
		putTracks(t, st, a.id, testTrack{tracks[0], 1, 1, name + " 1", true}, testTrack{tracks[1], 1, 2, name + " 2", true})
		albums[name] = coverAlbum{album: a, tracks: tracks}
	}
	return albums
}

// coversOf is the list of the covers of the albums named, in that order.
func coversOf(albums map[string]coverAlbum, names ...string) []AlbumCover {
	covers := make([]AlbumCover, 0, len(names))
	for _, name := range names {
		a := albums[name].album
		covers = append(covers, AlbumCover{AlbumID: a.id, Hash: a.cover})
	}
	return covers
}

func wantCovers(t *testing.T, where string, p Playlist, want []AlbumCover) {
	t.Helper()
	if p.Covers == nil || !reflect.DeepEqual(p.Covers, want) {
		t.Fatalf("%s: the covers are %+v, want %+v", where, p.Covers, want)
	}
}

// The rules, one by one, and on every answer that carries a playlist.
func TestPlaylistCovers(t *testing.T) {
	st := newStore(t)
	albums := coverIndex(t, st, []string{"Coverless"}, "Alpha", "Bravo", "Coverless", "Delta", "Echo", "Foxtrot", "Gone")
	svc := New(st, time.Now)
	ctx := t.Context()
	track := func(name string, n int) string { return albums[name].tracks[n-1] }

	// An empty playlist has an empty list, never null.
	p := newPlaylist(t, svc, userA, "covers")
	wantCovers(t, "a new playlist", p, []AlbumCover{})

	// The album of a track that is not available gives nothing until another
	// track of it does; an album without a cover gives nothing; an album
	// gives one cover however many items it has; at most four.
	p, _ = appendTracks(t, svc, userA, p.ID, track("Coverless", 1), track("Alpha", 2), track("Gone", 1))
	setTrackUnavailable(t, st, track("Alpha", 2))
	gone := albums["Gone"].album
	err := st.WithWriteTx(ctx, func(q *store.Queries) error {
		if err := q.SetTracksOfAlbumUnavailable(ctx, store.SetTracksOfAlbumUnavailableParams{UpdatedAt: 3, AlbumID: gone.id}); err != nil {
			return err
		}
		return q.SetAlbumUnavailable(ctx, store.SetAlbumUnavailableParams{UpdatedAt: 3, ID: gone.id})
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err = svc.GetPlaylist(ctx, userA, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantCovers(t, "only items without a cover", p, []AlbumCover{})

	p, added := appendTracks(t, svc, userA, p.ID, track("Bravo", 1), track("Alpha", 1), track("Bravo", 2), track("Bravo", 1),
		track("Delta", 2), track("Coverless", 2), track("Echo", 1), track("Foxtrot", 1))
	want := coversOf(albums, "Bravo", "Alpha", "Delta", "Echo")
	wantCovers(t, "addPlaylistItems", p, want)
	got, err := svc.GetPlaylist(ctx, userA, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantCovers(t, "getPlaylist", got, want)
	list, err := svc.ListPlaylists(ctx, userA)
	if err != nil || len(list) != 1 {
		t.Fatalf("the list: %+v, %v", list, err)
	}
	wantCovers(t, "listPlaylists", list[0], want)
	if p, err = svc.UpdatePlaylist(ctx, userA, p.ID, "renamed", "", nil); err != nil {
		t.Fatal(err)
	}
	wantCovers(t, "updatePlaylist", p, want)

	// The order is the order of the items: a move changes it.
	foxtrot := added[len(added)-1].ItemID
	if p, err = svc.MovePlaylistItem(ctx, userA, p.ID, foxtrot, 0, ptr(p.ETag())); err != nil {
		t.Fatal(err)
	}
	wantCovers(t, "movePlaylistItem", p, coversOf(albums, "Foxtrot", "Bravo", "Alpha", "Delta"))
	// The first item of Bravo goes: the next one keeps its place.
	if p, err = svc.RemovePlaylistItem(ctx, userA, p.ID, added[0].ItemID, nil); err != nil {
		t.Fatal(err)
	}
	wantCovers(t, "removePlaylistItem", p, coversOf(albums, "Foxtrot", "Alpha", "Bravo", "Delta"))

	// A new cover of an album is shown at once, without a new revision; an
	// album that goes leaves its place to the next one.
	alpha := albums["Alpha"]
	alpha.album.cover = coverOfName("Alpha, again")
	albums["Alpha"] = alpha
	putAlbums(t, st, alpha.album)
	putTracks(t, st, alpha.album.id, testTrack{alpha.tracks[0], 1, 1, "Alpha 1", true}, testTrack{alpha.tracks[1], 1, 2, "Alpha 2", false})
	if got, err = svc.GetPlaylist(ctx, userA, p.ID); err != nil || got.Revision != p.Revision {
		t.Fatalf("after a new cover: %+v, %v", got, err)
	}
	wantCovers(t, "a new cover", got, coversOf(albums, "Foxtrot", "Alpha", "Bravo", "Delta"))
	setTrackUnavailable(t, st, track("Foxtrot", 1))
	if got, err = svc.GetPlaylist(ctx, userA, p.ID); err != nil {
		t.Fatal(err)
	}
	wantCovers(t, "a track gone", got, coversOf(albums, "Alpha", "Bravo", "Delta", "Echo"))

	// Fewer than four.
	few := newPlaylist(t, svc, userA, "few")
	few, _ = appendTracks(t, svc, userA, few.ID, track("Delta", 1), track("Delta", 2), track("Echo", 2), track("Delta", 1))
	wantCovers(t, "two albums", few, coversOf(albums, "Delta", "Echo"))

	// The covers of the playlists of the other user are their own.
	other := newPlaylist(t, svc, userB, "other")
	other, _ = appendTracks(t, svc, userB, other.ID, track("Echo", 1))
	wantCovers(t, "the playlist of B", other, coversOf(albums, "Echo"))
	if list, err = svc.ListPlaylists(ctx, userA); err != nil || len(list) != 2 {
		t.Fatalf("the list: %+v, %v", list, err)
	}
	wantCovers(t, "the list, first", list[0], coversOf(albums, "Alpha", "Bravo", "Delta", "Echo"))
	wantCovers(t, "the list, second", list[1], coversOf(albums, "Delta", "Echo"))
}

// wantCoversOf is the oracle of the covers: the rules written again from
// the proposal, on the items of a playlist as track ids.
func wantCoversOf(items []string, albumOf map[string]testAlbum, available map[string]bool) []AlbumCover {
	covers := []AlbumCover{}
	seen := map[string]bool{}
	for _, track := range items {
		a := albumOf[track]
		if !available[track] || a.cover == "" || seen[a.id] || len(covers) == MaxPlaylistCovers {
			continue
		}
		seen[a.id] = true
		covers = append(covers, AlbumCover{AlbumID: a.id, Hash: a.cover})
	}
	return covers
}

// Random playlists on a random index, against the oracle: albums with and
// without a cover, available and not, tracks available and not, the same
// track or album many times.
func TestPlaylistCoversAgainstAModel(t *testing.T) {
	for seed := range uint64(4) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			r := rand.New(rand.NewPCG(seed, 2))
			st := newStore(t)
			putUsers(t, st, userA, userB)
			svc := New(st, time.Now)
			artist := testArtist{id: uuid.NewString(), name: "Artist"}
			albumOf := map[string]testAlbum{}
			available := map[string]bool{}
			var tracks []string
			for i := range 10 {
				a := testAlbum{id: uuid.NewString(), artist: artist, title: fmt.Sprint("Album ", i), firstSeen: 1, available: r.IntN(5) != 0}
				if r.IntN(3) != 0 {
					a.cover = coverOfName(fmt.Sprint(seed, i))
				}
				putAlbums(t, st, a)
				var rows []testTrack
				for n := range 3 {
					id := uuid.NewString()
					// The tracks of an album that is not available are not
					// either, as the scanner leaves them.
					rows = append(rows, testTrack{id, 1, int64(n + 1), "Track", a.available && r.IntN(4) != 0})
					albumOf[id], available[id] = a, rows[n].available
					tracks = append(tracks, id)
				}
				putTracks(t, st, a.id, rows...)
			}
			// Tracks are added while available, as the API demands, and
			// become unavailable afterwards.
			err := st.WithWriteTx(t.Context(), func(q *store.Queries) error {
				_, err := q.Conn().ExecContext(t.Context(), `UPDATE tracks SET available = 1`)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string][]AlbumCover{}
			for range 30 {
				items := make([]string, r.IntN(25))
				for i := range items {
					items[i] = tracks[r.IntN(len(tracks))]
					if r.IntN(3) == 0 && i > 0 {
						items[i] = items[r.IntN(i)]
					}
				}
				p := newPlaylist(t, svc, userA, "random")
				if len(items) > 0 {
					appendTracks(t, svc, userA, p.ID, items...)
				}
				want[p.ID] = wantCoversOf(items, albumOf, available)
			}
			err = st.WithWriteTx(t.Context(), func(q *store.Queries) error {
				for id, ok := range available {
					if !ok {
						if err := q.SetTrackUnavailable(t.Context(), store.SetTrackUnavailableParams{UpdatedAt: 2, ID: id}); err != nil {
							return err
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			list, err := svc.ListPlaylists(t.Context(), userA)
			if err != nil || len(list) != len(want) {
				t.Fatalf("%d playlists, %v", len(list), err)
			}
			for _, p := range list {
				wantCovers(t, "listed", p, want[p.ID])
				got, err := svc.GetPlaylist(t.Context(), userA, p.ID)
				if err != nil {
					t.Fatal(err)
				}
				wantCovers(t, "read", got, want[p.ID])
			}
		})
	}
}

// The covers are read in the transaction of the rest of the playlist: while
// a track comes and goes, every answer has the covers of the duration it
// has, never those of the other state.
func TestPlaylistCoversAreOneState(t *testing.T) {
	st := newStore(t)
	albums := coverIndex(t, st, nil, "Alpha", "Bravo", "Charlie", "Delta", "Echo")
	svc := New(st, time.Now)
	p := newPlaylist(t, svc, userA, "one state")
	p, _ = appendTracks(t, svc, userA, p.ID, albums["Alpha"].tracks[0], albums["Bravo"].tracks[0], albums["Charlie"].tracks[0],
		albums["Delta"].tracks[0], albums["Echo"].tracks[0])
	states := map[int64][]AlbumCover{
		5000: coversOf(albums, "Alpha", "Bravo", "Charlie", "Delta"),
		4000: coversOf(albums, "Bravo", "Charlie", "Delta", "Echo"),
	}
	ctx, stop := context.WithCancel(t.Context())
	var writer sync.WaitGroup
	writer.Go(func() {
		for i := 0; ctx.Err() == nil; i++ {
			err := st.WithWriteTx(ctx, func(q *store.Queries) error {
				_, err := q.Conn().ExecContext(ctx, `UPDATE tracks SET available = ? WHERE id = ?`, i%2, albums["Alpha"].tracks[0])
				return err
			})
			if err != nil && ctx.Err() == nil {
				t.Error(err)
				return
			}
		}
	})
	check := func(where string, got Playlist) error {
		if want, ok := states[got.DurationMS]; !ok || !reflect.DeepEqual(got.Covers, want) {
			return fmt.Errorf("%s: %d ms with the covers %+v", where, got.DurationMS, got.Covers)
		}
		return nil
	}
	var readers sync.WaitGroup
	errs := make(chan error, 4)
	seen := make(chan int64, 4*150)
	for range 4 {
		readers.Go(func() {
			for range 150 {
				got, err := svc.GetPlaylist(ctx, userA, p.ID)
				if err == nil {
					seen <- got.DurationMS
					err = check("read", got)
				}
				if err == nil {
					var list []Playlist
					if list, err = svc.ListPlaylists(ctx, userA); err == nil {
						err = check("listed", list[0])
					}
				}
				if err != nil {
					errs <- err
					return
				}
			}
		})
	}
	readers.Wait()
	stop()
	writer.Wait()
	close(errs)
	close(seen)
	for err := range errs {
		t.Error(err)
	}
	// Both states were seen, or the test proved nothing.
	both := map[int64]bool{}
	for d := range seen {
		both[d] = true
	}
	if len(both) != 2 {
		t.Errorf("the readers saw only %v", both)
	}
}
