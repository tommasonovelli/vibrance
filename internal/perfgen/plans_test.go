//go:build perf

package perfgen

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"vibrance/internal/media"
	"vibrance/internal/search"
	"vibrance/internal/store"
)

// explainer runs the queries of the store on a database and keeps, for each
// statement, the plan SQLite makes for it with the values it was given. It
// only reads: a statement that writes is planned and not run.
type explainer struct {
	t    *testing.T
	db   *sql.DB
	plan []string
}

func (e *explainer) explain(ctx context.Context, query string, args []any) {
	e.t.Helper()
	rows, err := e.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		e.t.Fatalf("EXPLAIN QUERY PLAN %s: %v", query, err)
	}
	for rows.Next() {
		var (
			id, parent, unused int64
			detail             string
		)
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			e.t.Fatal(err)
		}
		e.plan = append(e.plan, detail)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *explainer) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	e.explain(ctx, query, args)
	return driver.RowsAffected(0), nil
}

func (e *explainer) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("the store prepares no statement")
}

func (e *explainer) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	e.explain(ctx, query, args)
	return e.db.QueryContext(ctx, query, args...)
}

func (e *explainer) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	e.explain(ctx, query, args)
	return e.db.QueryRowContext(ctx, query, args...)
}

// planCase is one query of the store with what its plan must be.
type planCase struct {
	name string
	run  func(q *store.Queries) error
	// index is an index the plan must use; "" asks for none in particular.
	index string
	// sorts allows the plan to sort its rows, and scans to read a whole
	// table: only where the rows are few by construction, or where the
	// query is meant to read everything.
	sorts, scans bool
}

