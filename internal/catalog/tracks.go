package catalog

import (
	"context"
	"database/sql"
	"fmt"

	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// The list of the tracks, GET /tracks (docs/proposals/web-client-api.md
// A1): every available track, paginated by key like the list of the
// albums (DESIGN.md §8.5), in four orders, each ending with the id so that
// it is total:
//
//   - title:  (title_key, id);
//   - artist: (artist_key, album_key, album_id, disc, no, id), where
//     artist_key is the key of the artist of the track;
//   - album:  (album_key, album_id, disc, no, id), the order of the album
//     page, album after album;
//   - added:  (first_seen_at, id).

// SortAlbum is the order of the list of the tracks by album. The other
// orders of the tracks have the names of those of the albums.
const SortAlbum = "album"

// trackCursorPrefix makes the order of a cursor of the tracks differ from
// every order of the albums, whose cursors may have the same kinds of keys:
// a cursor of one list is refused by the other.
const trackCursorPrefix = "track_"

// TrackQuery asks for a page of the list of the tracks.
type TrackQuery struct {
	// UserID is the user the page is read for: Favorite is whether the
	// track is one of its favorites.
	UserID string
	// Sort is SortTitle, SortArtist, SortAlbum or SortAdded, and Order one
	// of the Order constants.
	Sort, Order string
	// ArtistID, when not "", keeps only the tracks of the albums of that
	// artist (the artist of the album, not of the track). An id that is
	// no artist's gives an empty list.
	ArtistID string
	// Limit is how many tracks a page has at most, at least 1.
	Limit int
	// After is the cursor of the page before, nil for the first page.
	After *string
}

// TrackPage is a page of the list of the tracks. Next is the cursor of the
// page after it, "" on the last page.
type TrackPage struct {
	Tracks []Track
	Next   string
}

// ListTracks returns a page of the available tracks in the order the query
// asks for. A cursor that is not one of the list in that order is 400
// invalid_cursor.
func (s *Service) ListTracks(ctx context.Context, tq TrackQuery) (TrackPage, error) {
	o, ok := trackOrders[[2]string{tq.Sort, tq.Order}]
	if !ok {
		// The specification enumerates both: the validator refuses any
		// other value before the operation runs.
		return TrackPage{}, fmt.Errorf("catalog: no list of the tracks by %q %q", tq.Sort, tq.Order)
	}
	sort := trackCursorPrefix + tq.Sort
	n := int64(tq.Limit) + 1
	// keys stays nil for the first page.
	var keys []httpx.CursorKey
	if tq.After != nil {
		var err error
		if keys, err = httpx.DecodeCursor(*tq.After, sort, tq.Order, o.kinds...); err != nil {
			return TrackPage{}, err
		}
	}
	var rows []trackRow
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		switch {
		case tq.ArtistID != "":
			rows, err = o.ofArtist(ctx, q, tq.UserID, tq.ArtistID, keys, n)
		case keys == nil:
			rows, err = o.first(ctx, q, tq.UserID, n)
		default:
			rows, err = o.after(ctx, q, tq.UserID, keys, n)
		}
		return err
	})
	if err != nil {
		return TrackPage{}, fmt.Errorf("catalog: listing the tracks: %w", err)
	}
	rows, more := cut(rows, tq.Limit)
	page := TrackPage{Tracks: make([]Track, 0, len(rows))}
	for _, r := range rows {
		page.Tracks = append(page.Tracks, trackInAlbum(r))
	}
	if more {
		if page.Next, err = httpx.EncodeCursor(sort, tq.Order, o.key(rows[len(rows)-1].Track)...); err != nil {
			return TrackPage{}, err
		}
	}
	return page, nil
}

// trackRows converts the rows of a list of the tracks, which have one shape
// and a type of their own for each query of sqlc.
func trackRows[R ~struct {
	Track            store.Track
	AlbumTitle       string
	AlbumYear        sql.NullInt64
	AlbumCoverSha256 sql.NullString
	AlbumArtistID    string
	AlbumArtistName  string
	Favorite         bool
}](rows []R, err error) ([]trackRow, error) {
	if err != nil {
		return nil, err
	}
	out := make([]trackRow, len(rows))
	for i, r := range rows {
		out[i] = trackRow(r)
	}
	return out, nil
}

// trackOrder is one order of the list of the tracks: the kinds of the
// values of its cursor, the values of a row, and its three queries. first
// and after walk the index of the order over every track; ofArtist reads
// the tracks of the albums of one artist and sorts them, for the first page
// (a nil k) and for the others.
type trackOrder struct {
	kinds    []httpx.CursorKind
	key      func(store.Track) []httpx.CursorKey
	first    func(ctx context.Context, q *store.Queries, user string, n int64) ([]trackRow, error)
	after    func(ctx context.Context, q *store.Queries, user string, k []httpx.CursorKey, n int64) ([]trackRow, error)
	ofArtist func(ctx context.Context, q *store.Queries, user, artist string, k []httpx.CursorKey, n int64) ([]trackRow, error)
}

