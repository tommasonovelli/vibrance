package store

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The queries of the playlists (sql/playlists.sql), on a real database.

// The list of the items walks the index of its order from the key of the
// cursor on, sorts nothing and reads no whole table (DESIGN.md §8.5, T9);
// the rows a change needs are found by their key.
func TestPlaylistQueryPlans(t *testing.T) {
	s := newStore(t)
	const index = "playlist_items_position_idx"
	plan := explain(t, s, listPlaylistItems, testUser, testList, 3, "i1", 1)
	joined := strings.Join(plan, "\n")
	if !regexp.MustCompile(`(?m)^SEARCH playlist_items USING (COVERING )?INDEX ` + index + ` \(playlist_id=\? AND .*>\(?\?`).MatchString(joined) {
		t.Errorf("ListPlaylistItems does not search %s from the key of the cursor:\n%s", index, joined)
	}
	if strings.Contains(joined, "TEMP B-TREE") {
		t.Errorf("ListPlaylistItems sorts its rows instead of reading them in the order of an index:\n%s", joined)
	}
	for name, q := range map[string]struct {
		query string
		args  []any
	}{
		"ListPlaylistItems":       {listPlaylistItems, []any{testUser, testList, 3, "i1", 1}},
		"GetPlaylistOfUser":       {getPlaylistOfUser, []any{testList, testUser}},
		"GetPlaylistStateOfUser":  {getPlaylistStateOfUser, []any{testList, testUser}},
		"GetPlaylistItemPosition": {getPlaylistItemPosition, []any{"i1", testList}},
		"GetTrackAvailability":    {getTrackAvailability, []any{testTrack}},
		"ShiftPlaylistItems":      {shiftPlaylistItems, []any{1, testList, 0, 5}},
		"SetPlaylistItemPosition": {setPlaylistItemPosition, []any{1, "i1"}},
		"DeletePlaylistItem":      {deletePlaylistItem, []any{"i1"}},
		"RenamePlaylist":          {renamePlaylist, []any{"n", "d", 1, testList}},
		"DeletePlaylist":          {deletePlaylist, []any{testList}},
	} {
		plan := explain(t, s, q.query, q.args...)
		for _, line := range plan {
			if strings.HasPrefix(line, "SCAN ") && !strings.Contains(line, "CONSTANT ROW") {
				t.Errorf("%s reads a whole table: %s\n%s", name, line, strings.Join(plan, "\n"))
			}
		}
	}
	// The renumbering reads the items of its playlist alone, through the
	// index.
	joined = strings.Join(explain(t, s, renumberPlaylistItems, testList), "\n")
	if !strings.Contains(joined, "INDEX "+index+" (playlist_id=?)") || strings.Contains(joined, "SCAN items") {
		t.Errorf("RenumberPlaylistItems does not read the items of one playlist through %s:\n%s", index, joined)
	}
}

// RenumberPlaylistItems makes the positions of one playlist 0..n-1 in the
// order (position, id), whatever they were: gaps, ties, any order of
// writing. It touches nothing else of the items, no other playlist, and
// inserts nothing (DESIGN.md §8.6, T5).
func TestRenumberPlaylistItems(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedTwoAlbums(t, s)
	userRows(t, s)
	mustExec(t, s.write, `DELETE FROM playlist_items`)
	mustExec(t, s.write, `INSERT INTO playlist_items (id, playlist_id, track_id, position, added_at) VALUES
		('e', ?1, ?3, 9, 105), ('b', ?1, ?4, 5, 102), ('a', ?1, ?3, 5, 101), ('d', ?1, ?3, 0, 104), ('c', ?1, ?4, 7, 103),
		('x', ?2, ?3, 4, 201), ('y', ?2, ?4, 4, 202)`, testList, testList2, testTrack, testTrack2)
	type row struct {
		id, playlist, track string
		position, addedAt   int64
	}
	items := func() []row {
		t.Helper()
		rows, err := s.read.QueryContext(ctx, `SELECT id, playlist_id, track_id, position, added_at FROM playlist_items ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.playlist, &r.track, &r.position, &r.addedAt); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		return out
	}
	want := []row{
		{"a", testList, testTrack, 1, 101}, {"b", testList, testTrack2, 2, 102}, {"c", testList, testTrack2, 3, 103},
		{"d", testList, testTrack, 0, 104}, {"e", testList, testTrack, 4, 105},
		{"x", testList2, testTrack, 4, 201}, {"y", testList2, testTrack2, 4, 202},
	}
	// Twice: the second time there is nothing to do.
	for range 2 {
		write(t, s, func(q *Queries) error { return q.RenumberPlaylistItems(ctx, testList) })
		if got := items(); !slices.Equal(got, want) {
			t.Fatalf("after renumbering:\n got %v\nwant %v", got, want)
		}
	}
	// A playlist without items, and one that does not exist.
	mustExec(t, s.write, `DELETE FROM playlist_items WHERE playlist_id = ?`, testList)
	write(t, s, func(q *Queries) error {
		return errors.Join(q.RenumberPlaylistItems(ctx, testList), q.RenumberPlaylistItems(ctx, "no playlist"))
	})
	if got := items(); !slices.Equal(got, want[5:]) {
		t.Fatalf("after renumbering nothing: %v", got)
	}
	write(t, s, func(q *Queries) error { return q.RenumberPlaylistItems(ctx, testList2) })
	want[5].position, want[6].position = 0, 1
	if got := items(); !slices.Equal(got, want[5:]) {
		t.Fatalf("a tie is broken by the id: %v", got)
	}
	// Positions that are not dense in one way only: two items at one
	// position with the highest in its place, and a gap with no two items at
	// one position.
	for _, c := range []struct {
		what      string
		positions [3]int
	}{{"a tie alone", [3]int{0, 0, 2}}, {"a gap alone", [3]int{0, 1, 3}}, {"no first position", [3]int{1, 2, 3}}} {
		mustExec(t, s.write, `DELETE FROM playlist_items WHERE playlist_id = ?`, testList)
		mustExec(t, s.write, `INSERT INTO playlist_items (id, playlist_id, track_id, position, added_at) VALUES
			('a', ?1, ?2, ?3, 1), ('b', ?1, ?2, ?4, 1), ('c', ?1, ?2, ?5, 1)`, testList, testTrack, c.positions[0], c.positions[1], c.positions[2])
		write(t, s, func(q *Queries) error { return q.RenumberPlaylistItems(ctx, testList) })
		for i, r := range items()[:3] {
			if r.id != string(rune('a'+i)) || r.position != int64(i) {
				t.Fatalf("%s: %v", c.what, items())
			}
		}
	}
	mustExec(t, s.write, `DELETE FROM playlist_items WHERE playlist_id = ?`, testList)
	// Items that all move forward: each is given its place once, although
	// its new position is further on in the index the items are read from.
	mustExec(t, s.write, `INSERT INTO playlist_items (id, playlist_id, track_id, position, added_at) VALUES
		('p', ?1, ?2, 0, 1), ('q', ?1, ?2, 0, 1), ('r', ?1, ?2, 0, 1), ('s', ?1, ?2, 0, 1), ('t', ?1, ?2, 2, 1)`, testList, testTrack)
	write(t, s, func(q *Queries) error { return q.RenumberPlaylistItems(ctx, testList) })
	for i, r := range items()[:5] {
		if r.id != string(rune('p'+i)) || r.position != int64(i) {
			t.Fatalf("items that move forward: %v", items())
		}
	}
}
