package catalog

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"vibrance/internal/search"
	"vibrance/internal/store"
)

// The search of DESIGN.md §10 as the catalog answers it. What a text finds
// and in which order is proved in internal/search; here, that what is found
// is shown as the catalog shows it everywhere else, and that a search reads
// one state of the index.

var allKinds = search.Kinds{Artists: true, Albums: true, Tracks: true}

// syncSearch writes the full-text rows of the albums and of their artists,
// as the scanner does in the transaction that changes them.
func syncSearch(ctx context.Context, q *store.Queries, albums ...testAlbum) error {
	for _, a := range albums {
		if err := errors.Join(search.SyncAlbum(ctx, q.Conn(), a.id), search.SyncArtist(ctx, q.Conn(), a.artist.id)); err != nil {
			return err
		}
	}
	return nil
}

func TestSearch(t *testing.T) {
	st := newStore(t)
	svc := New(st, time.Now)
	ctx := t.Context()
	miles := testArtist{id: "0199a5c0-0000-5000-8000-000000000001", name: "Miles Davis"}
	evans := testArtist{id: "0199a5c0-0000-5000-8000-000000000002", name: "Bill Evans"}
	kind := testAlbum{id: "0199a5c0-0000-7000-8000-000000000011", artist: miles, title: "Kind of Blue", year: 1959, firstSeen: 5, available: true}
	milestones := testAlbum{id: "0199a5c0-0000-7000-8000-000000000012", artist: miles, title: "Milestones", firstSeen: 6, available: true}
	waltz := testAlbum{id: "0199a5c0-0000-7000-8000-000000000013", artist: evans, title: "Waltz for Debby", year: 1962, firstSeen: 7, available: true}
	putAlbums(t, st, kind, milestones, waltz)
	soWhat := testTrack{id: "0199a5c0-0000-7000-8000-000000000021", disc: 1, no: 1, title: "So What", available: true}
	blue := testTrack{id: "0199a5c0-0000-7000-8000-000000000022", disc: 1, no: 2, title: "Blue in Green", available: true}
	putTracks(t, st, kind.id, soWhat, blue)
	putTracks(t, st, waltz.id, testTrack{id: "0199a5c0-0000-7000-8000-000000000023", disc: 1, no: 1, title: "Milestones", available: true})
	putFavorite(t, st, blue.id)
	if err := st.WithWriteTx(ctx, func(q *store.Queries) error { return syncSearch(ctx, q, kind, milestones, waltz) }); err != nil {
		t.Fatal(err)
	}

	// Each kind as its list and its detail show it.
	got, err := svc.Search(ctx, userA, "mil", allKinds, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Artists) != 1 || got.Artists[0] != (ArtistSummary{ArtistRef: ArtistRef{ID: miles.id, Name: "Miles Davis"}, AlbumCount: 2}) {
		t.Errorf("the artists: %s", show(got.Artists))
	}
	wantAlbum, err := svc.GetAlbum(ctx, userA, milestones.id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Albums) != 2 || show(got.Albums[0]) != show(wantAlbum.Album) || got.Albums[1].ID != kind.id {
		t.Errorf("the albums: %s", show(got.Albums))
	}
	if len(got.Tracks) != 1 || got.Tracks[0].Title != "Milestones" || got.Tracks[0].Album.ID != waltz.id {
		t.Errorf("the tracks: %s", show(got.Tracks))
	}
	// A track is found by the title of its album too.
	byAlbum, err := svc.Search(ctx, userA, "blue", allKinds, 10)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, tr := range byAlbum.Tracks {
		titles = append(titles, tr.Title)
		want, err := svc.GetTrack(ctx, userA, tr.ID)
		if err != nil {
			t.Fatal(err)
		}
		if show(tr) != show(want) {
			t.Errorf("the track %s: %s, and by itself %s", tr.Title, show(tr), show(want))
		}
	}
	if !slices.Equal(titles, []string{"Blue in Green", "So What"}) {
		t.Errorf("the tracks: %q", titles)
	}

	// The favorites are those of the user who asks.
	for user, want := range map[string]bool{userA: true, userB: false} {
		got, err := svc.Search(ctx, user, "green", allKinds, 10)
		if err != nil || len(got.Tracks) != 1 || got.Tracks[0].ID != blue.id || got.Tracks[0].Favorite != want {
			t.Errorf("as %s: %s, %v", user, show(got.Tracks), err)
		}
	}

	// The kinds and the limit; a kind that is not asked for is empty.
	got, err = svc.Search(ctx, userA, "mil", search.Kinds{Albums: true}, 1)
	if err != nil || len(got.Albums) != 1 || len(got.Artists) != 0 || len(got.Tracks) != 0 {
		t.Errorf("one album: %s, %v", show(got), err)
	}
	// A text without a word finds nothing, and is no error.
	for _, text := range []string{"", "?!", `"*"`} {
		got, err := svc.Search(ctx, userA, text, allKinds, 10)
		if err != nil || len(got.Artists)+len(got.Albums)+len(got.Tracks) != 0 {
			t.Errorf("%q: %s, %v", text, show(got), err)
		}
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := svc.Search(cancelled, userA, "mil", allKinds, 10); !errors.Is(err, context.Canceled) {
		t.Errorf("with a context that ended: %v", err)
	}
}

// Readers search while the scanner takes albums away and brings them back.
// The full-text rows and what they describe are read in one transaction,
// so a search never fails on a row that left meanwhile, and all it finds
// is available.
func TestSearchWhileTheIndexChanges(t *testing.T) {
	st := newStore(t)
	svc := New(st, time.Now)
	var albums []testAlbum
	for i := range 12 {
		albums = append(albums, testAlbum{
			id:     fmt.Sprintf("0199a5c0-0000-7000-8000-0000000001%02d", i),
			artist: testArtist{id: fmt.Sprintf("0199a5c0-0000-5000-8000-0000000002%02d", i%4), name: fmt.Sprintf("Common Artist %d", i%4)},
			title:  fmt.Sprintf("Common Album %d", i), firstSeen: int64(i), available: true,
		})
	}
	putAlbums(t, st, albums...)
	track := func(a testAlbum) store.UpsertTrackParams {
		return store.UpsertTrackParams{ID: "0199a5c0-0000-7000-8000-0000000003" + a.id[len(a.id)-2:], AlbumID: a.id,
			Fingerprint: "f", FpVersion: "v", Occurrence: 1, Disc: 1, No: 1, Title: "Common Track", Artist: a.artist.name,
			RelPath: "t.flac", FileSize: 1, FileMtimeNs: 1, FileSha256: "s", Codec: "flac", SampleRate: 44100, Channels: 2, UpdatedAt: 1}
	}
	// set makes an album and its track available or not, with their
	// full-text rows, in one transaction.
	set := func(ctx context.Context, a testAlbum) error {
		return st.WithWriteTx(ctx, func(q *store.Queries) error {
			if err := putAlbum(ctx, q, a); err != nil {
				return err
			}
			var err error
			if a.available {
				err = q.UpsertTrack(ctx, track(a))
			} else {
				err = q.SetTracksOfAlbumUnavailable(ctx, store.SetTracksOfAlbumUnavailableParams{UpdatedAt: 2, AlbumID: a.id})
			}
			return errors.Join(err, syncSearch(ctx, q, a))
		})
	}
	for _, a := range albums {
		if err := set(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	var writer sync.WaitGroup
	writer.Go(func() {
		for i := 0; ctx.Err() == nil; i++ {
			a := albums[i%len(albums)]
			a.available = (i/len(albums))%2 == 1
			if err := set(ctx, a); err != nil && ctx.Err() == nil {
				t.Error(err)
				return
			}
		}
	})
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 60 {
				got, err := svc.Search(ctx, userA, "common", allKinds, 50)
				if err != nil {
					t.Error(err)
					return
				}
				// An album found has its track found and its artist too:
				// the three tables are of one state.
				found := map[string]bool{}
				for _, tr := range got.Tracks {
					if !tr.Available {
						t.Errorf("a track that is not available is found: %s", show(tr))
						return
					}
					found[tr.Album.ID] = true
				}
				artists := map[string]int{}
				for _, a := range got.Artists {
					artists[a.ID] = a.AlbumCount
				}
				counts := map[string]int{}
				for _, a := range got.Albums {
					counts[a.Artist.ID]++
					if !found[a.ID] {
						t.Errorf("the album %s is found without its track", a.ID)
						return
					}
				}
				if len(got.Tracks) != len(got.Albums) || !equalCounts(artists, counts) {
					t.Errorf("%d tracks of %d albums; artists %v, albums by artist %v", len(got.Tracks), len(got.Albums), artists, counts)
					return
				}
			}
		})
	}
	readers.Wait()
	cancel()
	writer.Wait()
}

func equalCounts(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
