package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/names"
	"vibrance/internal/store"
)

// The details of DESIGN.md §8.2: an artist with its albums, an album with
// its tracks, a track with its album, available or not.

const (
	userA = "0199a5c0-0000-7000-8000-0000000000aa"
	userB = "0199a5c0-0000-7000-8000-0000000000bb"
)

// testTrack is a track of an album of a test.
type testTrack struct {
	id        string
	disc, no  int64
	title     string
	available bool
}

func putTracks(t *testing.T, st *store.Store, albumID string, tracks ...testTrack) {
	t.Helper()
	ctx := t.Context()
	err := st.WithWriteTx(ctx, func(q *store.Queries) error {
		for i, tr := range tracks {
			err := q.UpsertTrack(ctx, store.UpsertTrackParams{
				ID: tr.id, AlbumID: albumID, Fingerprint: tr.id, FpVersion: "v", Occurrence: 1,
				Disc: tr.disc, No: tr.no, Title: tr.title, Artist: "Track Artist", RelPath: tr.id + ".flac",
				FileSize: int64(1000 + i), FileMtimeNs: 1, FileSha256: "s", Codec: "flac", SampleRate: 44100, Channels: 2,
				BitDepth: sql.NullInt64{Int64: 16, Valid: true}, DurationMs: sql.NullInt64{Int64: 1000, Valid: true},
				UpdatedAt: 1, TitleKey: sortKey(tr.title), ArtistKey: sortKey("Track Artist"), FirstSeenAt: 1,
			})
			if err != nil {
				return err
			}
			if !tr.available {
				if err := q.SetTrackUnavailable(ctx, store.SetTrackUnavailableParams{UpdatedAt: 2, ID: tr.id}); err != nil {
					return err
				}
			}
		}
		return q.UpdateAlbumCounters(ctx, albumID)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// putFavorite writes two accounts and makes trackID a favorite of the
// first.
func putFavorite(t *testing.T, st *store.Store, trackID string) {
	t.Helper()
	ctx := t.Context()
	err := st.WithWriteTx(ctx, func(q *store.Queries) error {
		for _, id := range []string{userA, userB} {
			if err := q.CreateUser(ctx, store.CreateUserParams{ID: id, Username: "u" + id[len(id)-2:], PasswordHash: "x",
				Role: "user", CreatedAt: 1}); err != nil {
				return err
			}
		}
		return q.AddFavorite(ctx, store.AddFavoriteParams{UserID: userA, TrackID: trackID, CreatedAt: 1})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// An album lists its available tracks by disc and number, whatever order
// they were indexed in; counts its discs on them; and says which tracks are
// favorites of the user who asks.
func TestGetAlbum(t *testing.T) {
	st := newStore(t)
	artist := testArtist{id: uuid.NewString(), name: "Miles Davis"}
	album := testAlbum{id: uuid.NewString(), artist: artist, title: "Kind of Blue", year: 1959, firstSeen: 1727000000123, available: true}
	putAlbums(t, st, album)
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	putTracks(t, st, album.id,
		testTrack{ids[0], 2, 1, "Disc two", true},
		testTrack{ids[1], 1, 10, "Ten", true},
		testTrack{ids[2], 1, 2, "Two", true},
		testTrack{ids[3], 3, 1, "Gone", false},
		testTrack{ids[4], 1, 1, "One", true},
	)
	putFavorite(t, st, ids[2])
	svc := New(st, time.Now)

	d, err := svc.GetAlbum(t.Context(), userA, album.id)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, tr := range d.Tracks {
		titles = append(titles, tr.Title)
		if tr.Favorite != (tr.ID == ids[2]) || !tr.Available || tr.Album.ID != album.id || tr.Album.Title != album.title {
			t.Errorf("track %+v", tr)
		}
	}
	if want := []string{"One", "Two", "Ten", "Disc two"}; !slices.Equal(titles, want) {
		t.Fatalf("tracks %q, want %q", titles, want)
	}
	if d.DiscCount != 2 || d.TrackCount != 4 || d.DurationMS != 4000 || *d.Year != 1959 || d.Genre != nil || d.CoverHash != "" ||
		d.Artist != (ArtistRef{ID: artist.id, Name: artist.name}) || !d.AddedAt.Equal(time.UnixMilli(1727000000123)) || d.Compilation {
		t.Fatalf("album %+v", d.Album)
	}
	other, err := svc.GetAlbum(t.Context(), userB, album.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range other.Tracks {
		if tr.Favorite {
			t.Fatalf("a favorite of another user: %+v", tr)
		}
	}

	_, err = svc.GetAlbum(t.Context(), userA, uuid.NewString())
	wantNotFound(t, "an album that does not exist", err, CodeAlbumNotFound)
	album.available = false
	putAlbums(t, st, album)
	_, err = svc.GetAlbum(t.Context(), userA, album.id)
	wantNotFound(t, "an album that is not available", err, CodeAlbumNotFound)
}

// A track is read available or not, with its album as it was last known.
func TestGetTrack(t *testing.T) {
	st := newStore(t)
	artist := testArtist{id: uuid.NewString(), name: "Artist"}
	album := testAlbum{id: uuid.NewString(), artist: artist, title: "Album", firstSeen: 1, available: true}
	putAlbums(t, st, album)
	present, gone := uuid.NewString(), uuid.NewString()
	putTracks(t, st, album.id, testTrack{present, 1, 1, "Present", true}, testTrack{gone, 1, 2, "Gone", false})
	if err := st.WithWriteTx(t.Context(), func(q *store.Queries) error {
		_, err := q.Conn().ExecContext(t.Context(), `UPDATE tracks SET lyrics_rel = 'x.lrc', lyrics_sha256 = 's',
			rg_track_gain = -6.5, genre = 'Jazz', bit_depth = NULL, bitrate = 320000 WHERE id = ?`, present)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	putFavorite(t, st, present)
	svc := New(st, time.Now)

	tr, err := svc.GetTrack(t.Context(), userA, present)
	if err != nil {
		t.Fatal(err)
	}
	want := Track{ID: present, Title: "Present", Artist: "Track Artist",
		Album: AlbumRef{ID: album.id, Title: "Album", Artist: ArtistRef{ID: artist.id, Name: "Artist"}},
		Disc:  1, Number: 1, DurationMS: ptr(int64(1000)), Genre: ptr("Jazz"),
		Format:    Format{Codec: "flac", SampleRate: 44100, Channels: 2, Bitrate: ptr(320000), Size: 1000},
		HasLyrics: true, ReplayGain: &ReplayGain{TrackGainDB: ptr(-6.5)}, Available: true, Favorite: true}
	if !reflect.DeepEqual(tr, want) {
		t.Fatalf("track\n got %s\nwant %s", show(tr), show(want))
	}
	if tr, err = svc.GetTrack(t.Context(), userB, present); err != nil || tr.Favorite {
		t.Fatalf("for another user: %+v, %v", tr, err)
	}

	// Not available, and its album neither: the last data known.
	album.available = false
	putAlbums(t, st, album)
	tr, err = svc.GetTrack(t.Context(), userA, gone)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Available || tr.Title != "Gone" || tr.Album.Title != "Album" || tr.ReplayGain != nil || tr.HasLyrics || tr.Favorite {
		t.Fatalf("an unavailable track: %+v", tr)
	}
	_, err = svc.GetTrack(t.Context(), userA, uuid.NewString())
	wantNotFound(t, "a track that does not exist", err, CodeTrackNotFound)
}

// An artist shows its available albums by year (none last), then title; an
// artist without one is not found, like one that does not exist.
func TestGetArtist(t *testing.T) {
	st := newStore(t)
	artist := testArtist{id: names.ArtistID("Artist"), name: "Artist"}
	other := testArtist{id: names.ArtistID("Other"), name: "Other"}
	alone := testArtist{id: names.ArtistID("Alone"), name: "Alone"}
	putAlbums(t, st,
		testAlbum{id: uuid.NewString(), artist: artist, title: "No year", firstSeen: 1, available: true},
		testAlbum{id: uuid.NewString(), artist: artist, title: "B", year: 2000, firstSeen: 1, available: true},
		testAlbum{id: uuid.NewString(), artist: artist, title: "A", year: 2000, firstSeen: 1, available: true},
		testAlbum{id: uuid.NewString(), artist: artist, title: "Early", year: 1990, firstSeen: 1, available: true},
		testAlbum{id: uuid.NewString(), artist: artist, title: "Gone", year: 1980, firstSeen: 1, available: false},
		testAlbum{id: uuid.NewString(), artist: other, title: "Theirs", year: 1995, firstSeen: 1, available: true},
		testAlbum{id: uuid.NewString(), artist: alone, title: "Gone too", firstSeen: 1, available: false},
	)
	svc := New(st, time.Now)
	d, err := svc.GetArtist(t.Context(), artist.id)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, a := range d.Albums {
		titles = append(titles, a.Title)
		if a.Artist != d.ArtistRef {
			t.Errorf("album %+v of artist %+v", a, d.ArtistRef)
		}
	}
	if want := []string{"Early", "A", "B", "No year"}; !slices.Equal(titles, want) || d.ArtistRef != (ArtistRef{ID: artist.id, Name: "Artist"}) {
		t.Fatalf("%+v: albums %q, want %q", d.ArtistRef, titles, want)
	}
	_, err = svc.GetArtist(t.Context(), alone.id)
	wantNotFound(t, "an artist without an available album", err, CodeArtistNotFound)
	_, err = svc.GetArtist(t.Context(), uuid.NewString())
	wantNotFound(t, "an artist that does not exist", err, CodeArtistNotFound)
}

// A read whose context is over is not a missing row: it is the end of the
// context, and never a 404.
func TestReadsAfterTheContextEnds(t *testing.T) {
	svc := New(newStore(t), time.Now)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for name, read := range map[string]func() error{
		"GetArtist": func() error { _, err := svc.GetArtist(ctx, uuid.NewString()); return err },
		"GetAlbum":  func() error { _, err := svc.GetAlbum(ctx, userA, uuid.NewString()); return err },
		"GetTrack":  func() error { _, err := svc.GetTrack(ctx, userA, uuid.NewString()); return err },
		"Search":    func() error { _, err := svc.Search(ctx, userA, "a", allKinds, 1); return err },
		"ListArtists": func() error {
			_, err := svc.ListArtists(ctx, 1, nil)
			return err
		},
		"ListAlbums": func() error {
			_, err := svc.ListAlbums(ctx, AlbumQuery{Sort: SortTitle, Order: OrderAsc, Limit: 1})
			return err
		},
	} {
		if err := read(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: %v, want the end of the context", name, err)
		}
	}
}

// Readers page through the lists while the scanner changes which albums
// are available: a list read across changes never shows an album twice,
// and its pages follow each other in the order of the list, because each
// page starts strictly after the key where the last one ended.
func TestPagesWhileTheIndexChanges(t *testing.T) {
	albums, _ := randomIndex(11, 80)
	st := newStore(t)
	putAlbums(t, st, albums...)
	svc := New(st, time.Now)
	byID := map[string]testAlbum{}
	for _, a := range albums {
		byID[a.id] = a
	}

	ctx, cancel := context.WithCancel(t.Context())
	var writer sync.WaitGroup
	writer.Go(func() {
		for i := 0; ctx.Err() == nil; i++ {
			a := albums[i%len(albums)]
			a.available = i%2 == 0
			err := st.WithWriteTx(ctx, func(q *store.Queries) error { return putAlbum(ctx, q, a) })
			if err != nil && ctx.Err() == nil {
				t.Error(err)
				return
			}
		}
	})
	var readers sync.WaitGroup
	for _, sort := range []string{SortTitle, SortArtist, SortYear, SortAdded} {
		for _, order := range []string{OrderAsc, OrderDesc} {
			readers.Go(func() {
				for range 5 {
					ids, err := readAlbums(ctx, svc, AlbumQuery{Sort: sort, Order: order, Limit: 3}, false)
					if err != nil {
						t.Error(err)
						return
					}
					less := albumOrderOf(sort)
					for i := 1; i < len(ids); i++ {
						c := less(byID[ids[i-1]], byID[ids[i]])
						if order == OrderDesc {
							c = -c
						}
						if c >= 0 {
							t.Errorf("%s %s: %s then %s", sort, order, ids[i-1], ids[i])
							return
						}
					}
				}
			})
		}
	}
	readers.Wait()
	cancel()
	writer.Wait()
}

// show is v as JSON, to read the values behind its pointers.
func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(b)
}
