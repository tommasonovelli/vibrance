package library

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"

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

// The write transactions of the scanner. Like commitAlbum, each is the
// whole function of its transaction, and reads and writes the database
// alone.

// commitAbsent records that the folder of an album is gone (§6.4): the
// album and its tracks become unavailable and leave the full-text tables,
// and so does its artist if it has no available album left. No row is
// deleted (I3): the album comes back with the same ids if its files do.
func commitAbsent(ctx context.Context, q *store.Queries, albumID, artistID string, now int64) error {
	err := q.SetTracksOfAlbumUnavailable(ctx, store.SetTracksOfAlbumUnavailableParams{UpdatedAt: now, AlbumID: albumID})
	if err != nil {
		return fmt.Errorf("marking the tracks of the album %s unavailable: %w", albumID, err)
	}
	if err := q.SetAlbumUnavailable(ctx, store.SetAlbumUnavailableParams{UpdatedAt: now, ID: albumID}); err != nil {
		return fmt.Errorf("marking the album %s unavailable: %w", albumID, err)
	}
	if err := q.UpdateAlbumCounters(ctx, albumID); err != nil {
		return fmt.Errorf("counting the tracks of the album %s: %w", albumID, err)
	}
	if err := search.SyncAlbum(ctx, q.Conn(), albumID); err != nil {
		return err
	}
	return search.SyncArtist(ctx, q.Conn(), artistID)
}

// commitReferences makes the references of the users follow the audio: the
// playlist items and the favorites of a track that is not available move
// to an available track with the same fingerprint, when there is one.
// *moved is how many tracks lost their references this way.
//
// The rule reads only the state of the database, so it does not matter in
// which order the albums were indexed, nor how many cycles ago: running it
// again changes nothing. A playlist item keeps its id, its position and
// added_at, and each playlist that has such an item gets one new revision,
// as after any change of its items. A favorite keeps its created_at; a user
// that already has the other track among its favorites keeps that row, and
// the old one goes. No row of tracks changes (I3, I15).
func commitReferences(ctx context.Context, q *store.Queries, now int64, moved *int) error {
	rows, err := q.ListMovedReferences(ctx)
	if err != nil {
		return fmt.Errorf("listing the references to unavailable tracks: %w", err)
	}
	*moved = 0
	playlists := map[string]bool{}
	for i, r := range rows {
		// The first row of a track is the one its references move to.
		if i > 0 && rows[i-1].OldID == r.OldID {
			continue
		}
		*moved++
		ids, err := q.ListPlaylistIDsByTrack(ctx, r.OldID)
		if err != nil {
			return fmt.Errorf("listing the playlists of the track %s: %w", r.OldID, err)
		}
		for _, id := range ids {
			playlists[id] = true
		}
		if err := q.MovePlaylistItems(ctx, store.MovePlaylistItemsParams{NewID: r.NewID, OldID: r.OldID}); err != nil {
			return fmt.Errorf("moving the playlist items of the track %s: %w", r.OldID, err)
		}
		if err := q.CopyFavorites(ctx, store.CopyFavoritesParams{NewID: r.NewID, OldID: r.OldID}); err != nil {
			return fmt.Errorf("moving the favorites of the track %s: %w", r.OldID, err)
		}
		if err := q.DeleteFavoritesOfTrack(ctx, r.OldID); err != nil {
			return fmt.Errorf("removing the favorites of the track %s: %w", r.OldID, err)
		}
	}
	for id := range playlists {
		if err := q.TouchPlaylist(ctx, store.TouchPlaylistParams{UpdatedAt: now, ID: id}); err != nil {
			return fmt.Errorf("giving the playlist %s a new revision: %w", id, err)
		}
	}
	return nil
}

// commitFingerprint gives a row the fingerprint that the current ffmpeg
// computed for its file (§6.6): the row keeps its id. others names the
// rows of its album that have that fingerprint already: if one of them has
// the occurrence of the row, the row takes the lowest free one, as a row
// whose fingerprint changes does in the planner (§5.4). *written is false
// when the row is no longer the one that was read: then nothing changes.
func commitFingerprint(ctx context.Context, q *store.Queries, others store.ListOccurrencesParams, set store.SetTrackFingerprintParams, written *bool) error {
	taken, err := q.ListOccurrences(ctx, others)
	if err != nil {
		return fmt.Errorf("reading the occurrences of the album %s: %w", others.AlbumID, err)
	}
	if slices.Contains(taken, set.Occurrence) {
		// taken is in ascending order: the first gap is the lowest free
		// occurrence.
		set.Occurrence = 1
		for _, n := range taken {
			if n == set.Occurrence {
				set.Occurrence++
			}
		}
	}
	n, err := q.SetTrackFingerprint(ctx, set)
	if err != nil {
		return fmt.Errorf("writing the fingerprint of the track %s: %w", set.ID, err)
	}
	*written = n == 1
	return nil
}

// commitMeta records something the server remembers of itself, such as the
// ffmpeg that computed every fingerprint of the available tracks (§6.6).
func commitMeta(ctx context.Context, q *store.Queries, key, value string) error {
	if err := q.SetMeta(ctx, store.SetMetaParams{Key: key, Value: value}); err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

// sortKeys are the sort keys of every artist and of every album title, as
// the compiled collation computes them.
type sortKeys struct {
	artists []store.SetArtistSortKeyParams
	albums  []store.SetAlbumTitleKeyParams
	// version is the collation they were computed with.
	version string
}

// commitSortKeys writes every sort key of the index and the version of the
// collation that computed them, together (§5.5): after a stop in the
// middle, the next start finds the old version and computes them again.
func commitSortKeys(ctx context.Context, q *store.Queries, keys sortKeys) error {
	for _, a := range keys.artists {
		if err := q.SetArtistSortKey(ctx, a); err != nil {
			return fmt.Errorf("writing the sort key of the artist %s: %w", a.ID, err)
		}
		// albums.artist_key is a copy of it (§5.2).
		if err := q.SetArtistKeyOfAlbums(ctx, store.SetArtistKeyOfAlbumsParams{ArtistKey: a.SortKey, ArtistID: a.ID}); err != nil {
			return fmt.Errorf("giving the albums of the artist %s its sort key: %w", a.ID, err)
		}
	}
	for _, a := range keys.albums {
		if err := q.SetAlbumTitleKey(ctx, a); err != nil {
			return fmt.Errorf("writing the sort key of the album %s: %w", a.ID, err)
		}
	}
	return commitMeta(ctx, q, metaCollateVersion, keys.version)
}
