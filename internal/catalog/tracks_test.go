package catalog

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/store"
)

// The list of the tracks, GET /tracks (docs/proposals/web-client-api.md
// A1): paginated by key like the lists of DESIGN.md §8.5, in four total
// orders, with strict cursors (T9).

// listTrack is a track of the list as a test puts it in the index.
type listTrack struct {
	id        string
	album     testAlbum
	title     string
	artist    string
	disc, no  int64
	firstSeen int64
	available bool
	// favorite says whether the track is a favorite of userA.
	favorite bool
}

var (
	trackTitles  = []string{"Intro", "intro", "Track 2", "Track 10", "Écoute", "Ecoute", "日本", "Zebra", "Intro"}
	trackSeens   = []int64{1000, 1000, 2000, 3000}
	trackNumbers = []int64{1, 1, 2, 3, 10}
)

// randomTracks makes a random index of albums, as randomIndex does, and
// their tracks: titles, artists, discs, numbers and moments that tie, the
// artist of a track often not the artist of its album, some tracks not
// available, all those of an unavailable album not available, and some of
// them favorites of userA.
func randomTracks(seed uint64, albums int) ([]testAlbum, []testArtist, []listTrack) {
	all, artists := randomIndex(seed, albums)
	var key [32]byte
	key[0], key[1] = byte(seed), 1
	src := rand.NewChaCha8(key)
	r := rand.New(src)
	var tracks []listTrack
	for _, a := range all {
		for range r.IntN(4) {
			tracks = append(tracks, listTrack{
				id:        uuid.Must(uuid.NewRandomFromReader(src)).String(),
				album:     a,
				title:     trackTitles[r.IntN(len(trackTitles))],
				artist:    artistNames[r.IntN(len(artistNames))],
				disc:      int64(1 + r.IntN(2)),
				no:        trackNumbers[r.IntN(len(trackNumbers))],
				firstSeen: trackSeens[r.IntN(len(trackSeens))],
				available: a.available && r.IntN(5) != 0,
				favorite:  r.IntN(3) == 0,
			})
		}
	}
	return all, artists, tracks
}

