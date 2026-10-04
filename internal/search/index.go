package search

import (
	"context"
	"fmt"

	"vibrance/internal/store"
)

// The full-text tables hold a copy of the names of the rows that are
// available, and nothing else (§10.1). The rowid of a row is the seq of the
// artist, album or track it describes (T6).
//
// A row is never changed in place: it is deleted and, if what it describes
// is available, written again from the tables it copies. So the full-text
// tables are a function of artists, albums and tracks, and a statement here
// reads no value from its caller but an id.
const (
	deleteAlbum = `DELETE FROM search_albums WHERE rowid IN (SELECT seq FROM albums WHERE id = ?)`
	insertAlbum = `INSERT INTO search_albums (rowid, title, artist)
SELECT albums.seq, albums.title, artists.name
FROM albums JOIN artists ON artists.id = albums.artist_id
WHERE albums.id = ? AND albums.available = 1`

	deleteTracks = `DELETE FROM search_tracks WHERE rowid IN (SELECT seq FROM tracks WHERE album_id = ?)`
	insertTracks = `INSERT INTO search_tracks (rowid, title, artist, album)
SELECT tracks.seq, tracks.title, tracks.artist, albums.title
FROM tracks JOIN albums ON albums.id = tracks.album_id
WHERE tracks.album_id = ? AND tracks.available = 1`

	deleteArtist = `DELETE FROM search_artists WHERE rowid IN (SELECT seq FROM artists WHERE id = ?)`
	insertArtist = `INSERT INTO search_artists (rowid, name)
SELECT artists.seq, artists.name
FROM artists
WHERE artists.id = ?
  AND EXISTS (SELECT 1 FROM albums WHERE albums.artist_id = artists.id AND albums.available = 1)`
)

// SyncAlbum makes the full-text rows of an album and of its tracks what
// albums, artists and tracks say now: the rows there were are deleted, and
// those of the album, if it is available, and of its available tracks are
// written. So it is both the insertion and the deletion of an album.
//
// db is the write transaction that changed those tables (store.Queries.Conn):
// the index and what it describes change together or not at all (§10.1).
func SyncAlbum(ctx context.Context, db store.DBTX, albumID string) error {
	return run(ctx, db, albumID, "album", deleteAlbum, insertAlbum, deleteTracks, insertTracks)
}

// SyncArtist does the same for the row of an artist. An artist has no
// availability of its own: it can be found while it has at least one
// available album, which is when the lists show it (§8.5).
func SyncArtist(ctx context.Context, db store.DBTX, artistID string) error {
	return run(ctx, db, artistID, "artist", deleteArtist, insertArtist)
}

func run(ctx context.Context, db store.DBTX, id, what string, statements ...string) error {
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement, id); err != nil {
			return fmt.Errorf("search: indexing the %s %s: %w", what, id, err)
		}
	}
	return nil
}
