package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"vibrance/internal/names"
	"vibrance/internal/store"
)

// The keys of meta that the library keeps (DESIGN.md §5.2).
const (
	// metaCollateVersion is the collation the sort keys were computed with.
	metaCollateVersion = "collate_version"
	// metaFFmpegVersion is the ffmpeg that computed the fingerprint of
	// every available track, once the job of §6.6 has gone through them.
	metaFFmpegVersion = "ffmpeg_version"
)

// EnsureSortKeys computes every sort key of the index again when they were
// made with another collation than the compiled one (§5.5, T26): the keys
// of two versions of golang.org/x/text do not order together, and a list
// ordered by them would be wrong. The startup calls it before the server
// serves, and before the scanner starts (§11.2 step 6). When the keys are
// current nothing is written. recomputed is whether a key was written: a
// new database has no version and no keys, and only the version is
// recorded.
func EnsureSortKeys(ctx context.Context, st *store.Store) (recomputed bool, err error) {
	keys := sortKeys{version: names.CollateVersion}
	current := false
	err = st.Read(ctx, func(q *store.Queries) error {
		version, err := q.GetMeta(ctx, metaCollateVersion)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("reading %s: %w", metaCollateVersion, err)
		case version == names.CollateVersion:
			current = true
			return nil
		}
		artists, err := q.ListArtists(ctx)
		if err != nil {
			return fmt.Errorf("listing the artists: %w", err)
		}
		for _, a := range artists {
			keys.artists = append(keys.artists, store.SetArtistSortKeyParams{SortKey: names.SortKey(a.Name), ID: a.ID})
		}
		albums, err := q.ListAlbumTitles(ctx)
		if err != nil {
			return fmt.Errorf("listing the albums: %w", err)
		}
		for _, a := range albums {
			keys.albums = append(keys.albums, store.SetAlbumTitleKeyParams{TitleKey: names.SortKey(a.Title), ID: a.ID})
		}
		tracks, err := q.ListTrackNames(ctx)
		if err != nil {
			return fmt.Errorf("listing the tracks: %w", err)
		}
		for _, t := range tracks {
			keys.tracks = append(keys.tracks, store.SetTrackSortKeysParams{TitleKey: names.SortKey(t.Title),
				ArtistKey: names.SortKey(t.Artist), ID: t.ID})
		}
		return nil
	})
	if err != nil || current {
		return false, wrapSortKeys(err)
	}
	err = st.WithWriteTx(ctx, func(q *store.Queries) error { return commitSortKeys(ctx, q, keys) })
	return err == nil && len(keys.artists)+len(keys.albums)+len(keys.tracks) > 0, wrapSortKeys(err)
}

func wrapSortKeys(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("library: computing the sort keys again: %w", err)
}
