//go:build perf

package perfgen

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"vibrance/internal/names"
	"vibrance/internal/search"
	"vibrance/internal/store"
)

// openDataset opens the database of a dataset, which no server is using.
func openDataset(t *testing.T, stateDir string) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(stateDir, "vibrance.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	return st
}

// measureSearchRows relates the time of a search of the tracks to how many
// tracks it matches: the full-text index ranks every row a search matches
// before it can return the best ones (NOTES.md N-161). The count is that of
// the expression Parse makes of these words, each a quoted prefix.
func measureSearchRows(t *testing.T, stateDir string) {
	ctx := t.Context()
	st := openDataset(t, stateDir)
	queries := append(slices.Clone(broadSearches), "love", "lov", "live", "night", "blue", "feat", "amore", "i love you", "symphony no", "zzzzqqq")
	for _, query := range queries {
		var expr []string
		for _, word := range strings.Fields(query) {
			expr = append(expr, `"`+word+`"*`)
		}
		var (
			matched int64
			fastest = time.Hour
		)
		err := st.Read(ctx, func(q *store.Queries) error {
			// The test's own statement: the count is not a query of the
			// server (I7 keeps the full-text statements in internal/search).
			row := q.Conn().QueryRowContext(ctx, `SELECT count(*) FROM search_tracks WHERE search_tracks MATCH ?`, strings.Join(expr, " "))
			if err := row.Scan(&matched); err != nil {
				return err
			}
			for range 5 {
				began := time.Now()
				if _, err := search.Find(ctx, q, "", search.Parse(query), search.Kinds{Tracks: true}, 10); err != nil {
					return err
				}
				fastest = min(fastest, time.Since(began))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		fmt.Printf("PERF search rows: %-14q matches %6d tracks of %d; the tracks, limit 10, the fastest of 5: %s\n",
			query, matched, Full.Tracks, ms(fastest))
	}
}

// measureArtistRename measures what a cycle of the scanner writes when the
// artist with the most albums changes how its name is written: the commit
// of one of its albums gives the artist its name and sort key, gives every
// album of the artist the sort key, and writes again the full-text rows of
// all of them and of their tracks, in one write transaction (DESIGN.md
// §10.1; library.putArtist and library.syncSearch). It runs the same
// queries. The readers do not wait for it; every other write does.
func measureArtistRename(t *testing.T, stateDir string) {
	ctx := t.Context()
	st := openDataset(t, stateDir)
	r := &report{t: t}
	defer r.print()

	var states []store.ListAlbumStatesRow
	err := st.Read(ctx, func(q *store.Queries) (err error) {
		states, err = q.ListAlbumStates(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	albums := map[string]int{}
	for _, a := range states {
		albums[a.ArtistID]++
	}
	largest := states[0].ArtistID
	for id, n := range albums {
		if n > albums[largest] {
			largest = id
		}
	}
	var artist store.Artist
	err = st.Read(ctx, func(q *store.Queries) (err error) {
		artist, err = q.GetArtist(ctx, largest)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// Another case of the same name, or for a script without cases another
	// white space around it: the same identity key, so the same artist
	// (DESIGN.md §5.3).
	other := strings.ToUpper(artist.Name)
	if other == artist.Name {
		other += " "
	}
	spellings := []string{other, artist.Name}
	if names.IdentityKey(spellings[0]) != names.IdentityKey(spellings[1]) || spellings[0] == spellings[1] {
		t.Fatalf("no other case of the name %q", artist.Name)
	}
	slowest := time.Duration(0)
	for i := range 4 {
		name := spellings[i%2]
		began := time.Now()
		if err := st.WithWriteTx(ctx, func(q *store.Queries) error { return rename(ctx, q, artist.ID, name) }); err != nil {
			t.Fatal(err)
		}
		slowest = max(slowest, time.Since(began))
	}
	r.value(fmt.Sprintf("an artist of %d albums renamed: its write transaction, the slowest of 4", albums[largest]),
		slowest.Seconds(), 0, "s")
}

// rename is what library.putArtist and library.syncSearch write for an
// artist whose name changed.
func rename(ctx context.Context, q *store.Queries, id, name string) error {
	key := names.SortKey(name)
	if err := q.UpsertArtist(ctx, store.UpsertArtistParams{ID: id, Name: name, SortKey: key}); err != nil {
		return err
	}
	if err := q.SetArtistKeyOfAlbums(ctx, store.SetArtistKeyOfAlbumsParams{ArtistKey: key, ArtistID: id}); err != nil {
		return err
	}
	albums, err := q.ListAlbumIDsByArtist(ctx, id)
	if err != nil {
		return err
	}
	for _, album := range albums {
		if err := search.SyncAlbum(ctx, q.Conn(), album); err != nil {
			return err
		}
	}
	return search.SyncArtist(ctx, q.Conn(), id)
}

// checkRows verifies that a dataset has the rows of its size, so that a
// measure is of the dataset it names.
func checkRows(t *testing.T, stateDir string, size Size) {
	t.Helper()
	ctx := t.Context()
	st, err := store.Open(ctx, filepath.Join(stateDir, "vibrance.db"))
	if err != nil {
		t.Fatal(err)
	}
	var rows store.CountRowsRow
	err = st.Read(ctx, func(q *store.Queries) (err error) {
		rows, err = q.CountRows(ctx)
		return err
	})
	if err := errors.Join(err, st.Close()); err != nil {
		t.Fatal(err)
	}
	want := store.CountRowsRow{Users: int64(size.Users), Playlists: int64(size.Playlists), PlaylistItems: int64(size.Playlists * size.PlaylistItems),
		Favorites: int64(size.Favorites), Artists: int64(size.Artists), Albums: int64(size.Albums), Tracks: int64(size.Tracks)}
	if rows != want {
		t.Fatalf("the dataset has %+v, want %+v", rows, want)
	}
}
