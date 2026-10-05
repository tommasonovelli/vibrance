package catalog

import (
	"bytes"
	"cmp"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// The lists of DESIGN.md §8.5: paginated by key, in a total order, with
// strict cursors (T9).

// Names and titles that tie, differ only in case or accents, hold numbers,
// or are in other scripts. "a\u200db" has the sort key of "ab" (the joiner
// does not sort) and another identity.
var (
	artistNames = []string{"Ève", "Eve", "Zoë", "Zoe", "Artist 2", "Artist 10", "The Beatles", "Ärzte", "李白",
		"Ωmega", "a\u200db", "ab", "AB", "Ève"}
	albumTitles = []string{"Track 2", "Track 10", "track 1", "Écoute", "Ecoute", "ecoute", "Zebra", "apple",
		"Apple", "日本", "😀", "Kind of Blue", "Kind of Blue"}
	albumYears = []int{0, 0, 1959, 1959, 2001, 1, 9999}
	firstSeens = []int64{1000, 1000, 2000, 3000}
)

// randomIndex makes a random index: albums of many artists, some not
// available, with titles, years and dates that tie. Some artists have no
// available album; artistsOf returns every artist, with or without one.
func randomIndex(seed uint64, n int) (albums []testAlbum, artists []testArtist) {
	var key [32]byte
	key[0] = byte(seed)
	src := rand.NewChaCha8(key)
	r := rand.New(src)
	newID := func() string { return uuid.Must(uuid.NewRandomFromReader(src)).String() }
	for _, name := range artistNames {
		// Two artists may have one name: their ids differ, as when the name
		// of a tag is spelled in two ways that sort alike.
		artists = append(artists, testArtist{id: newID(), name: name})
	}
	for range n {
		albums = append(albums, testAlbum{
			id:        newID(),
			artist:    artists[r.IntN(len(artists)-2)],
			title:     albumTitles[r.IntN(len(albumTitles))],
			year:      albumYears[r.IntN(len(albumYears))],
			firstSeen: firstSeens[r.IntN(len(firstSeens))],
			available: r.IntN(6) != 0,
		})
	}
	// The last two artists have only albums that are not available.
	for _, a := range artists[len(artists)-2:] {
		albums = append(albums, testAlbum{id: newID(), artist: a, title: "Gone", firstSeen: 1, available: false})
	}
	return albums, artists
}

var limits = []int{1, 2, 7, 50, 200}

// §14 S15: for every order and direction, the pages read with any limit,
// put end to end, are the whole list in its order: nothing twice, nothing
// missing. Also for the albums of one artist, of an artist without an
// available album, and of no artist: those two with one limit, since they
// are empty. (Under -race a page costs milliseconds: the sizes are what
// keeps the test within seconds.)
func TestAlbumPagesAreTheWholeList(t *testing.T) {
	for seed := range uint64(2) {
		albums, artists := randomIndex(seed, 130)
		st := newStore(t)
		putAlbums(t, st, albums...)
		svc := New(st)
		for _, sort := range []string{SortTitle, SortArtist, SortYear, SortAdded} {
			for _, order := range []string{OrderAsc, OrderDesc} {
				for _, artist := range []string{"", artists[0].id, artists[len(artists)-1].id, uuid.NewString()} {
					want := wantAlbums(albums, sort, order, artist)
					if artist == "" && len(want) < 100 || artist == artists[0].id && len(want) < 3 {
						t.Fatalf("seed %d: only %d available albums of %q", seed, len(want), artist)
					}
					sizes := limits
					if len(want) == 0 {
						sizes = []int{50}
					}
					for _, limit := range sizes {
						got := allAlbums(t, svc, AlbumQuery{Sort: sort, Order: order, ArtistID: artist, Limit: limit})
						if !slices.Equal(got, want) {
							t.Fatalf("seed %d, %s %s, artist %q, limit %d:\n got %v\nwant %v", seed, sort, order, artist, limit, got, want)
						}
					}
				}
			}
		}
	}
}

// The same for the artists: those with at least one available album, by
// (sort key, id), each with how many it has.
func TestArtistPagesAreTheWholeList(t *testing.T) {
	for seed := range uint64(3) {
		albums, artists := randomIndex(seed, 140)
		st := newStore(t)
		putAlbums(t, st, albums...)
		svc := New(st)

		counts := map[string]int{}
		for _, a := range albums {
			if a.available {
				counts[a.artist.id]++
			}
		}
		var want []ArtistSummary
		for _, a := range artists {
			if counts[a.id] > 0 {
				want = append(want, ArtistSummary{ArtistRef: ArtistRef{ID: a.id, Name: a.name}, AlbumCount: counts[a.id]})
			}
		}
		slices.SortFunc(want, func(a, b ArtistSummary) int {
			return cmp.Or(bytes.Compare(sortKey(a.Name), sortKey(b.Name)), cmp.Compare(a.ID, b.ID))
		})
		if len(want) != len(artists)-2 {
			t.Fatalf("seed %d: %d artists with an available album", seed, len(want))
		}
		for _, limit := range limits {
			var got []ArtistSummary
			var after *string
			for {
				page, err := svc.ListArtists(t.Context(), limit, after)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, page.Artists...)
				if page.Next == "" {
					break
				}
				if len(page.Artists) != limit {
					t.Fatalf("limit %d: a page of %d with a cursor", limit, len(page.Artists))
				}
				after = &page.Next
			}
			if !slices.Equal(got, want) {
				t.Fatalf("seed %d, limit %d:\n got %v\nwant %v", seed, limit, got, want)
			}
		}
	}
}