// checkQueryPlans verifies on the full dataset, with the statistics SQLite
// has there, what DESIGN.md §5.2 and §8.5 ask: every list walks the index of
// its order from the key of the cursor and sorts nothing, every mandatory
// index is the one its query uses, and no request reads a whole table. It
// prints every plan, and how long each query took.
func checkQueryPlans(t *testing.T, stateDir string) {
	ctx := t.Context()
	dsn := url.URL{Scheme: "file", OmitHost: true, Path: filepath.Join(stateDir, "vibrance.db"),
		RawQuery: url.Values{"_pragma": {"query_only(ON)", "busy_timeout(5000)"}}.Encode()}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	db.SetMaxOpenConns(1)
	e := &explainer{t: t, db: db}
	q := store.New(e)

	// Real values for the parameters: an album in the middle of a list, the
	// account with the playlists, one of its playlists, a track.
	const page = 51
	firstPage, err := q.ListAlbumsByTitleAsc(ctx, page)
	if err != nil || len(firstPage) != page {
		t.Fatalf("the first page of the albums: %d rows, %v", len(firstPage), err)
	}
	album := firstPage[page-1].Album
	admin, err := q.GetUserByUsername(ctx, Admin)
	if err != nil {
		t.Fatal(err)
	}
	playlists, err := q.ListPlaylistsOfUser(ctx, admin.ID)
	if err != nil || len(playlists) == 0 {
		t.Fatalf("the playlists: %d, %v", len(playlists), err)
	}
	tracks, err := q.ListTracksByAlbum(ctx, album.ID)
	if err != nil || len(tracks) == 0 {
		t.Fatalf("the tracks: %d, %v", len(tracks), err)
	}
	track, playlist := tracks[0], playlists[0].Playlist.ID

	cases := append(albumListCases(album, page), artistAlbumCases(album, page)...)
	cases = append(cases, []planCase{
		{name: "ListArtistsByName", index: "artists_sort_idx", run: func(q *store.Queries) error {
			_, err := q.ListArtistsByName(ctx, page)
			return err
		}},
		{name: "ListArtistsByNameAfter", index: "artists_sort_idx", run: func(q *store.Queries) error {
			_, err := q.ListArtistsByNameAfter(ctx, store.ListArtistsByNameAfterParams{SortKey: album.ArtistKey, AfterID: album.ArtistID, PageSize: page})
			return err
		}},
		{name: "ListAlbumsOfArtist", index: "albums_artist_id_idx", sorts: true, run: func(q *store.Queries) error {
			_, err := q.ListAlbumsOfArtist(ctx, album.ArtistID)
			return err
		}},
		{name: "GetAvailableAlbum", run: func(q *store.Queries) error {
			_, err := q.GetAvailableAlbum(ctx, album.ID)
			return err
		}},
		{name: "ListAvailableTracksOfAlbum", index: "tracks_album_disc_no_idx", run: func(q *store.Queries) error {
			_, err := q.ListAvailableTracksOfAlbum(ctx, store.ListAvailableTracksOfAlbumParams{UserID: admin.ID, AlbumID: album.ID})
			return err
		}},
		{name: "GetTrackWithAlbum", run: func(q *store.Queries) error {
			_, err := q.GetTrackWithAlbum(ctx, store.GetTrackWithAlbumParams{UserID: admin.ID, ID: track.ID})
			return err
		}},
		{name: "GetTrackFile", run: func(q *store.Queries) error {
			_, err := q.GetTrackFile(ctx, track.ID)
			return err
		}},
		{name: "ListFavorites", index: "favorites_user_created_idx", run: func(q *store.Queries) error {
			_, err := q.ListFavorites(ctx, store.ListFavoritesParams{UserID: admin.ID, PageSize: page})
			return err
		}},
		{name: "ListFavoritesAfter", index: "favorites_user_created_idx", run: func(q *store.Queries) error {
			_, err := q.ListFavoritesAfter(ctx, store.ListFavoritesAfterParams{UserID: admin.ID, CreatedAt: epoch + 1_000_000_000, AfterID: track.ID, PageSize: page})
			return err
		}},
		{name: "GetPlaylistOfUser", index: "playlist_items_position_idx", run: func(q *store.Queries) error {
			_, err := q.GetPlaylistOfUser(ctx, store.GetPlaylistOfUserParams{ID: playlist, UserID: admin.ID})
			return err
		}},
		{name: "GetPlaylistStateOfUser", index: "playlist_items_position_idx", run: func(q *store.Queries) error {
			_, err := q.GetPlaylistStateOfUser(ctx, store.GetPlaylistStateOfUserParams{ID: playlist, UserID: admin.ID})
			return err
		}},
		{name: "RenumberPlaylistItems", index: "playlist_items_position_idx", scans: true, sorts: true, run: func(q *store.Queries) error {
			return q.RenumberPlaylistItems(ctx, playlist)
		}},
		// The playlists of every user are a small table, 500 for each user at
		// most, and those of one user are sorted.
		{name: "ListPlaylistsOfUser", index: "playlist_items_position_idx", sorts: true, scans: true, run: func(q *store.Queries) error {
			_, err := q.ListPlaylistsOfUser(ctx, admin.ID)
			return err
		}},
		{name: "ListPlaylistItems", index: "playlist_items_position_idx", run: func(q *store.Queries) error {
			_, err := q.ListPlaylistItems(ctx, store.ListPlaylistItemsParams{UserID: admin.ID, PlaylistID: playlist, AfterPosition: 100, AfterID: track.ID, PageSize: page})
			return err
		}},
		// The dataset has only the sessions of this run: SQLite reads a table of
		// a few rows whole, whatever its indexes.
		{name: "GetSessionByTokenHash", scans: true, run: func(q *store.Queries) error {
			if _, err := q.GetSessionByTokenHash(ctx, sum("no such token", 0)); !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("a session that does not exist: %w", err)
			}
			return nil
		}},
		{name: "ListSessionsOfUser", sorts: true, scans: true, run: func(q *store.Queries) error {
			_, err := q.ListSessionsOfUser(ctx, store.ListSessionsOfUserParams{UserID: admin.ID, Now: epoch})
			return err
		}},
		{name: "DeleteExpiredSessions", index: "sessions_expires_at_idx", run: func(q *store.Queries) error {
			_, err := q.DeleteExpiredSessions(ctx, epoch)
			return err
		}},

		// What the scanner reads: for one album, and at every cycle.
		{name: "ListTracksByAlbum", index: "tracks_album_", sorts: true, run: func(q *store.Queries) error {
			_, err := q.ListTracksByAlbum(ctx, album.ID)
			return err
		}},
		{name: "ListOccurrences", index: "tracks_album_fingerprint_occurrence_idx", run: func(q *store.Queries) error {
			_, err := q.ListOccurrences(ctx, store.ListOccurrencesParams{AlbumID: album.ID, Fingerprint: track.Fingerprint, ID: track.ID})
			return err
		}},
		{name: "ListAlbumStates (every album, once a cycle)", scans: true, run: func(q *store.Queries) error {
			_, err := q.ListAlbumStates(ctx)
			return err
		}},
		{name: "CountAlbums (every album, twice a cycle)", scans: true, sorts: true, run: func(q *store.Queries) error {
			_, err := q.CountAlbums(ctx)
			return err
		}},
		{name: "CountTracks (every track, twice a cycle)", scans: true, sorts: true, run: func(q *store.Queries) error {
			_, err := q.CountTracks(ctx)
			return err
		}},
		// P6 of every cycle: the references of the users that follow the
		// audio. With every track available it finds nothing.
		{name: "ListMovedReferences (P6, once a cycle)", scans: true, sorts: true, run: func(q *store.Queries) error {
			rows, err := q.ListMovedReferences(ctx)
			if len(rows) != 0 {
				return fmt.Errorf("%d references to move", len(rows))
			}
			return err
		}},
		{name: "ListStaleFingerprints (after every cycle)", scans: true, run: func(q *store.Queries) error {
			rows, err := q.ListStaleFingerprints(ctx, store.ListStaleFingerprintsParams{FpVersion: media.PinnedVersion, PageSize: 100})
			if len(rows) != 0 {
				return fmt.Errorf("%d fingerprints of another ffmpeg", len(rows))
			}
			return err
		}},
		// Once for each album with a new cover, from the queue of the
		// thumbnails made ahead (DESIGN.md §9.2): no index has the hash of
		// the cover, so it reads every available album, and sorts the few
		// that have it (NOTES.md N-164).
		{name: "GetAlbumCoverByHash (each new cover)", scans: true, sorts: true, run: func(q *store.Queries) error {
			_, err := q.GetAlbumCoverByHash(ctx, sql.NullString{String: sum("no such cover", 0), Valid: true})
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("a cover no album has: %w", err)
			}
			return nil
		}},
		{name: "search.Find, 3 types, limit 50", scans: true, sorts: true, run: func(q *store.Queries) error {
			_, err := search.Find(ctx, q, admin.ID, search.Parse("love"), search.Kinds{Artists: true, Albums: true, Tracks: true}, 50)
			return err
		}},
	}...)

	wholeTable := regexp.MustCompile(`^SCAN \S+$`)
	for _, c := range cases {
		e.plan = nil
		began := time.Now()
		if err := c.run(q); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		took := time.Since(began)
		joined := strings.Join(e.plan, "\n       ")
		fmt.Printf("PLAN %s (%s, plan included)\n       %s\n", c.name, ms(took), joined)
		if c.index != "" && !strings.Contains(joined, "INDEX "+c.index) {
			t.Errorf("%s does not use %s", c.name, c.index)
		}
		if !c.sorts && strings.Contains(joined, "TEMP B-TREE") {
			t.Errorf("%s sorts its rows instead of reading them in the order of an index", c.name)
		}
		for _, line := range e.plan {
			if !c.scans && wholeTable.MatchString(line) {
				t.Errorf("%s reads a whole table: %s", c.name, line)
			}
		}
		if strings.Contains(c.name, "After") && !regexp.MustCompile(`INDEX `+c.index+` \(.*[<>]\(?\?`).MatchString(joined) {
			t.Errorf("%s does not search its index from the key of the cursor", c.name)
		}
	}
}

