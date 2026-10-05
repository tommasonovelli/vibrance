package store

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// The plans of the queries of the catalog (sql/catalog.sql), as SQLite
// makes them (DESIGN.md §8.5, T9): every list walks the index of its order,
// from the key of the cursor on, and sorts nothing; no query of the catalog
// reads a whole table.

func TestCatalogQueryPlans(t *testing.T) {
	s := newStore(t)
	null := any(nil)
	artist := any(testArtist)
	for _, q := range []struct {
		name  string
		query string
		args  []any
		// index is the index the plan must search, or scan in order.
		index string
		// sorts allows a temporary b-tree: only the details that are not
		// paginated, whose rows are those of one album or one artist.
		sorts bool
	}{
		{"ListArtistsByName", listArtistsByName, []any{1}, "artists_sort_idx", false},
		{"ListArtistsByNameAfter", listArtistsByNameAfter, []any{[]byte{1}, testArtist, 1}, "artists_sort_idx", false},

		{"ListAlbumsByTitleAsc", listAlbumsByTitleAsc, []any{null, 1}, "albums_title_idx", false},
		{"ListAlbumsByTitleAsc of an artist", listAlbumsByTitleAsc, []any{artist, 1}, "albums_title_idx", false},
		{"ListAlbumsByTitleAscAfter", listAlbumsByTitleAscAfter, []any{null, []byte{1}, testAlbum, 1}, "albums_title_idx", false},
		{"ListAlbumsByTitleDesc", listAlbumsByTitleDesc, []any{null, 1}, "albums_title_idx", false},
		{"ListAlbumsByTitleDescAfter", listAlbumsByTitleDescAfter, []any{artist, []byte{1}, testAlbum, 1}, "albums_title_idx", false},

		{"ListAlbumsByArtistAsc", listAlbumsByArtistAsc, []any{null, 1}, "albums_artist_idx", false},
		{"ListAlbumsByArtistAscAfter", listAlbumsByArtistAscAfter, []any{null, []byte{1}, 3, []byte{2}, testAlbum, 1}, "albums_artist_idx", false},
		{"ListAlbumsByArtistDesc", listAlbumsByArtistDesc, []any{null, 1}, "albums_artist_idx", false},
		{"ListAlbumsByArtistDescAfter", listAlbumsByArtistDescAfter, []any{artist, []byte{1}, 3, []byte{2}, testAlbum, 1}, "albums_artist_idx", false},

		{"ListAlbumsByYearAsc", listAlbumsByYearAsc, []any{null, 1}, "albums_year_idx", false},
		{"ListAlbumsByYearAscAfter", listAlbumsByYearAscAfter, []any{null, 3, []byte{2}, testAlbum, 1}, "albums_year_idx", false},
		{"ListAlbumsByYearDesc", listAlbumsByYearDesc, []any{null, 1}, "albums_year_idx", false},
		{"ListAlbumsByYearDescAfter", listAlbumsByYearDescAfter, []any{artist, 3, []byte{2}, testAlbum, 1}, "albums_year_idx", false},

		{"ListAlbumsByAddedAsc", listAlbumsByAddedAsc, []any{null, 1}, "albums_first_seen_idx", false},
		{"ListAlbumsByAddedAscAfter", listAlbumsByAddedAscAfter, []any{null, 3, testAlbum, 1}, "albums_first_seen_idx", false},
		{"ListAlbumsByAddedDesc", listAlbumsByAddedDesc, []any{null, 1}, "albums_first_seen_idx", false},
		{"ListAlbumsByAddedDescAfter", listAlbumsByAddedDescAfter, []any{artist, 3, testAlbum, 1}, "albums_first_seen_idx", false},

		{"ListAlbumsOfArtist", listAlbumsOfArtist, []any{testArtist}, "albums_artist_id_idx", true},
		{"ListAvailableTracksOfAlbum", listAvailableTracksOfAlbum, []any{testArtist, testAlbum}, "tracks_album_disc_no_idx", false},
	} {
		plan := explain(t, s, q.query, q.args...)
		joined := strings.Join(plan, "\n")
		if !regexp.MustCompile(`(?m)INDEX ` + q.index + `( |$)`).MatchString(joined) {
			t.Errorf("%s does not use %s:\n%s", q.name, q.index, joined)
		}
		if !q.sorts && strings.Contains(joined, "TEMP B-TREE") {
			t.Errorf("%s sorts its rows instead of reading them in the order of an index:\n%s", q.name, joined)
		}
		for _, line := range plan {
			if strings.HasPrefix(line, "SCAN ") && !strings.Contains(line, " INDEX ") {
				t.Errorf("%s reads a whole table: %s\n%s", q.name, line, joined)
			}
		}
		if strings.Contains(q.name, "After") && !regexp.MustCompile(`INDEX `+q.index+` \(.*[<>]\(?\?`).MatchString(joined) {
			t.Errorf("%s does not search its index from the key of the cursor:\n%s", q.name, joined)
		}
	}
}

// The single rows are read by their key.
func TestCatalogLookupPlans(t *testing.T) {
	s := newStore(t)
	for name, q := range map[string]struct {
		query string
		args  []any
	}{
		"GetAvailableAlbum": {getAvailableAlbum, []any{testAlbum}},
		"GetTrackWithAlbum": {getTrackWithAlbum, []any{testArtist, testTrack}},
	} {
		plan := explain(t, s, q.query, q.args...)
		for _, line := range plan {
			if strings.HasPrefix(line, "SCAN ") || strings.Contains(line, "TEMP B-TREE") {
				t.Errorf("%s: %s\n%s", name, line, strings.Join(plan, "\n"))
			}
		}
	}
}

// explain returns the details of EXPLAIN QUERY PLAN of query, with args
// bound: the plan does not depend on their values.
func explain(t *testing.T, s *Store, query string, args ...any) []string {
	t.Helper()
	rows, err := s.read.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN %s: %v", query, err)
	}
	var details []string
	for rows.Next() {
		var (
			id, parent, unused int64
			detail             string
		)
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return details
}
