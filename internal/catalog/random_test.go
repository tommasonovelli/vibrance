package catalog

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// The tracks chosen at random, GET /tracks/random
// (docs/proposals/web-client-api.md B1), on a real database: only available
// tracks, none twice in one answer, every one of them a possible choice,
// and the filter by the artist of the album.

// randomTrackIndex writes two artists with an available album each, a
// third album of the first that is not available, and unavailable tracks
// in an available album. The first track of the second artist is a
// favorite of userA. It returns the ids of the available tracks of each
// artist, those of the tracks that are not available, and the two artists.
func randomTrackIndex(t *testing.T) (svc *Service, first, second, gone []string, firstArtist, secondArtist string) {
	t.Helper()
	st := newStore(t)
	a := testArtist{id: uuid.NewString(), name: "First"}
	b := testArtist{id: uuid.NewString(), name: "Second"}
	albumA := testAlbum{id: uuid.NewString(), artist: a, title: "A", firstSeen: 1, available: true}
	albumB := testAlbum{id: uuid.NewString(), artist: b, title: "B", firstSeen: 1, available: true}
	albumC := testAlbum{id: uuid.NewString(), artist: a, title: "C", firstSeen: 1, available: false}
	putAlbums(t, st, albumA, albumB, albumC)
	put := func(album string, n int, available bool) []string {
		var ids []string
		var tracks []testTrack
		for i := range n {
			id := uuid.NewString()
			ids = append(ids, id)
			tracks = append(tracks, testTrack{id, 1, int64(i + 1), fmt.Sprintf("T%d", i), available})
		}
		putTracks(t, st, album, tracks...)
		return ids
	}
	first = put(albumA.id, 12, true)
	gone = put(albumA.id, 3, false)
	second = put(albumB.id, 9, true)
	// The album C is not available, and so none of its tracks (DESIGN.md
	// §6.4).
	gone = append(gone, put(albumC.id, 4, false)...)
	putFavorite(t, st, second[0])
	return New(st, nil), first, second, gone, a.id, b.id
}

func TestRandomTracks(t *testing.T) {
	svc, first, second, gone, firstArtist, secondArtist := randomTrackIndex(t)
	ctx := t.Context()
	available := append(slices.Clone(first), second...)
	checked := 0
	choose := func(artist string, limit int) []string {
		t.Helper()
		tracks, err := svc.ListRandomTracks(ctx, userA, artist, limit)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(tracks))
		for _, tr := range tracks {
			ids = append(ids, tr.ID)
		}
		// Each track is the one GET /tracks/{id} shows to the same user,
		// favorite included: checked on one answer in 25, which is enough.
		if checked++; checked%25 != 1 {
			return ids
		}
		for _, tr := range tracks {
			want, err := svc.GetTrack(ctx, userA, tr.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tr, want) {
				t.Fatalf("a track chosen at random is\n %+v\nand the track is\n %+v", tr, want)
			}
		}
		return ids
	}
	for _, c := range []struct {
		name, artist string
		among        []string
	}{
		{"the library", "", available},
		{"one artist", firstArtist, first},
		{"another artist", secondArtist, second},
	} {
		// Fewer than there are: that many, each available, of the artist,
		// and none twice; over many answers, every one of them is chosen.
		seen := map[string]int{}
		for range 200 {
			ids := choose(c.artist, 5)
			if len(ids) != 5 {
				t.Fatalf("%s: %d tracks for a limit of 5", c.name, len(ids))
			}
			if len(distinct(ids)) != len(ids) {
				t.Fatalf("%s: a track twice in one answer: %v", c.name, ids)
			}
			for _, id := range ids {
				if !slices.Contains(c.among, id) {
					t.Fatalf("%s: %s is not one of the tracks to choose from (gone: %v)", c.name, id, slices.Contains(gone, id))
				}
				seen[id]++
			}
		}
		if len(seen) != len(c.among) {
			t.Fatalf("%s: 200 answers of 5 chose %d of the %d tracks", c.name, len(seen), len(c.among))
		}
		// More than there are: every one, once, in an order that changes.
		orders := map[string]bool{}
		for range 20 {
			ids := choose(c.artist, 200)
			if got, want := slices.Sorted(slices.Values(ids)), slices.Sorted(slices.Values(c.among)); !slices.Equal(got, want) {
				t.Fatalf("%s: a limit over the tracks chose\n %v\nwant every one once\n %v", c.name, got, want)
			}
			orders[fmt.Sprint(ids)] = true
		}
		if len(orders) < 2 {
			t.Fatalf("%s: 20 answers with every track all had the same order", c.name)
		}
	}
	// An id that is no artist's, and an artist without an available album,
	// choose nothing.
	if ids := choose(uuid.NewString(), 50); len(ids) != 0 {
		t.Fatalf("an id that is no artist's chose %v", ids)
	}
	st := svc.store
	lonely := testArtist{id: uuid.NewString(), name: "Lonely"}
	putAlbums(t, st, testAlbum{id: uuid.NewString(), artist: lonely, title: "Gone", firstSeen: 1, available: false})
	if ids := choose(lonely.id, 50); len(ids) != 0 {
		t.Fatalf("an artist without an available album chose %v", ids)
	}
}

// An empty library chooses nothing, and is not an error.
func TestRandomTracksOfAnEmptyLibrary(t *testing.T) {
	svc := New(newStore(t), nil)
	tracks, err := svc.ListRandomTracks(t.Context(), userA, "", 50)
	if err != nil || len(tracks) != 0 {
		t.Fatalf("an empty library: %v, %v", tracks, err)
	}
}