// albumListCases are the sixteen queries of the list of the albums, with
// the key of album as their cursor.
func albumListCases(album store.Album, page int64) []planCase {
	ctx := context.Background()
	none := func(_ any, err error) error { return err }
	return []planCase{
		{name: "ListAlbumsByTitleAsc", index: "albums_title_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByTitleAsc(ctx, page))
		}},
		{name: "ListAlbumsByTitleAscAfter", index: "albums_title_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByTitleAscAfter(ctx, store.ListAlbumsByTitleAscAfterParams{
				TitleKey: album.TitleKey, AfterID: album.ID, PageSize: page}))
		}},
		{name: "ListAlbumsByTitleDesc", index: "albums_title_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByTitleDesc(ctx, page))
		}},
		{name: "ListAlbumsByTitleDescAfter", index: "albums_title_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByTitleDescAfter(ctx, store.ListAlbumsByTitleDescAfterParams{
				TitleKey: album.TitleKey, AfterID: album.ID, PageSize: page}))
		}},
		{name: "ListAlbumsByArtistAsc", index: "albums_artist_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByArtistAsc(ctx, page))
		}},
		{name: "ListAlbumsByArtistAscAfter", index: "albums_artist_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByArtistAscAfter(ctx, store.ListAlbumsByArtistAscAfterParams{
				ArtistKey: album.ArtistKey, YearKey: album.YearKey, TitleKey: album.TitleKey, AfterID: album.ID, PageSize: page}))
		}},
		{name: "ListAlbumsByArtistDesc", index: "albums_artist_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByArtistDesc(ctx, page))
		}},
		{name: "ListAlbumsByArtistDescAfter", index: "albums_artist_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByArtistDescAfter(ctx, store.ListAlbumsByArtistDescAfterParams{
				ArtistKey: album.ArtistKey, YearKey: album.YearKey, TitleKey: album.TitleKey, AfterID: album.ID, PageSize: page}))
		}},
		{name: "ListAlbumsByYearAsc", index: "albums_year_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByYearAsc(ctx, page))
		}},
		{name: "ListAlbumsByYearAscAfter", index: "albums_year_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByYearAscAfter(ctx, store.ListAlbumsByYearAscAfterParams{
				YearKey: album.YearKey, TitleKey: album.TitleKey, AfterID: album.ID, PageSize: page}))
		}},
		{name: "ListAlbumsByYearDesc", index: "albums_year_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByYearDesc(ctx, page))
		}},
		{name: "ListAlbumsByYearDescAfter", index: "albums_year_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByYearDescAfter(ctx, store.ListAlbumsByYearDescAfterParams{
				YearKey: album.YearKey, TitleKey: album.TitleKey, AfterID: album.ID, PageSize: page}))
		}},
		{name: "ListAlbumsByAddedAsc", index: "albums_first_seen_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByAddedAsc(ctx, page))
		}},
		{name: "ListAlbumsByAddedAscAfter", index: "albums_first_seen_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByAddedAscAfter(ctx, store.ListAlbumsByAddedAscAfterParams{
				FirstSeenAt: album.FirstSeenAt, AfterID: album.ID, PageSize: page}))
		}},
		{name: "ListAlbumsByAddedDesc", index: "albums_first_seen_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByAddedDesc(ctx, page))
		}},
		{name: "ListAlbumsByAddedDescAfter", index: "albums_first_seen_idx", run: func(q *store.Queries) error {
			return none(q.ListAlbumsByAddedDescAfter(ctx, store.ListAlbumsByAddedDescAfterParams{
				FirstSeenAt: album.FirstSeenAt, AfterID: album.ID, PageSize: page}))
		}},
	}
}

