package library

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"vibrance/internal/search"
	"vibrance/internal/store"
)

// This file is everything that runs inside the write transaction of an
// indexing, and what it shares with the read before it. A write
// transaction holds the only write connection of the server, so nothing
// here may touch the disk or run a process (DESIGN.md I11): the code reads
// and writes the database and nothing else, and a test keeps the indexer,
// the Root and the media adapter out of this file.

// snapshot is what the index has of an album when its indexing begins
// (§6.3 step 3).
type snapshot struct {
	// album is nil for an album that was never indexed.
	album *store.GetIndexedAlbumRow
	// tracks are all its rows, available or not.
	tracks []store.Track
}

// errIndexChanged: the rows of the album are no longer those its plan was
// made from.
var errIndexChanged = errors.New("the rows of the album changed while it was being indexed")

// readSnapshot reads the rows of an album. In a read transaction the two
// queries see one state of the database.
func readSnapshot(ctx context.Context, q *store.Queries, albumID string) (snapshot, error) {
	var s snapshot
	album, err := q.GetIndexedAlbum(ctx, albumID)
	switch {
	case err == nil:
		s.album = &album
	case !errors.Is(err, sql.ErrNoRows):
		return snapshot{}, fmt.Errorf("reading the album %s: %w", albumID, err)
	}
	if s.tracks, err = q.ListTracksByAlbum(ctx, albumID); err != nil {
		return snapshot{}, fmt.Errorf("reading the tracks of the album %s: %w", albumID, err)
	}
	return s, nil
}

// commitAlbum writes an album in the write transaction of q (§6.3 step 8):
// its artist, the album, its tracks, its counters and the full-text rows of
// all of them. Any error leaves nothing written.
//
// The write was computed from rows read before the transaction began. If
// they are no longer the rows of the album (another indexing of the same
// album, or a job that rewrites fingerprints, committed in between), the
// plan may pair a row that is not what it was: nothing is written and the
// error is errIndexChanged.
func commitAlbum(ctx context.Context, q *store.Queries, w albumWrite) error {
	id := w.album.ID
	current, err := readSnapshot(ctx, q, id)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, w.before) {
		return errIndexChanged
	}
	renamed, err := putArtist(ctx, q, w.artist)
	if err != nil {
		return err
	}
	if err := q.UpsertAlbum(ctx, w.album); err != nil {
		return fmt.Errorf("writing the album %s: %w", id, err)
	}
	// A plan never gives a row an occurrence that another row had before
	// it, so the rows can be written in any order.
	for _, t := range w.tracks {
		if err := q.UpsertTrack(ctx, t); err != nil {
			return fmt.Errorf("writing the track %s: %w", t.ID, err)
		}
	}
	for _, t := range w.gone {
		if err := q.SetTrackUnavailable(ctx, t); err != nil {
			return fmt.Errorf("marking the track %s unavailable: %w", t.ID, err)
		}
	}
	if err := q.UpdateAlbumCounters(ctx, id); err != nil {
		return fmt.Errorf("counting the tracks of the album %s: %w", id, err)
	}
	return syncSearch(ctx, q, w, renamed)
}

// putArtist creates the artist of an album, or gives it the name the album
// has for it. Two names with one identity key are one artist (§5.4), which
// has the name of the album indexed last. renamed is true when a known
// artist changed its name: its albums get its new sort key.
func putArtist(ctx context.Context, q *store.Queries, a store.UpsertArtistParams) (renamed bool, err error) {
	old, err := q.GetArtist(ctx, a.ID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, fmt.Errorf("reading the artist %s: %w", a.ID, err)
	case old.Name == a.Name && bytes.Equal(old.SortKey, a.SortKey):
		return false, nil
	default:
		renamed = true
	}
	if err := q.UpsertArtist(ctx, a); err != nil {
		return false, fmt.Errorf("writing the artist %s: %w", a.ID, err)
	}
	if renamed {
		err := q.SetArtistKeyOfAlbums(ctx, store.SetArtistKeyOfAlbumsParams{ArtistKey: a.SortKey, ArtistID: a.ID})
		if err != nil {
			return false, fmt.Errorf("giving the albums of the artist %s its sort key: %w", a.ID, err)
		}
	}
	return renamed, nil
}

// syncSearch brings the full-text tables up to date with what commitAlbum
// wrote, in the same transaction (§10.1): the rows of the album and of its
// tracks, the row of its artist, the row of the artist it had before, which
// may have no available album left, and, when the artist changed its name,
// the rows of its other albums, which hold that name.
func syncSearch(ctx context.Context, q *store.Queries, w albumWrite, renamed bool) error {
	albums := []string{w.album.ID}
	if renamed {
		var err error
		if albums, err = q.ListAlbumIDsByArtist(ctx, w.artist.ID); err != nil {
			return fmt.Errorf("listing the albums of the artist %s: %w", w.artist.ID, err)
		}
	}
	for _, id := range albums {
		if err := search.SyncAlbum(ctx, q.Conn(), id); err != nil {
			return err
		}
	}
	artists := []string{w.artist.ID}
	if b := w.before.album; b != nil && b.Album.ArtistID != w.artist.ID {
		artists = append(artists, b.Album.ArtistID)
	}
	for _, id := range artists {
		if err := search.SyncArtist(ctx, q.Conn(), id); err != nil {
			return err
		}
	}
	return nil
}
