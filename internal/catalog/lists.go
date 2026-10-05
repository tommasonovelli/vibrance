package catalog

import (
	"context"
	"fmt"

	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// The orders of the list of the albums and their directions: the values of
// the parameters sort and order of the specification (DESIGN.md §8.5).
const (
	SortTitle  = "title"
	SortArtist = "artist"
	SortYear   = "year"
	SortAdded  = "added"
	OrderAsc   = "asc"
	OrderDesc  = "desc"
)

// sortName is the order of the list of the artists, in its cursors: it is
// not a parameter, and differs from every order of the albums, so that a
// cursor of one list is refused by the other.
const sortName = "name"

// AlbumQuery asks for a page of the list of the albums.
type AlbumQuery struct {
	// Sort is one of the Sort constants and Order one of the Order ones.
	Sort, Order string
	// ArtistID, when not "", keeps only the albums of that artist. An id
	// that is no artist's gives an empty list.
	ArtistID string
	// Limit is how many albums a page has at most, at least 1.
	Limit int
	// After is the cursor of the page before, nil for the first page.
	After *string
}

// ListArtists returns a page of the artists that have at least one
// available album, ordered by (sort_key, id) (§8.5). after is the cursor of
// the page before, nil for the first page; one that is not a cursor of this
// list, the empty string included, is 400 invalid_cursor.
func (s *Service) ListArtists(ctx context.Context, limit int, after *string) (ArtistPage, error) {
	var (
		rows []artistRow
		err  error
	)
	n := int64(limit) + 1
	if after == nil {
		err = s.store.Read(ctx, func(q *store.Queries) error {
			rows, err = artistRows(q.ListArtistsByName(ctx, n))
			return err
		})
	} else {
		keys, cerr := httpx.DecodeCursor(*after, sortName, OrderAsc, httpx.CursorBytes, httpx.CursorID)
		if cerr != nil {
			return ArtistPage{}, cerr
		}
		err = s.store.Read(ctx, func(q *store.Queries) error {
			rows, err = artistRows(q.ListArtistsByNameAfter(ctx, store.ListArtistsByNameAfterParams{
				SortKey: keys[0].Bytes(), AfterID: keys[1].ID(), PageSize: n}))
			return err
		})
	}
	if err != nil {
		return ArtistPage{}, fmt.Errorf("catalog: listing the artists: %w", err)
	}
	rows, more := cut(rows, limit)
	page := ArtistPage{Artists: make([]ArtistSummary, 0, len(rows))}
	for _, r := range rows {
		page.Artists = append(page.Artists, ArtistSummary{ArtistRef: ArtistRef{ID: r.Artist.ID, Name: r.Artist.Name},
			AlbumCount: int(r.AlbumCount)})
	}
	if more {
		last := rows[len(rows)-1].Artist
		if page.Next, err = httpx.EncodeCursor(sortName, OrderAsc, httpx.BytesKey(last.SortKey), httpx.IDKey(last.ID)); err != nil {
			return ArtistPage{}, err
		}
	}
	return page, nil
}

// ListAlbums returns a page of the available albums in the order the query
// asks for (§8.5). A cursor that is not one of the list in that order is
// 400 invalid_cursor.
func (s *Service) ListAlbums(ctx context.Context, aq AlbumQuery) (AlbumPage, error) {
	o, ok := albumOrders[[2]string{aq.Sort, aq.Order}]
	if !ok {
		// The specification enumerates both: the validator refuses any
		// other value before the operation runs.
		return AlbumPage{}, fmt.Errorf("catalog: no list of the albums by %q %q", aq.Sort, aq.Order)
	}
	n := int64(aq.Limit) + 1
	// keys stays nil for the first page.
	var keys []httpx.CursorKey
	if aq.After != nil {
		var err error
		if keys, err = httpx.DecodeCursor(*aq.After, aq.Sort, aq.Order, o.kinds...); err != nil {
			return AlbumPage{}, err
		}
	}
	var rows []albumRow
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		switch {
		case aq.ArtistID != "":
			rows, err = o.ofArtist(ctx, q, aq.ArtistID, keys, n)
		case keys == nil:
			rows, err = o.first(ctx, q, n)
		default:
			rows, err = o.after(ctx, q, keys, n)
		}
		return err
	})
	if err != nil {
		return AlbumPage{}, fmt.Errorf("catalog: listing the albums: %w", err)
	}
	rows, more := cut(rows, aq.Limit)
	page := AlbumPage{Albums: make([]Album, 0, len(rows))}
	for _, r := range rows {
		page.Albums = append(page.Albums, albumOf(r.Album, r.ArtistName))
	}
	if more {
		if page.Next, err = httpx.EncodeCursor(aq.Sort, aq.Order, o.key(rows[len(rows)-1].Album)...); err != nil {
			return AlbumPage{}, err
		}
	}
	return page, nil
}

// cut keeps the first limit rows, and says whether there were more: a page
// reads one row more than it shows, so that the last page has no cursor.
func cut[R any](rows []R, limit int) ([]R, bool) {
	if len(rows) > limit {
		return rows[:limit], true
	}
	return rows, false
}

// artistRow is a row of the list of the artists, whichever query read it.
type artistRow struct {
	Artist     store.Artist
	AlbumCount int64
}

// albumRow is a row of a list of the albums, whichever query read it.
type albumRow struct {
	Album      store.Album
	ArtistName string
}

// albumRows converts the rows of a list of the albums, which have one shape
// and a type of their own for each query of sqlc.
func albumRows[R ~struct {
	Album      store.Album
	ArtistName string
}](rows []R, err error) ([]albumRow, error) {
	if err != nil {
		return nil, err
	}
	out := make([]albumRow, len(rows))
	for i, r := range rows {
		out[i] = albumRow(r)
	}
	return out, nil
}

// artistRows is albumRows for the list of the artists.
func artistRows[R ~struct {
	Artist     store.Artist
	AlbumCount int64
}](rows []R, err error) ([]artistRow, error) {
	if err != nil {
		return nil, err
	}
	out := make([]artistRow, len(rows))
	for i, r := range rows {
		out[i] = artistRow(r)
	}
	return out, nil
}

// albumOrder is one order of the list of the albums: the kinds of the
// values of its cursor, the values of a row, and its three queries. first
// and after walk the index of the order over every album; ofArtist reads
// the albums of one artist and sorts them, for the first page (a nil k) and
// for the others.
type albumOrder struct {
	kinds    []httpx.CursorKind
	key      func(store.Album) []httpx.CursorKey
	first    func(ctx context.Context, q *store.Queries, n int64) ([]albumRow, error)
	after    func(ctx context.Context, q *store.Queries, k []httpx.CursorKey, n int64) ([]albumRow, error)
	ofArtist func(ctx context.Context, q *store.Queries, artist string, k []httpx.CursorKey, n int64) ([]albumRow, error)
}
