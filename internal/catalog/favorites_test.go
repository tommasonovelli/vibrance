package catalog

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// The favorites of DESIGN.md §8.3 and §8.5 (step S18), on a real database:
// adding and removing are idempotent, each user has their own, and the list
// is paginated by (created_at, track_id), the most recent first.

// testClock is a clock a test sets.
type testClock struct {
	mu sync.Mutex
	ms int64
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.UnixMilli(c.ms)
}

func (c *testClock) set(ms int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ms = ms
}

// putUsers writes the accounts with those ids.
func putUsers(t *testing.T, st *store.Store, ids ...string) {
	t.Helper()
	ctx := t.Context()
	err := st.WithWriteTx(ctx, func(q *store.Queries) error {
		for _, id := range ids {
			if err := q.CreateUser(ctx, store.CreateUserParams{ID: id, Username: "u" + id[len(id)-4:], PasswordHash: "x",
				Role: "user", CreatedAt: 1}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// favoriteRowsOf counts the rows of the favorites of a user in the database.
func favoriteRowsOf(t *testing.T, st *store.Store, userID string) int {
	t.Helper()
	var n int
	err := st.Read(t.Context(), func(q *store.Queries) error {
		return q.Conn().QueryRowContext(t.Context(), `SELECT count(*) FROM favorites WHERE user_id = ?`, userID).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// fav is a favorite as the tests compare it: the track and the moment.
type fav struct {
	id string
	at int64
}

// allFavorites reads every page of the favorites of a user, and checks that
// every page but the last is full, that only the last has no cursor, and
// that every track listed says it is a favorite.
func allFavorites(t *testing.T, svc *Service, userID string, limit int) []fav {
	t.Helper()
	got, err := readFavorites(t.Context(), svc, userID, limit, true)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// readFavorites is allFavorites for a goroutine of a test, which cannot
// stop it. When the favorites do not change (settled), a page that is not
// full before the last, and an empty last page after a cursor, are errors.
// The keys of the rows always go down, whatever is written meanwhile.
func readFavorites(ctx context.Context, svc *Service, userID string, limit int, settled bool) ([]fav, error) {
	var (
		got   []fav
		after *string
	)
	for pages := 0; pages <= 10000; pages++ {
		page, err := svc.ListFavorites(ctx, userID, limit, after)
		if err != nil {
			return nil, err
		}
		for _, f := range page.Favorites {
			if !f.Track.Favorite {
				return nil, fmt.Errorf("a track of the list is not a favorite: %+v", f.Track)
			}
			next := fav{f.Track.ID, f.FavoritedAt.UnixMilli()}
			if len(got) > 0 && favOrder(got[len(got)-1], next) >= 0 {
				return nil, fmt.Errorf("limit %d: %+v after %+v", limit, next, got[len(got)-1])
			}
			got = append(got, next)
		}
		switch {
		case len(page.Favorites) > limit:
			return nil, fmt.Errorf("limit %d: a page of %d favorites", limit, len(page.Favorites))
		case settled && page.Next == "" && len(page.Favorites) == 0 && pages > 0:
			return nil, fmt.Errorf("limit %d: an empty last page after a cursor", limit)
		case page.Next == "":
			return got, nil
		case len(page.Favorites) != limit:
			return nil, fmt.Errorf("limit %d: a page of %d favorites with a cursor", limit, len(page.Favorites))
		}
		after = &page.Next
	}
	return nil, errors.New("no end")
}

// favOrder is the order of the list (§8.5), written again from the design:
// the most recent first, and the greater id first among those of one
// moment.
func favOrder(a, b fav) int {
	return cmp.Or(cmp.Compare(b.at, a.at), cmp.Compare(b.id, a.id))
}

// favoriteIndex writes an album with three tracks, the last of which is not
// available, and the accounts userA and userB.
func favoriteIndex(t *testing.T, st *store.Store) (one, two, gone string) {
	t.Helper()
	album := testAlbum{id: uuid.NewString(), artist: testArtist{id: uuid.NewString(), name: "Artist"}, title: "Album",
		firstSeen: 1, available: true}
	putAlbums(t, st, album)
	one, two, gone = uuid.NewString(), uuid.NewString(), uuid.NewString()
	putTracks(t, st, album.id, testTrack{one, 1, 1, "One", true}, testTrack{two, 1, 2, "Two", true}, testTrack{gone, 1, 3, "Gone", false})
	putUsers(t, st, userA, userB)
	return one, two, gone
}

func TestAddAndRemoveFavorite(t *testing.T) {
	st := newStore(t)
	one, two, gone := favoriteIndex(t, st)
	clock := &testClock{}
	svc := New(st, clock.now)
	ctx := t.Context()
	add := func(at int64, user, track string) {
		t.Helper()
		clock.set(at)
		if err := svc.AddFavorite(ctx, user, track); err != nil {
			t.Fatal(err)
		}
	}
	remove := func(user, track string) {
		t.Helper()
		if err := svc.RemoveFavorite(ctx, user, track); err != nil {
			t.Fatal(err)
		}
	}
	want := func(where, user string, favs ...fav) {
		t.Helper()
		if got := allFavorites(t, svc, user, 50); !slices.Equal(got, favs) {
			t.Fatalf("%s: the favorites are %+v, want %+v", where, got, favs)
		}
	}

	want("at first", userA)
	add(1000, userA, one)
	want("one added", userA, fav{one, 1000})
	want("one added by the other", userB)

	// Again: the same favorite, since the same moment.
	add(2000, userA, one)
	want("one added again", userA, fav{one, 1000})

	// A track that is not available can be a favorite, and is listed as it
	// is.
	add(3000, userA, gone)
	want("an unavailable track", userA, fav{gone, 3000}, fav{one, 1000})
	page, err := svc.ListFavorites(ctx, userA, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr := page.Favorites[0].Track; tr.Available || tr.Title != "Gone" || tr.Album.Title != "Album" || tr.Album.Artist.Name != "Artist" {
		t.Fatalf("an unavailable favorite: %+v", tr)
	}
	if f := page.Favorites[1]; !f.Track.Available || f.Track.Title != "One" || !f.FavoritedAt.Equal(time.UnixMilli(1000)) ||
		f.FavoritedAt.Location() != time.UTC {
		t.Fatalf("an available favorite: %+v", f)
	}

	// Each user has their own, and the details say the same as the list.
	add(4000, userB, one)
	add(4000, userB, two)
	want("the other's", userB, fav{max(one, two), 4000}, fav{min(one, two), 4000})
	want("mine, after the other's", userA, fav{gone, 3000}, fav{one, 1000})
	for _, c := range []struct {
		user, track string
		favorite    bool
	}{{userA, one, true}, {userA, two, false}, {userA, gone, true}, {userB, one, true}, {userB, two, true}, {userB, gone, false}} {
		tr, err := svc.GetTrack(ctx, c.user, c.track)
		if err != nil || tr.Favorite != c.favorite {
			t.Fatalf("GetTrack(%s, %s): favorite %v, %v; want %v", c.user, c.track, tr.Favorite, err, c.favorite)
		}
	}

	// Removing takes away only the favorite of the user who asks, and is
	// idempotent.
	remove(userB, one)
	want("removed", userB, fav{two, 4000})
	want("mine, after the other removed theirs", userA, fav{gone, 3000}, fav{one, 1000})
	remove(userB, one)
	remove(userA, two)
	want("removed again", userB, fav{two, 4000})
	want("mine, after removing what was not mine", userA, fav{gone, 3000}, fav{one, 1000})

	// Added again after a removal, it is a new favorite.
	remove(userA, one)
	add(5000, userA, one)
	want("added again", userA, fav{one, 5000}, fav{gone, 3000})

	// A track that does not exist.
	unknown := uuid.NewString()
	wantNotFound(t, "adding no track", svc.AddFavorite(ctx, userA, unknown), CodeTrackNotFound)
	wantNotFound(t, "removing no track", svc.RemoveFavorite(ctx, userA, unknown), CodeTrackNotFound)
	want("after the refusals", userA, fav{one, 5000}, fav{gone, 3000})

	// An account that is gone since its request was authenticated: nothing
	// is written, and nothing fails.
	nobody := uuid.NewString()
	if err := svc.AddFavorite(ctx, nobody, one); err != nil {
		t.Fatalf("adding for an account that is gone: %v", err)
	}
	if err := svc.RemoveFavorite(ctx, nobody, one); err != nil {
		t.Fatalf("removing for an account that is gone: %v", err)
	}
	if n := favoriteRowsOf(t, st, nobody); n != 0 {
		t.Fatalf("%d favorites of an account that does not exist", n)
	}
	if a, b := favoriteRowsOf(t, st, userA), favoriteRowsOf(t, st, userB); a != 2 || b != 1 {
		t.Fatalf("%d and %d rows, want 2 and 1", a, b)
	}
}

// §14 S18: the pages read with any limit, put end to end, are the whole
// list of the favorites of the user, the most recent first: nothing twice,
// nothing missing, nothing of another user. Many favorites share their
// moment, so the id decides; some tracks, and some whole albums, are not
// available, and stay in the list.
func TestFavoritePagesAreTheWholeList(t *testing.T) {
	for seed := range uint64(3) {
		r := rand.New(rand.NewPCG(seed, 18))
		st := newStore(t)
		putUsers(t, st, userA, userB)
		artist := testArtist{id: uuid.NewString(), name: "Artist"}
		available := map[string]bool{}
		var tracks []string
		for a := range 6 {
			album := testAlbum{id: uuid.NewString(), artist: artist, title: fmt.Sprintf("Album %d", a), firstSeen: 1, available: a != 0}
			putAlbums(t, st, album)
			var rows []testTrack
			for n := range 40 {
				id := uuid.NewString()
				// The tracks of an album that is gone are gone with it.
				available[id] = album.available && r.IntN(4) != 0
				rows = append(rows, testTrack{id, 1, int64(n + 1), "Track", available[id]})
				tracks = append(tracks, id)
			}
			putTracks(t, st, album.id, rows...)
		}
		want := map[string][]fav{}
		ctx := t.Context()
		err := st.WithWriteTx(ctx, func(q *store.Queries) error {
			for _, id := range tracks {
				for _, user := range []string{userA, userB} {
					if r.IntN(3) == 0 {
						continue
					}
					// Few moments, far apart and close, negative too: the
					// database takes any integer.
					at := []int64{-5, 0, 1, 2, 1727000000000, 1727000000001}[r.IntN(6)]
					if err := q.AddFavorite(ctx, store.AddFavoriteParams{UserID: user, TrackID: id, CreatedAt: at}); err != nil {
						return err
					}
					want[user] = append(want[user], fav{id, at})
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		svc := New(st, time.Now)
		for _, user := range []string{userA, userB} {
			slices.SortFunc(want[user], favOrder)
			if len(want[user]) < 100 {
				t.Fatalf("seed %d: only %d favorites", seed, len(want[user]))
			}
			for _, limit := range limits {
				if got := allFavorites(t, svc, user, limit); !slices.Equal(got, want[user]) {
					t.Fatalf("seed %d, limit %d:\n got %v\nwant %v", seed, limit, got, want[user])
				}
			}
		}
		// What is not available says so, and nothing else does.
		page, err := svc.ListFavorites(ctx, userA, 200, nil)
		if err != nil {
			t.Fatal(err)
		}
		gone := 0
		for _, f := range page.Favorites {
			if f.Track.Available != available[f.Track.ID] {
				t.Fatalf("seed %d: track %s available %v, want %v", seed, f.Track.ID, f.Track.Available, available[f.Track.ID])
			}
			if !f.Track.Available {
				gone++
			}
		}
		if gone < 10 {
			t.Fatalf("seed %d: only %d unavailable favorites in the list", seed, gone)
		}
		// A user without favorites.
		if got := allFavorites(t, svc, uuid.NewString(), 50); len(got) != 0 {
			t.Fatalf("the favorites of nobody: %v", got)
		}
	}
}

// §8.5: a cursor is one of this list or is refused, and it stays valid
// when the favorite it names is removed and when others are added.
func TestFavoriteCursors(t *testing.T) {
	st := newStore(t)
	putUsers(t, st, userA, userB)
	album := testAlbum{id: uuid.NewString(), artist: testArtist{id: uuid.NewString(), name: "Artist"}, title: "Album",
		firstSeen: 1, available: true}
	putAlbums(t, st, album)
	var ids []string
	var rows []testTrack
	for n := range 7 {
		ids = append(ids, fmt.Sprintf("0199a5c0-0000-7000-8000-00000000000%d", n))
		rows = append(rows, testTrack{ids[n], 1, int64(n + 1), "Track", true})
	}
	putTracks(t, st, album.id, rows...)
	clock := &testClock{}
	svc := New(st, clock.now)
	ctx := t.Context()
	// ids[0] is the oldest favorite, ids[5] the most recent; ids[6] is none.
	for n, id := range ids[:6] {
		clock.set(int64(100 + n))
		if err := svc.AddFavorite(ctx, userA, id); err != nil {
			t.Fatal(err)
		}
	}
	first, err := svc.ListFavorites(ctx, userA, 2, nil)
	if err != nil || len(first.Favorites) != 2 || first.Favorites[0].Track.ID != ids[5] || first.Favorites[1].Track.ID != ids[4] || first.Next == "" {
		t.Fatalf("the first page: %+v, %v", first, err)
	}

	other := func(sort, order string, keys ...httpx.CursorKey) string {
		t.Helper()
		c, err := httpx.EncodeCursor(sort, order, keys...)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	for where, cursor := range map[string]string{
		"empty":                   "",
		"not a cursor":            "x",
		"SQL":                     "' OR 1=1 --",
		"of the albums by date":   other(SortAdded, OrderDesc, httpx.IntKey(104), httpx.IDKey(ids[4])),
		"ascending":               other(sortFavorited, OrderAsc, httpx.IntKey(104), httpx.IDKey(ids[4])),
		"of the artists":          other(sortName, OrderAsc, httpx.BytesKey([]byte{1}), httpx.IDKey(ids[4])),
		"with a key too many":     other(sortFavorited, OrderDesc, httpx.IntKey(104), httpx.IDKey(ids[4]), httpx.IDKey(ids[4])),
		"with a key too few":      other(sortFavorited, OrderDesc, httpx.IntKey(104)),
		"with the keys exchanged": other(sortFavorited, OrderDesc, httpx.IDKey(ids[4]), httpx.IntKey(104)),
		"the cursor and more":     first.Next + "A",
	} {
		_, err := svc.ListFavorites(ctx, userA, 2, &cursor)
		wantCode(t, "a cursor "+where, err, 400, "invalid_cursor")
	}

	// The favorite the cursor names is removed, one older than it too, and
	// two are added, one of them with the moment of the cursor and a greater
	// id: the next pages are what is after the cursor now.
	for _, id := range []string{ids[4], ids[2]} {
		if err := svc.RemoveFavorite(ctx, userA, id); err != nil {
			t.Fatal(err)
		}
	}
	clock.set(104)
	if err := svc.AddFavorite(ctx, userA, ids[6]); err != nil {
		t.Fatal(err)
	}
	clock.set(1000)
	if err := svc.AddFavorite(ctx, userA, ids[2]); err != nil {
		t.Fatal(err)
	}
	var rest []string
	after := &first.Next
	for range 5 {
		page, err := svc.ListFavorites(ctx, userA, 2, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range page.Favorites {
			rest = append(rest, f.Track.ID)
		}
		if page.Next == "" {
			break
		}
		after = &page.Next
	}
	if want := []string{ids[3], ids[1], ids[0]}; !slices.Equal(rest, want) {
		t.Fatalf("after the cursor: %v, want %v", rest, want)
	}
	// The cursor of a user is a place in the order, not a secret: with it
	// another user reads their own favorites, never those of the first.
	if page, err := svc.ListFavorites(ctx, userB, 2, &first.Next); err != nil || len(page.Favorites) != 0 || page.Next != "" {
		t.Fatalf("the cursor of another user: %+v, %v", page, err)
	}
}

// Favorites are added, removed and listed at once, by several requests of
// one user and by other users, while an account is deleted: every write is
// whole, no read fails or sees the list out of order, and what is left is
// what the last writes say.
func TestFavoritesAtOnce(t *testing.T) {
	st := newStore(t)
	const leaving = "0199a5c0-0000-7000-8000-0000000000cc"
	putUsers(t, st, userA, userB, leaving)
	album := testAlbum{id: uuid.NewString(), artist: testArtist{id: uuid.NewString(), name: "Artist"}, title: "Album",
		firstSeen: 1, available: true}
	putAlbums(t, st, album)
	var ids []string
	var rows []testTrack
	for n := range 12 {
		ids = append(ids, uuid.NewString())
		rows = append(rows, testTrack{ids[n], 1, int64(n + 1), "Track", n%3 != 0})
	}
	putTracks(t, st, album.id, rows...)
	var ticks struct {
		sync.Mutex
		n int64
	}
	svc := New(st, func() time.Time {
		ticks.Lock()
		defer ticks.Unlock()
		// Three calls in a row have the same moment.
		ticks.n++
		return time.UnixMilli(ticks.n / 3)
	})
	ctx := t.Context()

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
	// Eight requests add the same track for userB at once.
	for range 8 {
		run(func() error { return svc.AddFavorite(ctx, userB, ids[0]) })
	}
	for w := range 4 {
		// userA adds every track, and half of the writers remove the even
		// ones again in every round but the last.
		run(func() error {
			for round := range 5 {
				for n, id := range ids {
					if err := svc.AddFavorite(ctx, userA, id); err != nil {
						return err
					}
					if n%2 == 0 && round < 4 && w%2 == 0 {
						if err := svc.RemoveFavorite(ctx, userA, id); err != nil {
							return err
						}
					}
				}
			}
			return nil
		})
		// userB adds and removes every track but the first.
		run(func() error {
			for range 5 {
				for _, id := range ids[1:] {
					if err := errors.Join(svc.AddFavorite(ctx, userB, id), svc.RemoveFavorite(ctx, userB, id)); err != nil {
						return err
					}
				}
			}
			return nil
		})
		// Readers: the keys of what they read always go down.
		run(func() error {
			for range 20 {
				for _, user := range []string{userA, userB, leaving} {
					if _, err := readFavorites(ctx, svc, user, 1+w, false); err != nil {
						return err
					}
				}
			}
			return nil
		})
	}
	// An account adds favorites while it is deleted: no request fails, and
	// nothing of it is left.
	run(func() error {
		for range 5 {
			for _, id := range ids {
				if err := svc.AddFavorite(ctx, leaving, id); err != nil {
					return fmt.Errorf("adding for an account that is being deleted: %w", err)
				}
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

	// Every track is a favorite of userA once: each writer ended with a
	// round that only adds.
	var gotIDs []string
	for _, f := range allFavorites(t, svc, userA, 5) {
		gotIDs = append(gotIDs, f.id)
	}
	slices.Sort(gotIDs)
	if want := slices.Sorted(slices.Values(ids)); !slices.Equal(gotIDs, want) || favoriteRowsOf(t, st, userA) != len(ids) {
		t.Fatalf("the favorites of userA: %v, want %v", gotIDs, want)
	}
	// The track eight requests added is one favorite of userB, and nothing
	// else is.
	if got := allFavorites(t, svc, userB, 5); len(got) != 1 || got[0].id != ids[0] || favoriteRowsOf(t, st, userB) != 1 {
		t.Fatalf("the favorites of userB: %v, want only %s", got, ids[0])
	}
	if n := favoriteRowsOf(t, st, leaving); n != 0 {
		t.Fatalf("%d favorites of the deleted account", n)
	}
}
