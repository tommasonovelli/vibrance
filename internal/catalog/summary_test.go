package catalog

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/store"
)

// The summaries of the catalog and of the favorites
// (docs/proposals/web-client-api.md A3, A4, step W2): what is not available
// is not counted in the catalog; the favorites count every favorite and add
// up the durations of the available ones, like a playlist (§8.6).

// putTrackOfDuration writes an available track of an album with a duration,
// or none when ms is 0, and counts the album again, as the indexer does.
func putTrackOfDuration(t *testing.T, st *store.Store, albumID string, no int64, ms int64) string {
	t.Helper()
	ctx := t.Context()
	id := uuid.NewString()
	err := st.WithWriteTx(ctx, func(q *store.Queries) error {
		err := q.UpsertTrack(ctx, store.UpsertTrackParams{
			ID: id, AlbumID: albumID, Fingerprint: id, FpVersion: "v", Occurrence: 1, Disc: 1, No: no, Title: "Track",
			Artist: "Artist", RelPath: id + ".flac", FileSize: 1, FileMtimeNs: 1, FileSha256: "s", Codec: "flac",
			SampleRate: 44100, Channels: 2, DurationMs: sql.NullInt64{Int64: ms, Valid: ms != 0}, UpdatedAt: 1,
			TitleKey: sortKey("Track"), ArtistKey: sortKey("Artist"), FirstSeenAt: 1,
		})
		if err != nil {
			return err
		}
		return q.UpdateAlbumCounters(ctx, albumID)
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// summaryIndex writes three artists: x with two available albums, y with
// one available album and one that is not, z with only an album that is
// not available. It returns the tracks by album, in order; the available
// tracks of the available albums last 1, 2, 4, 8 and 16 seconds and one
// has no known duration.
func summaryIndex(t *testing.T, st *store.Store) map[string][]string {
	t.Helper()
	x := testArtist{id: uuid.NewString(), name: "X"}
	y := testArtist{id: uuid.NewString(), name: "Y"}
	z := testArtist{id: uuid.NewString(), name: "Z"}
	albums := []testAlbum{
		{id: uuid.NewString(), artist: x, title: "X1", firstSeen: 1, available: true},
		{id: uuid.NewString(), artist: x, title: "X2", firstSeen: 1, available: true},
		{id: uuid.NewString(), artist: y, title: "Y1", firstSeen: 1, available: true},
		{id: uuid.NewString(), artist: y, title: "Y2", firstSeen: 1, available: true},
		{id: uuid.NewString(), artist: z, title: "Z1", firstSeen: 1, available: true},
	}
	putAlbums(t, st, albums...)
	durations := [][]int64{{1000, 2000}, {4000, 0}, {8000, 16000, 32000}, {64000}, {128000}}
	tracks := map[string][]string{}
	for i, a := range albums {
		for n, ms := range durations[i] {
			tracks[a.title] = append(tracks[a.title], putTrackOfDuration(t, st, a.id, int64(n+1), ms))
		}
	}
	// One track of Y1 goes; Y2 and Z1 go, with their tracks, as the scanner
	// does when a folder is gone.
	setTrackUnavailable(t, st, tracks["Y1"][2])
	ctx := t.Context()
	err := st.WithWriteTx(ctx, func(q *store.Queries) error {
		for _, a := range albums[3:] {
			if err := q.SetTracksOfAlbumUnavailable(ctx, store.SetTracksOfAlbumUnavailableParams{UpdatedAt: 2, AlbumID: a.id}); err != nil {
				return err
			}
			if err := q.SetAlbumUnavailable(ctx, store.SetAlbumUnavailableParams{UpdatedAt: 2, ID: a.id}); err != nil {
				return err
			}
			if err := q.UpdateAlbumCounters(ctx, a.id); err != nil {
				return err
			}
		}
		return q.UpdateAlbumCounters(ctx, albums[2].id)
	})
	if err != nil {
		t.Fatal(err)
	}
	return tracks
}

func TestCatalogSummary(t *testing.T) {
	st := newStore(t)
	svc := New(st, time.Now)
	ctx := t.Context()
	if got, err := svc.GetCatalogSummary(ctx); err != nil || got != (CatalogSummary{}) {
		t.Fatalf("an empty index: %+v, %v", got, err)
	}
	summaryIndex(t, st)
	want := CatalogSummary{Artists: 2, Albums: 3, Tracks: 6, DurationMS: 1000 + 2000 + 4000 + 8000 + 16000}
	got, err := svc.GetCatalogSummary(ctx)
	if err != nil || got != want {
		t.Fatalf("the summary is %+v, %v; want %+v", got, err, want)
	}

	// The counts are those of the lists, and the duration the sum of the
	// tracks they list.
	artists, err := svc.ListArtists(ctx, 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	albums := allAlbums(t, svc, AlbumQuery{Sort: SortTitle, Order: OrderAsc, Limit: 200})
	tracks, err := svc.ListTracks(ctx, TrackQuery{UserID: userA, Sort: SortTitle, Order: OrderAsc, Limit: 200})
	if err != nil || tracks.Next != "" || artists.Next != "" {
		t.Fatal(err)
	}
	var ms int64
	for _, tr := range tracks.Tracks {
		if tr.DurationMS != nil {
			ms += *tr.DurationMS
		}
	}
	if lists := (CatalogSummary{Artists: len(artists.Artists), Albums: len(albums), Tracks: len(tracks.Tracks), DurationMS: ms}); lists != got {
		t.Fatalf("the lists say %+v, the summary %+v", lists, got)
	}
}

func TestFavoritesSummary(t *testing.T) {
	st := newStore(t)
	svc := New(st, time.Now)
	ctx := t.Context()
	putUsers(t, st, userA, userB)
	tracks := summaryIndex(t, st)
	if got, err := svc.GetFavoritesSummary(ctx, userA); err != nil || got != (FavoritesSummary{}) {
		t.Fatalf("no favorites: %+v, %v", got, err)
	}
	// A: an available track, one without a duration, one gone from its
	// album, one of an album gone, one of an album gone with its artist.
	for _, id := range []string{tracks["X1"][1], tracks["X2"][1], tracks["Y1"][2], tracks["Y2"][0], tracks["Z1"][0]} {
		if err := svc.AddFavorite(ctx, userA, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.AddFavorite(ctx, userB, tracks["Y1"][0]); err != nil {
		t.Fatal(err)
	}
	for user, want := range map[string]FavoritesSummary{userA: {TrackCount: 5, DurationMS: 2000}, userB: {TrackCount: 1, DurationMS: 8000},
		uuid.NewString(): {}} {
		if got, err := svc.GetFavoritesSummary(ctx, user); err != nil || got != want {
			t.Fatalf("the favorites of %s: %+v, %v; want %+v", user, got, err, want)
		}
	}
	// The count is the length of the list.
	if listed := allFavorites(t, svc, userA, 2); len(listed) != 5 {
		t.Fatalf("%d favorites listed", len(listed))
	}
	if err := svc.RemoveFavorite(ctx, userA, tracks["X1"][1]); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.GetFavoritesSummary(ctx, userA); err != nil || got != (FavoritesSummary{TrackCount: 4}) {
		t.Fatalf("after a removal: %+v, %v", got, err)
	}
}
