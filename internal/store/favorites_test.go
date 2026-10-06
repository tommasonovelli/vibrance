package store

import (
	"regexp"
	"strings"
	"testing"
)

// The plans of the queries of the favorites (sql/favorites.sql), as SQLite
// makes them (DESIGN.md §8.5, T9): the list walks the index of its order
// backwards, from the key of the cursor on, sorts nothing and reads no
// whole table; the track of a change is found by its key.
func TestFavoritesQueryPlans(t *testing.T) {
	s := newStore(t)
	const index = "favorites_user_created_idx"
	for _, q := range []struct {
		name  string
		query string
		args  []any
	}{
		{"ListFavorites", listFavorites, []any{testArtist, 1}},
		{"ListFavoritesAfter", listFavoritesAfter, []any{testArtist, 3, testTrack, 1}},
	} {
		plan := explain(t, s, q.query, q.args...)
		joined := strings.Join(plan, "\n")
		if !regexp.MustCompile(`(?m)^SEARCH favorites USING (COVERING )?INDEX ` + index + ` \(user_id=\?`).MatchString(joined) {
			t.Errorf("%s does not walk %s for the user:\n%s", q.name, index, joined)
		}
		if strings.Contains(joined, "TEMP B-TREE") {
			t.Errorf("%s sorts its rows instead of reading them in the order of an index:\n%s", q.name, joined)
		}
		for _, line := range plan {
			if strings.HasPrefix(line, "SCAN ") {
				t.Errorf("%s reads a whole table: %s\n%s", q.name, line, joined)
			}
		}
		if strings.Contains(q.name, "After") && !regexp.MustCompile(`INDEX `+index+` \(user_id=\? AND .*<\(?\?`).MatchString(joined) {
			t.Errorf("%s does not search its index from the key of the cursor:\n%s", q.name, joined)
		}
	}
	for name, q := range map[string]struct {
		query string
		args  []any
	}{
		"TrackExists":    {trackExists, []any{testTrack}},
		"AddFavorite":    {addFavorite, []any{testTrack, 1, testArtist}},
		"RemoveFavorite": {removeFavorite, []any{testArtist, testTrack}},
		// The summary reads the favorites of the user, and the track of each
		// by its id: never every available track.
		"GetFavoritesSummary": {getFavoritesSummary, []any{testArtist}},
	} {
		plan := explain(t, s, q.query, q.args...)
		for _, line := range plan {
			if strings.HasPrefix(line, "SCAN ") && !strings.Contains(line, "CONSTANT ROW") || strings.Contains(line, "TEMP B-TREE") {
				t.Errorf("%s: %s\n%s", name, line, strings.Join(plan, "\n"))
			}
		}
	}
}
