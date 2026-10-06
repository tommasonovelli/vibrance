package catalog

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"vibrance/internal/httpx"
	"vibrance/internal/names"
	"vibrance/internal/store"
)

// The tests of the catalog run on a real database, which they fill with the
// queries the indexer uses (DESIGN.md §12.1).

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "vibrance.db"))
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

// testArtist and testAlbum are what a test puts in the index.
type testArtist struct {
	id, name string
}

type testAlbum struct {
	id        string
	artist    testArtist
	title     string
	year      int // 0: none
	firstSeen int64
	available bool
	cover     string // the SHA-256 of cover.jpg; "": none
}

func (a testAlbum) yearKey() int64 {
	if a.year == 0 {
		return 10000
	}
	return int64(a.year)
}

// putAlbums writes the albums and their artists, as the indexer would.
func putAlbums(t *testing.T, st *store.Store, albums ...testAlbum) {
	t.Helper()
	ctx := t.Context()
	err := st.WithWriteTx(ctx, func(q *store.Queries) error {
		for _, a := range albums {
			if err := putAlbum(ctx, q, a); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func putAlbum(ctx context.Context, q *store.Queries, a testAlbum) error {
	artistKey := names.SortKey(a.artist.name)
	if err := q.UpsertArtist(ctx, store.UpsertArtistParams{ID: a.artist.id, Name: a.artist.name, SortKey: artistKey}); err != nil {
		return err
	}
	err := q.UpsertAlbum(ctx, store.UpsertAlbumParams{
		ID: a.id, ArtistID: a.artist.id, ArtistKey: artistKey, Title: a.title, TitleKey: names.SortKey(a.title),
		Year: sql.NullInt64{Int64: int64(a.year), Valid: a.year != 0}, YearKey: a.yearKey(),
		RelPath: a.artist.name + "/" + a.title, AlbumRevision: 1, RenderVersion: "r", ReceiptHash: "h",
		FirstSeenAt: a.firstSeen, UpdatedAt: a.firstSeen,
		CoverRel: nullText(a.cover, "cover.jpg"), CoverSha256: nullText(a.cover, a.cover), CoverMime: nullText(a.cover, "image/jpeg"),
		CoverSize: sql.NullInt64{Int64: 1000, Valid: a.cover != ""}, CoverMtimeNs: sql.NullInt64{Int64: 1, Valid: a.cover != ""},
	})
	if err != nil || a.available {
		return err
	}
	return q.SetAlbumUnavailable(ctx, store.SetAlbumUnavailableParams{UpdatedAt: a.firstSeen + 1, ID: a.id})
}

// nullText is value, or NULL when cover is "".
func nullText(cover, value string) sql.NullString {
	return sql.NullString{String: value, Valid: cover != ""}
}

// sortKeys remembers the keys of names.SortKey, which makes a collator for
// each call: the oracle compares the same few names many times.
var sortKeys sync.Map

func sortKey(s string) []byte {
	if k, ok := sortKeys.Load(s); ok {
		return k.([]byte)
	}
	k := names.SortKey(s)
	sortKeys.Store(s, k)
	return k
}

// albumOrderOf compares two albums in the order of a list (§8.5), written
// again from the design: the oracle of the tests.
func albumOrderOf(sort string) func(a, b testAlbum) int {
	title := func(a, b testAlbum) int { return bytes.Compare(sortKey(a.title), sortKey(b.title)) }
	id := func(a, b testAlbum) int { return cmp.Compare(a.id, b.id) }
	switch sort {
	case SortTitle:
		return func(a, b testAlbum) int { return cmp.Or(title(a, b), id(a, b)) }
	case SortArtist:
		return func(a, b testAlbum) int {
			return cmp.Or(bytes.Compare(sortKey(a.artist.name), sortKey(b.artist.name)),
				cmp.Compare(a.yearKey(), b.yearKey()), title(a, b), id(a, b))
		}
	case SortYear:
		return func(a, b testAlbum) int { return cmp.Or(cmp.Compare(a.yearKey(), b.yearKey()), title(a, b), id(a, b)) }
	case SortAdded:
		return func(a, b testAlbum) int { return cmp.Or(cmp.Compare(a.firstSeen, b.firstSeen), id(a, b)) }
	}
	panic("no order " + sort)
}

// wantAlbums is the list of the available albums of artistID ("" for all)
// in an order, as ids.
func wantAlbums(albums []testAlbum, sort, order, artistID string) []string {
	var keep []testAlbum
	for _, a := range albums {
		if a.available && (artistID == "" || a.artist.id == artistID) {
			keep = append(keep, a)
		}
	}
	slices.SortFunc(keep, albumOrderOf(sort))
	if order == OrderDesc {
		slices.Reverse(keep)
	}
	ids := make([]string, 0, len(keep))
	for _, a := range keep {
		ids = append(ids, a.id)
	}
	return ids
}

// allAlbums reads every page of a list of the albums, and checks that every
// page but the last is full and that only the last has no cursor.
func allAlbums(t *testing.T, svc *Service, q AlbumQuery) []string {
	t.Helper()
	ids, err := readAlbums(t.Context(), svc, q, true)
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// readAlbums is allAlbums for a goroutine of a test, which cannot stop it.
// When the index does not change (settled), an empty last page after a
// cursor is an error: the page before it knew it was the last.
func readAlbums(ctx context.Context, svc *Service, q AlbumQuery, settled bool) ([]string, error) {
	var ids []string
	for pages := 0; pages <= 10000; pages++ {
		page, err := svc.ListAlbums(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("%+v: %w", q, err)
		}
		for _, a := range page.Albums {
			ids = append(ids, a.ID)
		}
		switch {
		case settled && page.Next == "" && len(page.Albums) == 0 && pages > 0:
			return nil, fmt.Errorf("%+v: an empty last page after a cursor", q)
		case page.Next == "":
			return ids, nil
		case len(page.Albums) != q.Limit:
			return nil, fmt.Errorf("%+v: a page of %d albums with a cursor", q, len(page.Albums))
		}
		q.After = &page.Next
	}
	return nil, fmt.Errorf("%+v: no end", q)
}

func wantCode(t *testing.T, where string, err error, status int, code string) {
	t.Helper()
	var e *httpx.Error
	if !errors.As(err, &e) || e.Status != status || e.Code != code {
		t.Fatalf("%s: %v, want %d %s", where, err, status, code)
	}
}

func wantNotFound(t *testing.T, where string, err error, code string) {
	t.Helper()
	wantCode(t, where, err, http.StatusNotFound, code)
}

func ptr[T any](v T) *T { return &v }