// putListTracks writes the albums and their tracks as the indexer does,
// with the sort keys and the copy of the key of the album, and the
// favorites of userA; userB has none.
func putListTracks(t *testing.T, st *store.Store, albums []testAlbum, tracks []listTrack) {
	t.Helper()
	putAlbums(t, st, albums...)
	ctx := t.Context()
	err := st.WithWriteTx(ctx, func(q *store.Queries) error {
		for _, id := range []string{userA, userB} {
			if err := q.CreateUser(ctx, store.CreateUserParams{ID: id, Username: "u" + id[len(id)-2:], PasswordHash: "x",
				Role: "user", CreatedAt: 1}); err != nil {
				return err
			}
		}
		for i, tr := range tracks {
			err := q.UpsertTrack(ctx, store.UpsertTrackParams{
				ID: tr.id, AlbumID: tr.album.id, Fingerprint: tr.id, FpVersion: "v", Occurrence: 1,
				Disc: tr.disc, No: tr.no, Title: tr.title, Artist: tr.artist, RelPath: fmt.Sprintf("%02d.flac", i),
				FileSize: 1, FileMtimeNs: 1, FileSha256: "s", Codec: "flac", SampleRate: 44100, Channels: 2, UpdatedAt: 1,
				TitleKey: sortKey(tr.title), ArtistKey: sortKey(tr.artist), FirstSeenAt: tr.firstSeen,
			})
			if err != nil {
				return err
			}
			if !tr.available {
				if err := q.SetTrackUnavailable(ctx, store.SetTrackUnavailableParams{UpdatedAt: 2, ID: tr.id}); err != nil {
					return err
				}
			}
			if tr.favorite {
				if err := q.AddFavorite(ctx, store.AddFavoriteParams{UserID: userA, TrackID: tr.id, CreatedAt: 1}); err != nil {
					return err
				}
			}
		}
		for _, a := range albums {
			err := q.SetAlbumKeyOfTracks(ctx, store.SetAlbumKeyOfTracksParams{AlbumKey: sortKey(a.title), AlbumID: a.id})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// trackOrderOf compares two tracks in an order of the list, written again
// from the proposal: the oracle of the tests.
func trackOrderOf(sort string) func(a, b listTrack) int {
	id := func(a, b listTrack) int { return cmp.Compare(a.id, b.id) }
	inAlbum := func(a, b listTrack) int {
		return cmp.Or(bytes.Compare(sortKey(a.album.title), sortKey(b.album.title)), cmp.Compare(a.album.id, b.album.id),
			cmp.Compare(a.disc, b.disc), cmp.Compare(a.no, b.no), id(a, b))
	}
	switch sort {
	case SortTitle:
		return func(a, b listTrack) int { return cmp.Or(bytes.Compare(sortKey(a.title), sortKey(b.title)), id(a, b)) }
	case SortArtist:
		return func(a, b listTrack) int {
			return cmp.Or(bytes.Compare(sortKey(a.artist), sortKey(b.artist)), inAlbum(a, b))
		}
	case SortAlbum:
		return inAlbum
	case SortAdded:
		return func(a, b listTrack) int { return cmp.Or(cmp.Compare(a.firstSeen, b.firstSeen), id(a, b)) }
	}
	panic("no order " + sort)
}

// wantTracks is the list of the available tracks of the albums of
// artistID ("" for all) in an order, as ids.
func wantTracks(tracks []listTrack, sort, order, artistID string) []string {
	var keep []listTrack
	for _, tr := range tracks {
		if tr.available && (artistID == "" || tr.album.artist.id == artistID) {
			keep = append(keep, tr)
		}
	}
	slices.SortFunc(keep, trackOrderOf(sort))
	if order == OrderDesc {
		slices.Reverse(keep)
	}
	ids := make([]string, 0, len(keep))
	for _, tr := range keep {
		ids = append(ids, tr.id)
	}
	return ids
}

// readTracks reads every page of a list of the tracks, and checks that
// every page but the last is full and that only the last has no cursor.
func readTracks(ctx context.Context, svc *Service, q TrackQuery) ([]Track, error) {
	var out []Track
	for pages := 0; pages <= 10000; pages++ {
		page, err := svc.ListTracks(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("%+v: %w", q, err)
		}
		out = append(out, page.Tracks...)
		switch {
		case page.Next == "" && len(page.Tracks) == 0 && pages > 0:
			return nil, fmt.Errorf("%+v: an empty last page after a cursor", q)
		case page.Next == "":
			return out, nil
		case len(page.Tracks) != q.Limit:
			return nil, fmt.Errorf("%+v: a page of %d tracks with a cursor", q, len(page.Tracks))
		}
		q.After = &page.Next
	}
	return nil, fmt.Errorf("%+v: no end", q)
}

func trackIDs(tracks []Track) []string {
	ids := make([]string, 0, len(tracks))
	for _, tr := range tracks {
		ids = append(ids, tr.ID)
	}
	return ids
}

var trackSorts = []string{SortTitle, SortArtist, SortAlbum, SortAdded}

// For every order and direction, the pages read with any limit, put end to
// end, are the whole list in its order: nothing twice, nothing missing, no
// track that is not available. Also for the tracks of the albums of one
// artist, of an artist without an available album, and of no artist.
func TestTrackPagesAreTheWholeList(t *testing.T) {
	for seed := range uint64(2) {
		albums, artists, tracks := randomTracks(seed, 70)
		st := newStore(t)
		putListTracks(t, st, albums, tracks)
		svc := New(st, time.Now)
		// The artist with the most available tracks.
		counts := map[string]int{}
		for _, tr := range tracks {
			if tr.available {
				counts[tr.album.artist.id]++
			}
		}
		most := artists[0].id
		for _, a := range artists {
			if counts[a.id] > counts[most] {
				most = a.id
			}
		}
		for _, sort := range trackSorts {
			for _, order := range []string{OrderAsc, OrderDesc} {
				for _, artist := range []string{"", most, artists[len(artists)-1].id, uuid.NewString()} {
					want := wantTracks(tracks, sort, order, artist)
					if artist == "" && len(want) < 50 || artist == most && len(want) < 5 {
						t.Fatalf("seed %d: only %d available tracks of %q", seed, len(want), artist)
					}
					sizes := limits
					if len(want) == 0 {
						sizes = []int{50}
					}
					for _, limit := range sizes {
						got, err := readTracks(t.Context(), svc, TrackQuery{UserID: userA, Sort: sort, Order: order, ArtistID: artist, Limit: limit})
						if err != nil {
							t.Fatal(err)
						}
						if ids := trackIDs(got); !slices.Equal(ids, want) {
							t.Fatalf("seed %d, %s %s, artist %q, limit %d:\n got %v\nwant %v", seed, sort, order, artist, limit, ids, want)
						}
					}
				}
			}
		}
	}
}

// A track of the list is the track of GET /tracks/{id}, with its album and
// whether it is a favorite of the user of the request.
func TestTrackListShowsTheTrack(t *testing.T) {
	albums, _, tracks := randomTracks(5, 20)
	st := newStore(t)
	putListTracks(t, st, albums, tracks)
	svc := New(st, time.Now)
	for _, user := range []string{userA, userB} {
		got, err := readTracks(t.Context(), svc, TrackQuery{UserID: user, Sort: SortAlbum, Order: OrderAsc, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		favorites := 0
		for _, tr := range got {
			one, err := svc.GetTrack(t.Context(), user, tr.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tr, one) {
				t.Fatalf("in the list %+v, alone %+v", tr, one)
			}
			if tr.Favorite {
				favorites++
			}
		}
		if user == userB && favorites != 0 || user == userA && favorites == 0 {
			t.Fatalf("%s has %d favorites in the list", user, favorites)
		}
	}
}

// A cursor of another order or direction, of the albums or the artists, and
// one that was tampered with, are 400 invalid_cursor; a cursor of the
// tracks is refused by the albums. A cursor with another artist filter is a
// position in the same list, and is read.
func TestTrackCursorsBelongToTheirList(t *testing.T) {
	albums, _, tracks := randomTracks(7, 20)
	st := newStore(t)
	putListTracks(t, st, albums, tracks)
	svc := New(st, time.Now)
	ctx := t.Context()

	cursors := map[[2]string]string{}
	for _, sort := range trackSorts {
		for _, order := range []string{OrderAsc, OrderDesc} {
			page, err := svc.ListTracks(ctx, TrackQuery{UserID: userA, Sort: sort, Order: order, Limit: 2})
			if err != nil || page.Next == "" {
				t.Fatalf("%s %s: %v, next %q", sort, order, err, page.Next)
			}
			cursors[[2]string{sort, order}] = page.Next
		}
	}
	for from, c := range cursors {
		for to := range cursors {
			_, err := svc.ListTracks(ctx, TrackQuery{UserID: userA, Sort: to[0], Order: to[1], Limit: 2, After: &c})
			if from == to {
				if err != nil {
					t.Errorf("a cursor of %v in its own list: %v", from, err)
				}
				continue
			}
			wantCode(t, fmt.Sprintf("a cursor of %v in %v", from, to), err, http.StatusBadRequest, "invalid_cursor")
		}
		// The albums have orders of the same names, title and added with
		// the same kinds of keys.
		for _, sort := range []string{SortTitle, SortArtist, SortYear, SortAdded} {
			_, err := svc.ListAlbums(ctx, AlbumQuery{Sort: sort, Order: from[1], Limit: 2, After: &c})
			wantCode(t, fmt.Sprintf("a cursor of the tracks %v in the albums by %s", from, sort), err, http.StatusBadRequest, "invalid_cursor")
		}
		bad := []byte(c)
		bad[0] ^= 1
		tampered := string(bad)
		_, err := svc.ListTracks(ctx, TrackQuery{UserID: userA, Sort: from[0], Order: from[1], Limit: 2, After: &tampered})
		wantCode(t, fmt.Sprintf("a tampered cursor of %v", from), err, http.StatusBadRequest, "invalid_cursor")
	}
	for _, sort := range []string{SortTitle, SortAdded} {
		page, err := svc.ListAlbums(ctx, AlbumQuery{Sort: sort, Order: OrderAsc, Limit: 1})
		if err != nil || page.Next == "" {
			t.Fatalf("albums by %s: %v, next %q", sort, err, page.Next)
		}
		_, err = svc.ListTracks(ctx, TrackQuery{UserID: userA, Sort: sort, Order: OrderAsc, Limit: 2, After: &page.Next})
		wantCode(t, "a cursor of the albums by "+sort+" in the tracks", err, http.StatusBadRequest, "invalid_cursor")
	}
	artists, err := svc.ListArtists(ctx, 1, nil)
	if err != nil || artists.Next == "" {
		t.Fatalf("artists: %v, next %q", err, artists.Next)
	}
	_, err = svc.ListTracks(ctx, TrackQuery{UserID: userA, Sort: SortTitle, Order: OrderAsc, Limit: 2, After: &artists.Next})
	wantCode(t, "a cursor of the artists in the tracks", err, http.StatusBadRequest, "invalid_cursor")
	for _, s := range []string{"", "x", "e30", "null"} {
		_, err = svc.ListTracks(ctx, TrackQuery{UserID: userA, Sort: SortTitle, Order: OrderAsc, Limit: 2, After: &s})
		wantCode(t, fmt.Sprintf("the cursor %q", s), err, http.StatusBadRequest, "invalid_cursor")
	}
	c := cursors[[2]string{SortAlbum, OrderAsc}]
	if _, err := svc.ListTracks(ctx, TrackQuery{UserID: userA, Sort: SortAlbum, Order: OrderAsc, ArtistID: albums[0].artist.id,
		Limit: 2, After: &c}); err != nil {
		t.Errorf("a cursor with another filter: %v", err)
	}
}

// A cursor stays valid when the track it names is no longer in the list:
// the next page starts after its key.
func TestTrackCursorOfARowThatLeftTheList(t *testing.T) {
	albums, _, tracks := randomTracks(3, 30)
	st := newStore(t)
	putListTracks(t, st, albums, tracks)
	svc := New(st, time.Now)
	ctx := t.Context()
	for _, sort := range trackSorts {
		for _, order := range []string{OrderAsc, OrderDesc} {
			want := wantTracks(tracks, sort, order, "")
			page, err := svc.ListTracks(ctx, TrackQuery{UserID: userA, Sort: sort, Order: order, Limit: 3})
			if err != nil {
				t.Fatal(err)
			}
			gone := page.Tracks[2].ID
			setAvailable(t, st, gone, false)
			rest, err := readTracks(ctx, svc, TrackQuery{UserID: userA, Sort: sort, Order: order, Limit: 4, After: &page.Next})
			if err != nil {
				t.Fatal(err)
			}
			if ids := trackIDs(rest); !slices.Equal(ids, want[3:]) {
				t.Fatalf("%s %s: after a cursor whose track left:\n got %v\nwant %v", sort, order, ids, want[3:])
			}
			setAvailable(t, st, gone, true)
		}
	}
}

// setAvailable makes a track unavailable, or available again, as the
// scanner does.
func setAvailable(t *testing.T, st *store.Store, id string, available bool) {
	t.Helper()
	query := `UPDATE tracks SET available = 0 WHERE id = ?`
	if available {
		query = `UPDATE tracks SET available = 1 WHERE id = ?`
	}
	err := st.WithWriteTx(t.Context(), func(q *store.Queries) error {
		_, err := q.Conn().ExecContext(t.Context(), query, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
