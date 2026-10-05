package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"vibrance/internal/store"
)

// The searches of the three tables (DESIGN.md §10.2). The best rows come
// first: bm25 is smaller for a better match, and a word found in the name
// or the title weighs ten times one found in the other columns, so that
// "blue" finds the album Blue before the albums of an artist called Blue.
// Rows of the same rank are in the order of their rowid, so that a search
// always answers the same. The only values are the expression of Parse and
// the limit.
const (
	matchArtists = `SELECT rowid FROM search_artists WHERE search_artists MATCH ?
ORDER BY bm25(search_artists), rowid LIMIT ?`
	matchAlbums = `SELECT rowid FROM search_albums WHERE search_albums MATCH ?
ORDER BY bm25(search_albums, 10.0, 1.0), rowid LIMIT ?`
	matchTracks = `SELECT rowid FROM search_tracks WHERE search_tracks MATCH ?
ORDER BY bm25(search_tracks, 10.0, 1.0, 1.0), rowid LIMIT ?`
)

// Kinds says which of the three tables a search looks in.
type Kinds struct {
	Artists, Albums, Tracks bool
}

// Results are the rows a search found, the best first. A kind that was not
// asked for is empty.
type Results struct {
	Artists []store.GetListedArtistBySeqRow
	Albums  []store.GetAvailableAlbumBySeqRow
	Tracks  []store.GetAvailableTrackBySeqRow
}

// Find searches the kinds asked for and returns at most limit rows of
// each, with whether each track is a favorite of userID. Only what is
// available is found (§10.1).
//
// q is a read transaction: the full-text tables and the rows they describe
// change together, so within one transaction every row found can be read.
// One that cannot is an error, not a result left out: the index would be
// wrong.
func Find(ctx context.Context, q *store.Queries, userID string, query Query, kinds Kinds, limit int) (res Results, err error) {
	if query.Empty() {
		return Results{}, nil
	}
	if kinds.Artists {
		res.Artists, err = find(ctx, q, matchArtists, query, limit, func(seq int64) (store.GetListedArtistBySeqRow, error) {
			return q.GetListedArtistBySeq(ctx, seq)
		})
		if err != nil {
			return Results{}, fmt.Errorf("search: searching the artists: %w", err)
		}
	}
	if kinds.Albums {
		res.Albums, err = find(ctx, q, matchAlbums, query, limit, func(seq int64) (store.GetAvailableAlbumBySeqRow, error) {
			return q.GetAvailableAlbumBySeq(ctx, seq)
		})
		if err != nil {
			return Results{}, fmt.Errorf("search: searching the albums: %w", err)
		}
	}
	if kinds.Tracks {
		res.Tracks, err = find(ctx, q, matchTracks, query, limit, func(seq int64) (store.GetAvailableTrackBySeqRow, error) {
			return q.GetAvailableTrackBySeq(ctx, store.GetAvailableTrackBySeqParams{UserID: userID, Seq: seq})
		})
		if err != nil {
			return Results{}, fmt.Errorf("search: searching the tracks: %w", err)
		}
	}
	return res, nil
}

// errNotAvailable is the error of a full-text row that describes nothing
// available. It is not sql.ErrNoRows, which a caller would take for "there
// is no such thing".
var errNotAvailable = errors.New("the full-text row describes nothing that is available")

// find runs one of the three searches and reads, in its order, the row
// each rowid it found describes.
func find[T any](ctx context.Context, q *store.Queries, statement string, query Query, limit int,
	read func(seq int64) (T, error)) ([]T, error) {
	seqs, err := match(ctx, q.Conn(), statement, query, limit)
	if err != nil {
		return nil, err
	}
	found := make([]T, 0, len(seqs))
	for _, seq := range seqs {
		row, err := read(seq)
		switch {
		case errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil:
			return nil, fmt.Errorf("rowid %d: %w", seq, errNotAvailable)
		case err != nil:
			return nil, fmt.Errorf("reading the row %d: %w", seq, err)
		}
		found = append(found, row)
	}
	return found, nil
}

// match returns the rowids one of the three searches finds, in its order.
func match(ctx context.Context, db store.DBTX, statement string, query Query, limit int) (seqs []int64, err error) {
	rows, err := db.QueryContext(ctx, statement, query.match, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			seqs, err = nil, cerr
		}
	}()
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			return nil, err
		}
		seqs = append(seqs, seq)
	}
	return seqs, rows.Err()
}