// The keys of the orders of the list of the tracks, each ending with the
// id: the values a cursor holds, in the order of the comparison.
var (
	trackTitleKinds  = []httpx.CursorKind{httpx.CursorBytes, httpx.CursorID}
	trackArtistKinds = []httpx.CursorKind{httpx.CursorBytes, httpx.CursorBytes, httpx.CursorID, httpx.CursorInt, httpx.CursorInt, httpx.CursorID}
	trackAlbumKinds  = []httpx.CursorKind{httpx.CursorBytes, httpx.CursorID, httpx.CursorInt, httpx.CursorInt, httpx.CursorID}
	trackAddedKinds  = []httpx.CursorKind{httpx.CursorInt, httpx.CursorID}
)

func trackTitleKey(t store.Track) []httpx.CursorKey {
	return []httpx.CursorKey{httpx.BytesKey(t.TitleKey), httpx.IDKey(t.ID)}
}

func trackArtistKey(t store.Track) []httpx.CursorKey {
	return []httpx.CursorKey{httpx.BytesKey(t.ArtistKey), httpx.BytesKey(t.AlbumKey), httpx.IDKey(t.AlbumID),
		httpx.IntKey(t.Disc), httpx.IntKey(t.No), httpx.IDKey(t.ID)}
}

func trackAlbumKey(t store.Track) []httpx.CursorKey {
	return []httpx.CursorKey{httpx.BytesKey(t.AlbumKey), httpx.IDKey(t.AlbumID), httpx.IntKey(t.Disc), httpx.IntKey(t.No),
		httpx.IDKey(t.ID)}
}

func trackAddedKey(t store.Track) []httpx.CursorKey {
	return []httpx.CursorKey{httpx.IntKey(t.FirstSeenAt), httpx.IDKey(t.ID)}
}

