package store

import (
	"slices"
	"strings"
	"testing"
)

// The queries of step W3 (docs/proposals/web-client-api.md B1, B4) and the
// index playlist_items(track_id, playlist_id) of migration 00004, on a
// database without statistics (a first scan, before the first PRAGMA
// optimize: NOTES.md N-185).

// The playlists of a user that hold a track are found from the items of the
// track, through the new index, and each playlist by its key: never every
// playlist, never every item. The references that follow the audio find
// the items of a track the same way.
func TestItemsOfATrackAreFoundByTheTrack(t *testing.T) {
	s := newStore(t)
	for name, c := range map[string]struct {
		query string
		args  []any
		want  []string
	}{
		"ListPlaylistRefsOfUserWithTrack": {listPlaylistRefsOfUserWithTrack, []any{testTrack, testUser}, []string{
			"SEARCH playlist_items USING COVERING INDEX playlist_items_track_idx (track_id=?)",
			"SEARCH playlists USING INDEX sqlite_autoindex_playlists_1 (id=?)",
		}},
		"ListPlaylistIDsByTrack": {listPlaylistIDsByTrack, []any{testTrack}, []string{
			"SEARCH playlist_items USING COVERING INDEX playlist_items_track_idx (track_id=?)",
		}},
		"MovePlaylistItems": {movePlaylistItems, []any{testTrack, testTrack}, []string{
			"SEARCH playlist_items USING COVERING INDEX playlist_items_track_idx (track_id=?)",
		}},
	} {
		plan := explain(t, s, c.query, c.args...)
		for _, want := range c.want {
			if !slices.Contains(plan, want) {
				t.Errorf("%s: the plan has no %q:\n%s", name, want, strings.Join(plan, "\n"))
			}
		}
		for _, line := range plan {
			if strings.HasPrefix(line, "SCAN ") {
				t.Errorf("%s reads a whole table: %s", name, line)
			}
		}
	}
}

// The new index begins with the track: the queries that read the items of
// one playlist keep reading them through playlist_items_position_idx, by
// the playlist, and find the track of each item by its id. None of them
// may walk the tracks first and look for each among the items through the
// new index, as an index that SQLite misjudges would make it do.
func TestItemsOfAPlaylistAreFoundByThePlaylist(t *testing.T) {
	s := newStore(t)
	for name, c := range map[string]struct {
		query string
		args  []any
	}{
		"GetPlaylistOfUser":       {getPlaylistOfUser, []any{testList, testUser}},
		"GetPlaylistStateOfUser":  {getPlaylistStateOfUser, []any{testList, testUser}},
		"ListPlaylistsOfUser":     {listPlaylistsOfUser, []any{testUser}},
		"ListPlaylistItems":       {listPlaylistItems, []any{testUser, testList, 3, "i1", 1}},
		"NextPlaylistCover":       {nextPlaylistCover, []any{testList, 3, "i1", "", "", ""}},
		"RenumberPlaylistItems":   {renumberPlaylistItems, []any{testList}},
		"ShiftPlaylistItems":      {shiftPlaylistItems, []any{1, testList, 0, 5}},
		"GetPlaylistItemPosition": {getPlaylistItemPosition, []any{"i1", testList}},
	} {
		plan := explain(t, s, c.query, c.args...)
		joined := strings.Join(plan, "\n")
		if strings.Contains(joined, "playlist_items_track_idx") {
			t.Errorf("%s reads the items through the index of the tracks:\n%s", name, joined)
		}
		for _, line := range plan {
			if strings.Contains(line, "SEARCH tracks") && !strings.Contains(line, "(id=?") {
				t.Errorf("%s does not find the track of an item by its id: %s", name, line)
			}
			if strings.HasPrefix(line, "SCAN ") && !strings.HasPrefix(line, "SCAN playlists") && !strings.HasPrefix(line, "SCAN (subquery") {
				t.Errorf("%s reads a whole table: %s", name, line)
			}
		}
	}
}

// The tracks chosen at random: the keys of the available tracks from the
// smallest index that begins with available, or those of the albums of one
// artist by the artist and the album; the rows of the answer by their key.
func TestRandomTracksPlans(t *testing.T) {
	s := newStore(t)
	for name, c := range map[string]struct {
		query string
		args  []any
		want  []string
	}{
		"ListRandomTracks": {listRandomTracks, []any{testUser, 50}, []string{
			"SEARCH t USING COVERING INDEX tracks_first_seen_idx (available=?)",
			"SEARCH tracks USING INTEGER PRIMARY KEY (rowid=?)",
		}},
		"ListRandomTracksOfArtist": {listRandomTracksOfArtist, []any{testUser, testArtist, 50}, []string{
			"SEARCH a USING INDEX albums_artist_id_idx (artist_id=? AND available=?)",
			"SEARCH t USING INDEX tracks_album_disc_no_idx (album_id=?)",
			"SEARCH tracks USING INTEGER PRIMARY KEY (rowid=?)",
		}},
	} {
		plan := explain(t, s, c.query, c.args...)
		for _, want := range c.want {
			if !slices.Contains(plan, want) {
				t.Errorf("%s: the plan has no %q:\n%s", name, want, strings.Join(plan, "\n"))
			}
		}
		for _, line := range plan {
			if strings.HasPrefix(line, "SCAN ") {
				t.Errorf("%s reads a whole table: %s", name, line)
			}
		}
	}
}