// §5.5: the order of the titles is that of the Unicode collation, with the
// numbers ordered as numbers: case and accents count after the letters.
func TestAlbumOrderOfTitles(t *testing.T) {
	st := newStore(t)
	artist := testArtist{id: uuid.NewString(), name: "Artist"}
	titles := []string{"Zebra", "Track 10", "Écoute", "track 1", "Track 2", "apple", "Ecoute", "The End", "Apple", "Track 9b"}
	var albums []testAlbum
	for i, title := range titles {
		albums = append(albums, testAlbum{id: fmt.Sprintf("0199a5c0-0000-7000-8000-%012d", i), artist: artist, title: title,
			firstSeen: 1, available: true})
	}
	putAlbums(t, st, albums...)
	page, err := New(st).ListAlbums(t.Context(), AlbumQuery{Sort: SortTitle, Order: OrderAsc, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range page.Albums {
		got = append(got, a.Title)
	}
	want := []string{"apple", "Apple", "Ecoute", "Écoute", "The End", "track 1", "Track 2", "Track 9b", "Track 10", "Zebra"}
	if !slices.Equal(got, want) {
		t.Fatalf("titles in order %q, want %q", got, want)
	}
}

// §8.5: with order=desc every key is reversed, an album without a year
// included: last in ascending order, first in descending order.
func TestAlbumsWithoutAYear(t *testing.T) {
	st := newStore(t)
	artist := testArtist{id: uuid.NewString(), name: "Artist"}
	putAlbums(t, st,
		testAlbum{id: "0199a5c0-0000-7000-8000-000000000001", artist: artist, title: "B", year: 9999, firstSeen: 1, available: true},
		testAlbum{id: "0199a5c0-0000-7000-8000-000000000002", artist: artist, title: "A", firstSeen: 1, available: true},
		testAlbum{id: "0199a5c0-0000-7000-8000-000000000003", artist: artist, title: "C", year: 1, firstSeen: 1, available: true},
	)
	svc := New(st)
	for order, want := range map[string][]string{OrderAsc: {"C", "B", "A"}, OrderDesc: {"A", "B", "C"}} {
		for _, sort := range []string{SortYear, SortArtist} {
			page, err := svc.ListAlbums(t.Context(), AlbumQuery{Sort: sort, Order: order, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, a := range page.Albums {
				got = append(got, a.Title)
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s %s: %v, want %v", sort, order, got, want)
			}
			if year := page.Albums[slices.Index(got, "A")].Year; year != nil {
				t.Errorf("an album without a year shows %d", *year)
			}
		}
	}
}

// A cursor of another order, direction or list, and one that was tampered
// with, are 400 invalid_cursor; a cursor of the same list with another
// artist filter is a position in the list, and is read.
func TestCursorsBelongToTheirList(t *testing.T) {
	albums, _ := randomIndex(7, 30)
	st := newStore(t)
	putAlbums(t, st, albums...)
	svc := New(st)
	ctx := t.Context()

	cursors := map[[2]string]string{}
	for _, sort := range []string{SortTitle, SortArtist, SortYear, SortAdded} {
		for _, order := range []string{OrderAsc, OrderDesc} {
			page, err := svc.ListAlbums(ctx, AlbumQuery{Sort: sort, Order: order, Limit: 2})
			if err != nil || page.Next == "" {
				t.Fatalf("%s %s: %v, next %q", sort, order, err, page.Next)
			}
			cursors[[2]string{sort, order}] = page.Next
		}
	}
	artists, err := svc.ListArtists(ctx, 1, nil)
	if err != nil || artists.Next == "" {
		t.Fatalf("artists: %v, next %q", err, artists.Next)
	}
	for from, c := range cursors {
		for to := range cursors {
			_, err := svc.ListAlbums(ctx, AlbumQuery{Sort: to[0], Order: to[1], Limit: 2, After: &c})
			if from == to {
				if err != nil {
					t.Errorf("a cursor of %v in its own list: %v", from, err)
				}
				continue
			}
			wantCode(t, fmt.Sprintf("a cursor of %v in %v", from, to), err, http.StatusBadRequest, "invalid_cursor")
		}
		_, err := svc.ListArtists(ctx, 2, &c)
		wantCode(t, fmt.Sprintf("a cursor of the albums %v in the artists", from), err, http.StatusBadRequest, "invalid_cursor")
		// The first letter changed: the cursor is no longer JSON.
		bad := []byte(c)
		bad[0] ^= 1
		tampered := string(bad)
		_, err = svc.ListAlbums(ctx, AlbumQuery{Sort: from[0], Order: from[1], Limit: 2, After: &tampered})
		wantCode(t, fmt.Sprintf("a tampered cursor of %v", from), err, http.StatusBadRequest, "invalid_cursor")
	}
	_, err = svc.ListAlbums(ctx, AlbumQuery{Sort: SortTitle, Order: OrderAsc, Limit: 2, After: &artists.Next})
	wantCode(t, "a cursor of the artists in the albums", err, http.StatusBadRequest, "invalid_cursor")
	for _, s := range []string{"", "x", "e30", "null"} {
		_, err = svc.ListAlbums(ctx, AlbumQuery{Sort: SortTitle, Order: OrderAsc, Limit: 2, After: &s})
		wantCode(t, fmt.Sprintf("the cursor %q", s), err, http.StatusBadRequest, "invalid_cursor")
		_, err = svc.ListArtists(ctx, 2, &s)
		wantCode(t, fmt.Sprintf("the artist cursor %q", s), err, http.StatusBadRequest, "invalid_cursor")
	}
	c := cursors[[2]string{SortTitle, OrderAsc}]
	if _, err := svc.ListAlbums(ctx, AlbumQuery{Sort: SortTitle, Order: OrderAsc, ArtistID: albums[0].artist.id, Limit: 2, After: &c}); err != nil {
		t.Errorf("a cursor with another filter: %v", err)
	}
}

// §8.5: a cursor stays valid when the row it names is no longer in the
// list: the next page starts after its key.
func TestCursorOfARowThatLeftTheList(t *testing.T) {
	albums, _ := randomIndex(3, 40)
	st := newStore(t)
	putAlbums(t, st, albums...)
	svc := New(st)
	for _, sort := range []string{SortTitle, SortArtist, SortYear, SortAdded} {
		for _, order := range []string{OrderAsc, OrderDesc} {
			want := wantAlbums(albums, sort, order, "")
			page, err := svc.ListAlbums(t.Context(), AlbumQuery{Sort: sort, Order: order, Limit: 3})
			if err != nil {
				t.Fatal(err)
			}
			last := page.Albums[2].ID
			i := slices.IndexFunc(albums, func(a testAlbum) bool { return a.id == last })
			gone := albums[i]
			gone.available = false
			putAlbums(t, st, gone)

			rest := allAlbums(t, svc, AlbumQuery{Sort: sort, Order: order, Limit: 4, After: &page.Next})
			if !slices.Equal(rest, want[3:]) {
				t.Fatalf("%s %s: after a cursor whose album left:\n got %v\nwant %v", sort, order, rest, want[3:])
			}
			putAlbums(t, st, albums[i])
		}
	}
}