// trackOrders are the orders of the list of the tracks, by sort and order,
// with the three queries of each.
var trackOrders = map[[2]string]trackOrder{
	{SortTitle, OrderAsc}: {
		kinds: trackTitleKinds,
		key:   trackTitleKey,
		first: func(ctx context.Context, q *store.Queries, user string, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByTitleAsc(ctx, store.ListTracksByTitleAscParams{UserID: user, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, user string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByTitleAscAfter(ctx, store.ListTracksByTitleAscAfterParams{UserID: user,
				TitleKey: k[0].Bytes(), AfterID: k[1].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, user, artist string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			first, k := pageKey(k, trackTitleKinds)
			return trackRows(q.ListArtistTracksByTitleAsc(ctx, store.ListArtistTracksByTitleAscParams{UserID: user, ArtistID: artist, FirstPage: first,
				TitleKey: k[0].Bytes(), AfterID: k[1].ID(), PageSize: n}))
		},
	},
	{SortTitle, OrderDesc}: {
		kinds: trackTitleKinds,
		key:   trackTitleKey,
		first: func(ctx context.Context, q *store.Queries, user string, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByTitleDesc(ctx, store.ListTracksByTitleDescParams{UserID: user, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, user string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByTitleDescAfter(ctx, store.ListTracksByTitleDescAfterParams{UserID: user,
				TitleKey: k[0].Bytes(), AfterID: k[1].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, user, artist string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			first, k := pageKey(k, trackTitleKinds)
			return trackRows(q.ListArtistTracksByTitleDesc(ctx, store.ListArtistTracksByTitleDescParams{UserID: user, ArtistID: artist, FirstPage: first,
				TitleKey: k[0].Bytes(), AfterID: k[1].ID(), PageSize: n}))
		},
	},
	{SortArtist, OrderAsc}: {
		kinds: trackArtistKinds,
		key:   trackArtistKey,
		first: func(ctx context.Context, q *store.Queries, user string, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByArtistAsc(ctx, store.ListTracksByArtistAscParams{UserID: user, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, user string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByArtistAscAfter(ctx, store.ListTracksByArtistAscAfterParams{UserID: user,
				ArtistKey: k[0].Bytes(), AlbumKey: k[1].Bytes(), AfterAlbumID: k[2].ID(), Disc: k[3].Int(), TrackNo: k[4].Int(), AfterID: k[5].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, user, artist string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			first, k := pageKey(k, trackArtistKinds)
			return trackRows(q.ListArtistTracksByArtistAsc(ctx, store.ListArtistTracksByArtistAscParams{UserID: user, ArtistID: artist, FirstPage: first,
				ArtistKey: k[0].Bytes(), AlbumKey: k[1].Bytes(), AfterAlbumID: k[2].ID(), Disc: k[3].Int(), TrackNo: k[4].Int(), AfterID: k[5].ID(), PageSize: n}))
		},
	},
	{SortArtist, OrderDesc}: {
		kinds: trackArtistKinds,
		key:   trackArtistKey,
		first: func(ctx context.Context, q *store.Queries, user string, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByArtistDesc(ctx, store.ListTracksByArtistDescParams{UserID: user, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, user string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByArtistDescAfter(ctx, store.ListTracksByArtistDescAfterParams{UserID: user,
				ArtistKey: k[0].Bytes(), AlbumKey: k[1].Bytes(), AfterAlbumID: k[2].ID(), Disc: k[3].Int(), TrackNo: k[4].Int(), AfterID: k[5].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, user, artist string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			first, k := pageKey(k, trackArtistKinds)
			return trackRows(q.ListArtistTracksByArtistDesc(ctx, store.ListArtistTracksByArtistDescParams{UserID: user, ArtistID: artist, FirstPage: first,
				ArtistKey: k[0].Bytes(), AlbumKey: k[1].Bytes(), AfterAlbumID: k[2].ID(), Disc: k[3].Int(), TrackNo: k[4].Int(), AfterID: k[5].ID(), PageSize: n}))
		},
	},
	{SortAlbum, OrderAsc}: {
		kinds: trackAlbumKinds,
		key:   trackAlbumKey,
		first: func(ctx context.Context, q *store.Queries, user string, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByAlbumAsc(ctx, store.ListTracksByAlbumAscParams{UserID: user, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, user string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByAlbumAscAfter(ctx, store.ListTracksByAlbumAscAfterParams{UserID: user,
				AlbumKey: k[0].Bytes(), AfterAlbumID: k[1].ID(), Disc: k[2].Int(), TrackNo: k[3].Int(), AfterID: k[4].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, user, artist string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			first, k := pageKey(k, trackAlbumKinds)
			return trackRows(q.ListArtistTracksByAlbumAsc(ctx, store.ListArtistTracksByAlbumAscParams{UserID: user, ArtistID: artist, FirstPage: first,
				AlbumKey: k[0].Bytes(), AfterAlbumID: k[1].ID(), Disc: k[2].Int(), TrackNo: k[3].Int(), AfterID: k[4].ID(), PageSize: n}))
		},
	},
	{SortAlbum, OrderDesc}: {
		kinds: trackAlbumKinds,
		key:   trackAlbumKey,
		first: func(ctx context.Context, q *store.Queries, user string, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByAlbumDesc(ctx, store.ListTracksByAlbumDescParams{UserID: user, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, user string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByAlbumDescAfter(ctx, store.ListTracksByAlbumDescAfterParams{UserID: user,
				AlbumKey: k[0].Bytes(), AfterAlbumID: k[1].ID(), Disc: k[2].Int(), TrackNo: k[3].Int(), AfterID: k[4].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, user, artist string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			first, k := pageKey(k, trackAlbumKinds)
			return trackRows(q.ListArtistTracksByAlbumDesc(ctx, store.ListArtistTracksByAlbumDescParams{UserID: user, ArtistID: artist, FirstPage: first,
				AlbumKey: k[0].Bytes(), AfterAlbumID: k[1].ID(), Disc: k[2].Int(), TrackNo: k[3].Int(), AfterID: k[4].ID(), PageSize: n}))
		},
	},
	{SortAdded, OrderAsc}: {
		kinds: trackAddedKinds,
		key:   trackAddedKey,
		first: func(ctx context.Context, q *store.Queries, user string, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByAddedAsc(ctx, store.ListTracksByAddedAscParams{UserID: user, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, user string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByAddedAscAfter(ctx, store.ListTracksByAddedAscAfterParams{UserID: user,
				FirstSeenAt: k[0].Int(), AfterID: k[1].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, user, artist string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			first, k := pageKey(k, trackAddedKinds)
			return trackRows(q.ListArtistTracksByAddedAsc(ctx, store.ListArtistTracksByAddedAscParams{UserID: user, ArtistID: artist, FirstPage: first,
				FirstSeenAt: k[0].Int(), AfterID: k[1].ID(), PageSize: n}))
		},
	},
	{SortAdded, OrderDesc}: {
		kinds: trackAddedKinds,
		key:   trackAddedKey,
		first: func(ctx context.Context, q *store.Queries, user string, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByAddedDesc(ctx, store.ListTracksByAddedDescParams{UserID: user, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, user string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			return trackRows(q.ListTracksByAddedDescAfter(ctx, store.ListTracksByAddedDescAfterParams{UserID: user,
				FirstSeenAt: k[0].Int(), AfterID: k[1].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, user, artist string, k []httpx.CursorKey, n int64) ([]trackRow, error) {
			first, k := pageKey(k, trackAddedKinds)
			return trackRows(q.ListArtistTracksByAddedDesc(ctx, store.ListArtistTracksByAddedDescParams{UserID: user, ArtistID: artist, FirstPage: first,
				FirstSeenAt: k[0].Int(), AfterID: k[1].ID(), PageSize: n}))
		},
	},
}
