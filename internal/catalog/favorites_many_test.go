package catalog

import (
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"vibrance/internal/httpx"
)

// Several favorites at once, POST /me/favorites/tracks
// (docs/proposals/web-client-api.md B2), on a real database: all or
// nothing, idempotent, and the list shows the new favorites in the order
// of the request.

func TestAddFavorites(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 8)
	gone := tracks[7]
	setTrackUnavailable(t, st, gone)
	clock := &testClock{}
	svc := New(st, clock.now)
	ctx := t.Context()

	// A favorite of before, at 500: it keeps its moment.
	clock.set(500)
	if err := svc.AddFavorite(ctx, userA, tracks[2]); err != nil {
		t.Fatal(err)
	}
	// The order of the request, not the order of the ids; a repeated id
	// counts once, at its first place; the track that is not available is
	// accepted.
	request := []string{tracks[5], tracks[0], tracks[2], gone, tracks[5], tracks[3]}
	clock.set(10_000)
	if err := svc.AddFavorites(ctx, userA, request); err != nil {
		t.Fatal(err)
	}
	want := []fav{{tracks[5], 10_000}, {tracks[0], 9_999}, {gone, 9_997}, {tracks[3], 9_996}, {tracks[2], 500}}
	if got := allFavorites(t, svc, userA, 2); !slices.Equal(got, want) {
		t.Fatalf("the favorites after the request:\n got %v\nwant %v", got, want)
	}
	// The same request again changes nothing: every track is a favorite
	// already, with its moment.
	clock.set(20_000)
	if err := svc.AddFavorites(ctx, userA, request); err != nil {
		t.Fatal(err)
	}
	if got := allFavorites(t, svc, userA, 50); !slices.Equal(got, want) {
		t.Fatalf("the favorites after the same request again:\n got %v\nwant %v", got, want)
	}
	if got := allFavorites(t, svc, userB, 50); len(got) != 0 {
		t.Fatalf("another user has favorites: %v", got)
	}

	// Ids that are no tracks: 422 unknown_track with those ids, once each,
	// in the order of the request, and nothing changes, the known tracks of
	// the request included.
	unknown1, unknown2 := uuid.NewString(), uuid.NewString()
	err := svc.AddFavorites(ctx, userA, []string{tracks[6], unknown1, tracks[1], unknown2, unknown1})
	wantCode(t, "unknown tracks", err, http.StatusUnprocessableEntity, CodeUnknownTrack)
	var refusal *httpx.Error
	if !errors.As(err, &refusal) || !slices.Equal(refusal.Details["track_ids"].([]string), []string{unknown1, unknown2}) {
		t.Fatalf("the details of unknown_track: %+v", refusal)
	}
	if got := allFavorites(t, svc, userA, 50); !slices.Equal(got, want) {
		t.Fatalf("a refused request changed the favorites:\n got %v\nwant %v", got, want)
	}

	// Another user, with the same tracks: the moments are of their request.
	clock.set(30_000)
	if err := svc.AddFavorites(ctx, userB, []string{tracks[2], tracks[1]}); err != nil {
		t.Fatal(err)
	}
	if got := allFavorites(t, svc, userB, 50); !slices.Equal(got, []fav{{tracks[2], 30_000}, {tracks[1], 29_999}}) {
		t.Fatalf("the favorites of another user: %v", got)
	}
	if got := allFavorites(t, svc, userA, 50); !slices.Equal(got, want) {
		t.Fatalf("the request of another user changed the favorites:\n got %v\nwant %v", got, want)
	}
}

// The largest request the specification allows, 1000 ids: 999 tracks of
// an album (a disc has at most 999), in reverse, and the first of them
// again at the end. They are listed in the order of the request, page
// after page, and the repeated one at its first place.
func TestAddFavoritesOfAWholeRequest(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 999)
	clock := &testClock{}
	clock.set(1_000_000)
	svc := New(st, clock.now)
	want := slices.Clone(tracks)
	slices.Reverse(want)
	if err := svc.AddFavorites(t.Context(), userA, append(slices.Clone(want), want[0])); err != nil {
		t.Fatal(err)
	}
	got := allFavorites(t, svc, userA, 50)
	if len(got) != len(want) {
		t.Fatalf("%d favorites, want %d", len(got), len(want))
	}
	for i, f := range got {
		if f.id != want[i] || f.at != 1_000_000-int64(i) {
			t.Fatalf("favorite %d is %+v, want %s at %d", i, f, want[i], 1_000_000-i)
		}
	}
}

// Requests of one user at once, with tracks in common, and a refused one
// among them: no request fails but the refused ones, which change nothing
// (their known track, tracks[29], is never a favorite), and every track
// asked for is a favorite once.
func TestAddFavoritesAtOnce(t *testing.T) {
	st := newStore(t)
	tracks := playlistIndex(t, st, 30)
	clock := &testClock{}
	clock.set(1_000_000)
	svc := New(st, clock.now)
	ctx := t.Context()
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for w := range 8 {
		wg.Go(func() {
			// Overlapping windows of the tracks, in both directions.
			ids := slices.Clone(tracks[w*2 : w*2+14])
			if w%2 == 1 {
				slices.Reverse(ids)
			}
			for range 5 {
				if err := svc.AddFavorites(ctx, userA, ids); err != nil {
					errs <- err
				}
			}
			err := svc.AddFavorites(ctx, userA, append([]string{tracks[29]}, uuid.NewString()))
			var refusal *httpx.Error
			if !errors.As(err, &refusal) || refusal.Code != CodeUnknownTrack {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	got := allFavorites(t, svc, userA, 9)
	ids := make([]string, 0, len(got))
	for _, f := range got {
		ids = append(ids, f.id)
	}
	slices.Sort(ids)
	if want := slices.Sorted(slices.Values(tracks[:28])); !slices.Equal(ids, want) {
		t.Fatalf("the favorites after the requests:\n got %v\nwant %v", ids, want)
	}
}