// artistAlbumCases are the eight queries of the list of the albums of one
// artist, each for its first page and for the page after the key of album:
// they read the albums of the artist, however many the others are, and sort
// those.
func artistAlbumCases(album store.Album, page int64) []planCase {
	ctx := context.Background()
	none := func(_ any, err error) error { return err }
	var cases []planCase
	for _, first := range []int64{1, 0} {
		name := " (first page)"
		if first == 0 {
			name = " (after a key)"
		}
		for _, c := range []planCase{
			{name: "ListArtistAlbumsByTitleAsc", run: func(q *store.Queries) error {
				return none(q.ListArtistAlbumsByTitleAsc(ctx, store.ListArtistAlbumsByTitleAscParams{ArtistID: album.ArtistID, FirstPage: first,
					TitleKey: album.TitleKey, AfterID: album.ID, PageSize: page}))
			}},
			{name: "ListArtistAlbumsByTitleDesc", run: func(q *store.Queries) error {
				return none(q.ListArtistAlbumsByTitleDesc(ctx, store.ListArtistAlbumsByTitleDescParams{ArtistID: album.ArtistID, FirstPage: first,
					TitleKey: album.TitleKey, AfterID: album.ID, PageSize: page}))
			}},
			{name: "ListArtistAlbumsByArtistAsc", run: func(q *store.Queries) error {
				return none(q.ListArtistAlbumsByArtistAsc(ctx, store.ListArtistAlbumsByArtistAscParams{ArtistID: album.ArtistID, FirstPage: first,
					ArtistKey: album.ArtistKey, YearKey: album.YearKey, TitleKey: album.TitleKey, AfterID: album.ID, PageSize: page}))
			}},
			{name: "ListArtistAlbumsByArtistDesc", run: func(q *store.Queries) error {
				return none(q.ListArtistAlbumsByArtistDesc(ctx, store.ListArtistAlbumsByArtistDescParams{ArtistID: album.ArtistID, FirstPage: first,
					ArtistKey: album.ArtistKey, YearKey: album.YearKey, TitleKey: album.TitleKey, AfterID: album.ID, PageSize: page}))
			}},
			{name: "ListArtistAlbumsByYearAsc", run: func(q *store.Queries) error {
				return none(q.ListArtistAlbumsByYearAsc(ctx, store.ListArtistAlbumsByYearAscParams{ArtistID: album.ArtistID, FirstPage: first,
					YearKey: album.YearKey, TitleKey: album.TitleKey, AfterID: album.ID, PageSize: page}))
			}},
			{name: "ListArtistAlbumsByYearDesc", run: func(q *store.Queries) error {
				return none(q.ListArtistAlbumsByYearDesc(ctx, store.ListArtistAlbumsByYearDescParams{ArtistID: album.ArtistID, FirstPage: first,
					YearKey: album.YearKey, TitleKey: album.TitleKey, AfterID: album.ID, PageSize: page}))
			}},
			{name: "ListArtistAlbumsByAddedAsc", run: func(q *store.Queries) error {
				return none(q.ListArtistAlbumsByAddedAsc(ctx, store.ListArtistAlbumsByAddedAscParams{ArtistID: album.ArtistID, FirstPage: first,
					FirstSeenAt: album.FirstSeenAt, AfterID: album.ID, PageSize: page}))
			}},
			{name: "ListArtistAlbumsByAddedDesc", run: func(q *store.Queries) error {
				return none(q.ListArtistAlbumsByAddedDesc(ctx, store.ListArtistAlbumsByAddedDescParams{ArtistID: album.ArtistID, FirstPage: first,
					FirstSeenAt: album.FirstSeenAt, AfterID: album.ID, PageSize: page}))
			}},
		} {
			c.name, c.index, c.sorts = c.name+name, "albums_artist_id_idx", true
			cases = append(cases, c)
		}
	}
	return cases
}
