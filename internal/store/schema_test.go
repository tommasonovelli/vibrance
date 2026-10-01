package store

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// column is one column as PRAGMA table_info reports it.
type column struct {
	name    string
	typ     string
	notNull bool
	dflt    string // the default as SQL text; "" is no default
	pk      int    // position in the primary key, from 1; 0 is not part of it
}

func req(name, typ string) column { return column{name: name, typ: typ, notNull: true} }
func opt(name, typ string) column { return column{name: name, typ: typ} }

// wantSchema is DESIGN.md §5.2, column by column and in its order: a
// column is NOT NULL unless the design marks it with `?`. seq is the
// rowid: SQLite gives it a value when none is given.
var wantSchema = []struct {
	table   string
	columns []column
}{
	{"meta", []column{
		{name: "key", typ: "text", notNull: true, pk: 1},
		req("value", "text"),
	}},
	{"users", []column{
		{name: "id", typ: "text", notNull: true, pk: 1},
		req("username", "text"),
		req("password_hash", "text"),
		req("role", "text"),
		{name: "disabled", typ: "integer", notNull: true, dflt: "0"},
		req("created_at", "integer"),
		req("password_changed_at", "integer"),
	}},
	{"sessions", []column{
		{name: "id", typ: "text", notNull: true, pk: 1},
		req("token_hash", "text"),
		req("user_id", "text"),
		req("kind", "text"),
		opt("device_name", "text"),
		req("created_at", "integer"),
		req("last_used_at", "integer"),
		req("expires_at", "integer"),
	}},
	{"artists", []column{
		{name: "seq", typ: "integer", pk: 1},
		req("id", "text"),
		req("name", "text"),
		req("sort_key", "blob"),
	}},
	{"albums", []column{
		{name: "seq", typ: "integer", pk: 1},
		req("id", "text"),
		req("artist_id", "text"),
		req("artist_key", "blob"),
		req("title", "text"),
		req("title_key", "blob"),
		opt("year", "integer"),
		req("year_key", "integer"),
		opt("genre", "text"),
		req("compilation", "integer"),
		req("rel_path", "text"),
		req("album_revision", "integer"),
		req("render_version", "text"),
		req("receipt_hash", "text"),
		opt("cover_rel", "text"),
		opt("cover_sha256", "text"),
		opt("cover_mime", "text"),
		opt("cover_size", "integer"),
		opt("cover_mtime_ns", "integer"),
		req("track_count", "integer"),
		req("duration_ms", "integer"),
		req("available", "integer"),
		req("first_seen_at", "integer"),
		req("updated_at", "integer"),
	}},
	{"tracks", []column{
		{name: "seq", typ: "integer", pk: 1},
		req("id", "text"),
		req("album_id", "text"),
		req("fingerprint", "text"),
		req("fp_version", "text"),
		{name: "occurrence", typ: "integer", notNull: true, dflt: "1"},
		req("disc", "integer"),
		req("no", "integer"),
		req("title", "text"),
		req("artist", "text"),
		opt("genre", "text"),
		req("rel_path", "text"),
		req("file_size", "integer"),
		req("file_mtime_ns", "integer"),
		req("file_sha256", "text"),
		req("codec", "text"),
		req("sample_rate", "integer"),
		req("channels", "integer"),
		opt("bit_depth", "integer"),
		opt("bitrate", "integer"),
		opt("duration_ms", "integer"),
		opt("lyrics_rel", "text"),
		opt("lyrics_sha256", "text"),
		opt("rg_track_gain", "real"),
		opt("rg_track_peak", "real"),
		opt("rg_album_gain", "real"),
		opt("rg_album_peak", "real"),
		req("available", "integer"),
		req("updated_at", "integer"),
	}},
	{"favorites", []column{
		{name: "user_id", typ: "text", notNull: true, pk: 1},
		{name: "track_id", typ: "text", notNull: true, pk: 2},
		req("created_at", "integer"),
	}},
	{"playlists", []column{
		{name: "id", typ: "text", notNull: true, pk: 1},
		req("user_id", "text"),
		req("name", "text"),
		{name: "description", typ: "text", notNull: true, dflt: "''"},
		req("revision", "integer"),
		req("created_at", "integer"),
		req("updated_at", "integer"),
	}},
	{"playlist_items", []column{
		{name: "id", typ: "text", notNull: true, pk: 1},
		req("playlist_id", "text"),
		req("track_id", "text"),
		req("position", "integer"),
		req("added_at", "integer"),
	}},
}

// The tables and their columns are those of §5.2: names, types, order,
// NOT NULL, defaults and primary keys.
func TestSchemaColumns(t *testing.T) {
	s := newStore(t)

	var wantTables []string
	for _, tbl := range wantSchema {
		wantTables = append(wantTables, tbl.table)
	}
	slices.Sort(wantTables)
	gotTables := strings1(t, s.read, `SELECT name FROM sqlite_master WHERE type = 'table'
		AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'search_%' AND name <> 'goose_db_version' ORDER BY name`)
	if !slices.Equal(gotTables, wantTables) {
		t.Fatalf("tables %v, want %v", gotTables, wantTables)
	}

	for _, tbl := range wantSchema {
		rows, err := s.read.QueryContext(t.Context(),
			`SELECT name, lower(type), "notnull", coalesce(dflt_value, ''), pk FROM pragma_table_info(?) ORDER BY cid`, tbl.table)
		if err != nil {
			t.Fatal(err)
		}
		var got []column
		for rows.Next() {
			var c column
			if err := rows.Scan(&c.name, &c.typ, &c.notNull, &c.dflt, &c.pk); err != nil {
				t.Fatal(err)
			}
			got = append(got, c)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, tbl.columns) {
			t.Errorf("table %s:\n got %+v\nwant %+v", tbl.table, got, tbl.columns)
		}
	}
}

// The indexes are those §5.2 makes mandatory, and no others.
func TestSchemaIndexes(t *testing.T) {
	s := newStore(t)
	want := []string{
		"albums_artist_id_idx ON albums (artist_id, available)",
		"albums_artist_idx ON albums (available, artist_key, year_key, title_key, id)",
		"albums_first_seen_idx ON albums (available, first_seen_at, id)",
		"albums_title_idx ON albums (available, title_key, id)",
		"albums_year_idx ON albums (available, year_key, title_key, id)",
		"favorites_user_created_idx ON favorites (user_id, created_at, track_id)",
		"playlist_items_position_idx ON playlist_items (playlist_id, position, id)",
		"sessions_expires_at_idx ON sessions (expires_at)",
		"sessions_user_id_idx ON sessions (user_id)",
		"tracks_album_disc_no_idx ON tracks (album_id, disc, no)",
		"UNIQUE tracks_album_fingerprint_occurrence_idx ON tracks (album_id, fingerprint, occurrence)",
	}
	// Only the indexes the migrations create: those SQLite makes by itself
	// for PRIMARY KEY and UNIQUE have no SQL text.
	got := strings1(t, s.read, `
		SELECT CASE WHEN l."unique" THEN 'UNIQUE ' ELSE '' END || m.name || ' ON ' || m.tbl_name || ' (' ||
		       (SELECT group_concat(name, ', ' ORDER BY seqno) FROM pragma_index_info(m.name)) || ')'
		FROM sqlite_master m JOIN pragma_index_list(m.tbl_name) l ON l.name = m.name
		WHERE m.type = 'index' AND m.sql IS NOT NULL
		ORDER BY m.name`)
	if !slices.Equal(got, want) {
		t.Fatalf("indexes:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
}

// The foreign keys are those of §5.2: what belongs to a user is deleted
// with it, and nothing may take an artist, an album or a track away.
func TestSchemaForeignKeys(t *testing.T) {
	s := newStore(t)
	want := []string{
		"albums.artist_id -> artists.id RESTRICT",
		"favorites.track_id -> tracks.id RESTRICT",
		"favorites.user_id -> users.id CASCADE",
		"playlist_items.playlist_id -> playlists.id CASCADE",
		"playlist_items.track_id -> tracks.id RESTRICT",
		"playlists.user_id -> users.id CASCADE",
		"sessions.user_id -> users.id CASCADE",
		"tracks.album_id -> albums.id RESTRICT",
	}
	got := strings1(t, s.read, `
		SELECT m.name || '.' || f."from" || ' -> ' || f."table" || '.' || f."to" || ' ' || f.on_delete
		FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) f
		WHERE m.type = 'table' ORDER BY 1`)
	if !slices.Equal(got, want) {
		t.Fatalf("foreign keys:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
	if got := strings1(t, s.read, `PRAGMA foreign_key_check`); len(got) != 0 {
		t.Fatalf("foreign_key_check: %v", got)
	}
}

// row is the values of one row, by column.
type row map[string]any

// fixture makes valid rows, each different from every other.
type fixture struct {
	t  *testing.T
	db *sql.DB
	n  int
}

func (f *fixture) next() int {
	f.n++
	return f.n
}

func uuid(n int) string { return fmt.Sprintf("00000000-0000-7000-8000-%012d", n) }

// valid returns a valid row for the table, with the optional columns left
// out. The rows it refers to are inserted.
func (f *fixture) valid(table string) row {
	f.t.Helper()
	n := f.next()
	hash := fmt.Sprintf("%064x", n)
	switch table {
	case "meta":
		return row{"key": fmt.Sprintf("key%d", n), "value": "v"}
	case "users":
		return row{"id": uuid(n), "username": fmt.Sprintf("user%d", n), "password_hash": "$argon2id$v=19$m=65536,t=3,p=1$c2FsdA$aGFzaA",
			"role": "user", "created_at": 1, "password_changed_at": 1}
	case "sessions":
		return row{"id": uuid(n), "token_hash": hash, "user_id": f.insert("users"), "kind": "cookie",
			"created_at": 1, "last_used_at": 2, "expires_at": 3}
	case "artists":
		return row{"id": uuid(n), "name": fmt.Sprintf("Artist %d", n), "sort_key": []byte{1, byte(n)}}
	case "albums":
		return row{"id": uuid(n), "artist_id": f.insert("artists"), "artist_key": []byte{1}, "title": fmt.Sprintf("Album %d", n),
			"title_key": []byte{2}, "year_key": 10000, "compilation": 0, "rel_path": fmt.Sprintf("Artist/Album %d", n),
			"album_revision": 1, "render_version": "1", "receipt_hash": hash, "track_count": 0, "duration_ms": 0,
			"available": 1, "first_seen_at": 1, "updated_at": 1}
	case "tracks":
		return row{"id": uuid(n), "album_id": f.insert("albums"), "fingerprint": hash, "fp_version": "8.1.3-musiclib1",
			"disc": 1, "no": 1, "title": fmt.Sprintf("Track %d", n), "artist": "Artist", "rel_path": "01 - Track.flac",
			"file_size": 1000, "file_mtime_ns": 1, "file_sha256": hash, "codec": "flac", "sample_rate": 44100,
			"channels": 2, "available": 1, "updated_at": 1}
	case "favorites":
		return row{"user_id": f.insert("users"), "track_id": f.insert("tracks"), "created_at": 1}
	case "playlists":
		return row{"id": uuid(n), "user_id": f.insert("users"), "name": fmt.Sprintf("Playlist %d", n), "revision": 1,
			"created_at": 1, "updated_at": 1}
	case "playlist_items":
		return row{"id": uuid(n), "playlist_id": f.insert("playlists"), "track_id": f.insert("tracks"), "position": 0, "added_at": 1}
	}
	f.t.Fatalf("no valid row for table %q", table)
	return nil
}

// insert inserts a valid row and returns its id.
func (f *fixture) insert(table string) string {
	f.t.Helper()
	r := f.valid(table)
	if err := insertRow(f.t.Context(), f.db, table, r); err != nil {
		f.t.Fatalf("a valid row of %s was refused: %v", table, err)
	}
	id, _ := r["id"].(string)
	return id
}

// execer is what a test writes to: a handle, a connection or a
// transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertRow(ctx context.Context, db execer, table string, r row) error {
	cols := slices.Sorted(maps.Keys(r))
	args := make([]any, len(cols))
	quoted := make([]string, len(cols))
	for i, c := range cols {
		args[i] = r[c]
		quoted[i] = `"` + c + `"`
	}
	query := fmt.Sprintf(`INSERT INTO %q (%s) VALUES (%s)`, table,
		strings.Join(quoted, ", "), strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", "))
	_, err := db.ExecContext(ctx, query, args...)
	return err
}

// wantSQLite checks the outcome of a statement: code 0 is success;
// otherwise the error must be that result code of SQLite and name what
// failed.
func wantSQLite(t *testing.T, err error, code int, text string) {
	t.Helper()
	if code == 0 {
		if err != nil {
			t.Errorf("refused: %v", err)
		}
		return
	}
	if err == nil {
		t.Errorf("accepted, want SQLite error %d (%s)", code, text)
		return
	}
	if got := sqliteCode(err); got != code || !strings.Contains(err.Error(), text) {
		t.Errorf("error %v (code %d), want code %d with %q", err, got, code, text)
	}
}

// Every CHECK of the schema refuses what is outside its domain, and
// accepts the edges of it.
func TestSchemaChecks(t *testing.T) {
	s := newStore(t)
	f := &fixture{t: t, db: s.write}
	long := func(n int) string { return strings.Repeat("x", n) }

	for _, tc := range []struct {
		name  string
		table string
		set   row
		check string // the CHECK that must refuse the row; "" if the row is valid
	}{
		{"username in upper case", "users", row{"username": "Alice"}, "users_username_lower_check"},
		{"username of 2 characters", "users", row{"username": "ab"}, "users_username_check"},
		{"username of 3 characters", "users", row{"username": "abc"}, ""},
		{"username of 32 characters", "users", row{"username": long(32)}, ""},
		{"username of 33 characters", "users", row{"username": long(33)}, "users_username_check"},
		{"username with every allowed character", "users", row{"username": "0a.b-c_9"}, ""},
		{"username starting with a dot", "users", row{"username": ".abc"}, "users_username_check"},
		{"username starting with a dash", "users", row{"username": "-abc"}, "users_username_check"},
		{"username starting with an underscore", "users", row{"username": "_abc"}, "users_username_check"},
		{"username with a space", "users", row{"username": "ab c"}, "users_username_check"},
		{"username with a slash", "users", row{"username": "ab/c"}, "users_username_check"},
		{"username with an at sign", "users", row{"username": "a@b.c"}, "users_username_check"},
		{"username outside ASCII", "users", row{"username": "andr\u00e9"}, "users_username_check"},
		{"username with a caret", "users", row{"username": "ab^c"}, "users_username_check"},
		{"username with a bracket", "users", row{"username": "ab]c"}, "users_username_check"},
		{"empty username", "users", row{"username": ""}, "users_username_check"},
		{"role admin", "users", row{"role": "admin"}, ""},
		{"unknown role", "users", row{"role": "root"}, "users_role_check"},
		{"role in upper case", "users", row{"role": "Admin"}, "users_role_check"},
		{"disabled 1", "users", row{"disabled": 1}, ""},
		{"disabled 2", "users", row{"disabled": 2}, "users_disabled_check"},
		{"disabled -1", "users", row{"disabled": -1}, "users_disabled_check"},

		{"session kind token", "sessions", row{"kind": "token"}, ""},
		{"unknown session kind", "sessions", row{"kind": "bearer"}, "sessions_kind_check"},
		{"device name of 100 characters", "sessions", row{"device_name": long(100)}, ""},
		{"device name of 100 characters outside ASCII", "sessions", row{"device_name": strings.Repeat("\u00e9", 100)}, ""},
		{"device name of 101 characters", "sessions", row{"device_name": long(101)}, "sessions_device_name_check"},

		{"year 1", "albums", row{"year": 1, "year_key": 1}, ""},
		{"year 9999", "albums", row{"year": 9999, "year_key": 9999}, ""},
		{"year 0", "albums", row{"year": 0, "year_key": 0}, "albums_year_check"},
		{"year 10000", "albums", row{"year": 10000, "year_key": 10000}, "albums_year_check"},
		{"year key that is not the year", "albums", row{"year": 1959, "year_key": 10000}, "albums_year_key_check"},
		{"year key without a year", "albums", row{"year_key": 1959}, "albums_year_key_check"},
		{"compilation 1", "albums", row{"compilation": 1}, ""},
		{"compilation 2", "albums", row{"compilation": 2}, "albums_compilation_check"},
		{"album available 0", "albums", row{"available": 0}, ""},
		{"album available 2", "albums", row{"available": 2}, "albums_available_check"},
		{"cover.jpg", "albums", row{"cover_rel": "cover.jpg", "cover_mime": "image/jpeg"}, ""},
		{"cover.png", "albums", row{"cover_rel": "cover.png", "cover_mime": "image/png"}, ""},
		{"cover with another name", "albums", row{"cover_rel": "folder.jpg"}, "albums_cover_rel_check"},
		{"cover in a folder", "albums", row{"cover_rel": "Extras/cover.jpg"}, "albums_cover_rel_check"},
		{"cover of another type", "albums", row{"cover_mime": "image/gif"}, "albums_cover_mime_check"},

		{"occurrence 2", "tracks", row{"occurrence": 2}, ""},
		{"occurrence 0", "tracks", row{"occurrence": 0}, "tracks_occurrence_check"},
		{"disc 99", "tracks", row{"disc": 99}, ""},
		{"disc 0", "tracks", row{"disc": 0}, "tracks_disc_check"},
		{"disc 100", "tracks", row{"disc": 100}, "tracks_disc_check"},
		{"track number 999", "tracks", row{"no": 999}, ""},
		{"track number 0", "tracks", row{"no": 0}, "tracks_no_check"},
		{"track number 1000", "tracks", row{"no": 1000}, "tracks_no_check"},
		{"codec mp3", "tracks", row{"codec": "mp3"}, ""},
		{"codec aac", "tracks", row{"codec": "aac"}, ""},
		{"codec alac", "tracks", row{"codec": "alac"}, ""},
		{"unknown codec", "tracks", row{"codec": "opus"}, "tracks_codec_check"},
		{"codec in upper case", "tracks", row{"codec": "FLAC"}, "tracks_codec_check"},
		{"track available 0", "tracks", row{"available": 0}, ""},
		{"track available 2", "tracks", row{"available": 2}, "tracks_available_check"},

		{"playlist name of 1 character", "playlists", row{"name": "x"}, ""},
		{"playlist name of 200 characters", "playlists", row{"name": long(200)}, ""},
		{"empty playlist name", "playlists", row{"name": ""}, "playlists_name_check"},
		{"playlist name of 201 characters", "playlists", row{"name": long(201)}, "playlists_name_check"},
		{"description of 2000 characters", "playlists", row{"description": long(2000)}, ""},
		{"description of 2001 characters", "playlists", row{"description": long(2001)}, "playlists_description_check"},
		{"revision 0", "playlists", row{"revision": 0}, "playlists_revision_check"},
		{"revision -1", "playlists", row{"revision": -1}, "playlists_revision_check"},

		{"position 5", "playlist_items", row{"position": 5}, ""},
		{"position -1", "playlist_items", row{"position": -1}, "playlist_items_position_check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := f.valid(tc.table)
			maps.Copy(r, tc.set)
			err := insertRow(t.Context(), s.write, tc.table, r)
			if tc.check == "" {
				wantSQLite(t, err, 0, "")
				return
			}
			wantSQLite(t, err, sqliteConstraintCheck, "CHECK constraint failed: "+tc.check)

			// The rule also holds when a valid row is changed.
			valid := f.valid(tc.table)
			if err := insertRow(t.Context(), s.write, tc.table, valid); err != nil {
				t.Fatal(err)
			}
			var sets []string
			var args []any
			for _, c := range slices.Sorted(maps.Keys(tc.set)) {
				sets = append(sets, `"`+c+`" = ?`)
				args = append(args, tc.set[c])
			}
			_, err = s.write.ExecContext(t.Context(),
				fmt.Sprintf(`UPDATE %q SET %s WHERE id = ?`, tc.table, strings.Join(sets, ", ")), append(args, valid["id"])...)
			wantSQLite(t, err, sqliteConstraintCheck, "CHECK constraint failed: "+tc.check)
		})
	}
}

// A row with every optional column set is valid too, and its values come
// back as they went in.
func TestSchemaOptionalColumns(t *testing.T) {
	s := newStore(t)
	f := &fixture{t: t, db: s.write}

	album := f.valid("albums")
	maps.Copy(album, row{"year": 1959, "year_key": 1959, "genre": "Jazz", "cover_rel": "cover.jpg",
		"cover_sha256": fmt.Sprintf("%064x", 1), "cover_mime": "image/jpeg", "cover_size": 12345, "cover_mtime_ns": int64(1759300000123456789)})
	if err := insertRow(t.Context(), s.write, "albums", album); err != nil {
		t.Fatal(err)
	}
	track := f.valid("tracks")
	maps.Copy(track, row{"album_id": album["id"], "genre": "Jazz", "bit_depth": 16, "bitrate": 320000, "duration_ms": 2000,
		"lyrics_rel": "01 - Track.lrc", "lyrics_sha256": fmt.Sprintf("%064x", 2),
		"rg_track_gain": -7.12, "rg_track_peak": 0.988, "rg_album_gain": -6.5, "rg_album_peak": 1.0})
	if err := insertRow(t.Context(), s.write, "tracks", track); err != nil {
		t.Fatal(err)
	}
	session := f.valid("sessions")
	session["device_name"] = "Pixel 9"
	if err := insertRow(t.Context(), s.write, "sessions", session); err != nil {
		t.Fatal(err)
	}

	got := string1(t, s.read, `SELECT year || ' ' || typeof(year) || ' ' || cover_mtime_ns || ' ' || typeof(artist_key)
		FROM albums WHERE id = ?`, album["id"])
	if want := "1959 integer 1759300000123456789 blob"; got != want {
		t.Fatalf("album: %q, want %q", got, want)
	}
	got = string1(t, s.read, `SELECT rg_track_gain || ' ' || typeof(rg_track_gain) || ' ' || rg_album_peak || ' ' || typeof(rg_album_peak)
		|| ' ' || occurrence || ' ' || typeof(duration_ms) FROM tracks WHERE id = ?`, track["id"])
	if want := "-7.12 real 1.0 real 1 integer"; got != want {
		t.Fatalf("track: %q, want %q", got, want)
	}
	// The defaults of §5.2.
	if got := string1(t, s.read, `SELECT disabled FROM users LIMIT 1`); got != "0" {
		t.Fatalf("default of users.disabled: %s", got)
	}
	playlist := f.insert("playlists")
	if got := string1(t, s.read, `SELECT '[' || description || ']' FROM playlists WHERE id = ?`, playlist); got != "[]" {
		t.Fatalf("default of playlists.description: %s", got)
	}
}

// Every column that §5.2 does not mark optional refuses NULL.
func TestSchemaNotNull(t *testing.T) {
	s := newStore(t)
	f := &fixture{t: t, db: s.write}
	for _, tbl := range wantSchema {
		for _, c := range tbl.columns {
			if !c.notNull {
				continue
			}
			r := f.valid(tbl.table)
			r[c.name] = nil
			err := insertRow(t.Context(), s.write, tbl.table, r)
			if got := sqliteCode(err); got != sqliteConstraintNotNull ||
				!strings.Contains(err.Error(), "NOT NULL constraint failed: "+tbl.table+"."+c.name) {
				t.Errorf("%s.%s = NULL: %v (code %d), want a NOT NULL failure", tbl.table, c.name, err, got)
			}
		}
	}
}

// Every UNIQUE and PRIMARY KEY refuses a second row with the same key,
// and nothing else is unique: the positions of a playlist are not (T5),
// and the same audio may be twice in an album, or in two albums (§5.4).
func TestSchemaUnique(t *testing.T) {
	s := newStore(t)
	f := &fixture{t: t, db: s.write}

	for _, tc := range []struct {
		table string
		same  []string // the columns the second row shares with the first
		code  int      // 0: the second row is accepted
		text  string
	}{
		{"meta", []string{"key"}, sqliteConstraintPK, "meta.key"},
		{"users", []string{"id"}, sqliteConstraintPK, "users.id"},
		{"users", []string{"username"}, sqliteConstraintUnique, "users.username"},
		{"users", []string{"password_hash", "role", "created_at"}, 0, ""},
		{"sessions", []string{"id"}, sqliteConstraintPK, "sessions.id"},
		{"sessions", []string{"token_hash"}, sqliteConstraintUnique, "sessions.token_hash"},
		{"sessions", []string{"user_id", "kind", "created_at"}, 0, ""},
		{"artists", []string{"id"}, sqliteConstraintUnique, "artists.id"},
		{"artists", []string{"seq"}, sqliteConstraintPK, "artists.seq"},
		{"artists", []string{"name", "sort_key"}, 0, ""},
		{"albums", []string{"id"}, sqliteConstraintUnique, "albums.id"},
		{"albums", []string{"seq"}, sqliteConstraintPK, "albums.seq"},
		{"albums", []string{"artist_id", "title", "rel_path", "receipt_hash"}, 0, ""},
		{"tracks", []string{"id"}, sqliteConstraintUnique, "tracks.id"},
		{"tracks", []string{"seq"}, sqliteConstraintPK, "tracks.seq"},
		{"tracks", []string{"album_id", "fingerprint", "occurrence"}, sqliteConstraintUnique, "tracks.album_id, tracks.fingerprint, tracks.occurrence"},
		{"tracks", []string{"album_id", "fingerprint"}, 0, ""},
		{"tracks", []string{"fingerprint", "occurrence"}, 0, ""},
		{"tracks", []string{"album_id", "disc", "no", "rel_path", "file_sha256"}, 0, ""},
		{"favorites", []string{"user_id", "track_id"}, sqliteConstraintPK, "favorites.user_id, favorites.track_id"},
		{"favorites", []string{"user_id"}, 0, ""},
		{"favorites", []string{"track_id"}, 0, ""},
		{"playlists", []string{"id"}, sqliteConstraintPK, "playlists.id"},
		{"playlists", []string{"user_id", "name"}, 0, ""},
		{"playlist_items", []string{"id"}, sqliteConstraintPK, "playlist_items.id"},
		{"playlist_items", []string{"playlist_id", "track_id", "position"}, 0, ""},
	} {
		t.Run(tc.table+" "+strings.Join(tc.same, "+"), func(t *testing.T) {
			first := f.valid(tc.table)
			// Explicit values for the columns that have a default or are
			// assigned by SQLite, so that the second row can share them.
			switch tc.table {
			case "artists", "albums", "tracks":
				first["seq"] = 1000 + f.next()
			}
			if tc.table == "tracks" {
				first["occurrence"] = 1
			}
			if err := insertRow(t.Context(), s.write, tc.table, first); err != nil {
				t.Fatal(err)
			}
			second := f.valid(tc.table)
			if tc.table == "tracks" && !slices.Contains(tc.same, "occurrence") {
				second["occurrence"] = 2
			}
			for _, c := range tc.same {
				second[c] = first[c]
			}
			err := insertRow(t.Context(), s.write, tc.table, second)
			if tc.code == 0 {
				wantSQLite(t, err, 0, "")
				return
			}
			wantSQLite(t, err, tc.code, "UNIQUE constraint failed: "+tc.text)
		})
	}
}

// Every FOREIGN KEY refuses a row that points to nothing, on insert and
// on update.
func TestSchemaForeignKeysRefuseMissingParents(t *testing.T) {
	s := newStore(t)
	f := &fixture{t: t, db: s.write}
	for _, tc := range []struct{ table, column string }{
		{"sessions", "user_id"},
		{"albums", "artist_id"},
		{"tracks", "album_id"},
		{"favorites", "user_id"},
		{"favorites", "track_id"},
		{"playlists", "user_id"},
		{"playlist_items", "playlist_id"},
		{"playlist_items", "track_id"},
	} {
		t.Run(tc.table+"."+tc.column, func(t *testing.T) {
			r := f.valid(tc.table)
			valid := r[tc.column]
			r[tc.column] = uuid(999_999)
			wantSQLite(t, insertRow(t.Context(), s.write, tc.table, r), sqliteConstraintFK, "FOREIGN KEY constraint failed")

			r[tc.column] = valid
			if err := insertRow(t.Context(), s.write, tc.table, r); err != nil {
				t.Fatal(err)
			}
			_, err := s.write.ExecContext(t.Context(),
				fmt.Sprintf(`UPDATE %q SET %q = ? WHERE %q = ?`, tc.table, tc.column, tc.column), uuid(999_999), valid)
			wantSQLite(t, err, sqliteConstraintFK, "FOREIGN KEY constraint failed")
		})
	}
}

// counts returns the number of rows of each table, as "table=n".
func counts(t *testing.T, db queryer, tables ...string) string {
	t.Helper()
	var out []string
	for _, table := range tables {
		out = append(out, table+"="+string1(t, db, fmt.Sprintf(`SELECT count(*) FROM %q`, table)))
	}
	return strings.Join(out, " ")
}

// Deleting a user deletes its sessions, favorites, playlists and their
// items, and nothing of anybody else (§7.5). An artist, an album or a
// track that something refers to cannot be deleted (I3).
func TestSchemaDeleteRules(t *testing.T) {
	s := newStore(t)
	f := &fixture{t: t, db: s.write}
	ctx := t.Context()

	alice, bob := f.insert("users"), f.insert("users")
	track := f.insert("tracks")
	album := string1(t, s.write, `SELECT album_id FROM tracks WHERE id = ?`, track)
	artist := string1(t, s.write, `SELECT artist_id FROM albums WHERE id = ?`, album)
	for _, user := range []string{alice, bob} {
		session := f.valid("sessions")
		session["user_id"] = user
		playlist := f.valid("playlists")
		playlist["user_id"] = user
		item := f.valid("playlist_items")
		item["playlist_id"], item["track_id"] = playlist["id"], track
		for _, ins := range []struct {
			table string
			r     row
		}{
			{"sessions", session},
			{"favorites", row{"user_id": user, "track_id": track, "created_at": 1}},
			{"playlists", playlist},
			{"playlist_items", item},
		} {
			if err := insertRow(ctx, s.write, ins.table, ins.r); err != nil {
				t.Fatal(err)
			}
		}
	}
	// f.valid made rows of its own for the columns that were then replaced:
	// count only what belongs to the two users.
	owned := func() string {
		return fmt.Sprint(
			string1(t, s.write, `SELECT count(*) FROM sessions WHERE user_id IN (?, ?)`, alice, bob), " sessions, ",
			string1(t, s.write, `SELECT count(*) FROM favorites WHERE user_id IN (?, ?)`, alice, bob), " favorites, ",
			string1(t, s.write, `SELECT count(*) FROM playlists WHERE user_id IN (?, ?)`, alice, bob), " playlists, ",
			string1(t, s.write, `SELECT count(*) FROM playlist_items WHERE playlist_id IN
				(SELECT id FROM playlists WHERE user_id IN (?, ?))`, alice, bob), " items, ",
			string1(t, s.write, `SELECT count(*) FROM playlist_items WHERE track_id = ?`, track), " items of the track")
	}
	if got, want := owned(), "2 sessions, 2 favorites, 2 playlists, 2 items, 2 items of the track"; got != want {
		t.Fatalf("before: %s, want %s", got, want)
	}
	library := counts(t, s.write, "artists", "albums", "tracks")

	// The library is never deleted from under the users' data.
	for _, del := range []struct{ table, id string }{{"tracks", track}, {"albums", album}, {"artists", artist}} {
		_, err := s.write.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %q WHERE id = ?`, del.table), del.id)
		wantSQLite(t, err, sqliteConstraintTrigger, "FOREIGN KEY constraint failed")
	}
	if got := counts(t, s.write, "artists", "albums", "tracks"); got != library {
		t.Fatalf("library rows %s, want %s", got, library)
	}

	mustExec(t, s.write, `DELETE FROM users WHERE id = ?`, alice)
	if got, want := owned(), "1 sessions, 1 favorites, 1 playlists, 1 items, 1 items of the track"; got != want {
		t.Fatalf("after deleting one user: %s, want %s", got, want)
	}
	if got := string1(t, s.write, `SELECT count(*) FROM favorites WHERE user_id = ?`, bob); got != "1" {
		t.Fatalf("the other user has %s favorites, want 1", got)
	}
	if got := counts(t, s.write, "artists", "albums", "tracks"); got != library {
		t.Fatalf("library rows after deleting a user %s, want %s", got, library)
	}
	if got := strings1(t, s.write, `PRAGMA foreign_key_check`); len(got) != 0 {
		t.Fatalf("foreign_key_check: %v", got)
	}
}

// searchRows are the rows of the library the full-text tests index: seq,
// id, title. The seq values have gaps and are not in the order of
// insertion, as after years of scans.
var searchRows = []struct {
	seq   int
	title string
}{
	{40, "Blue in Green"},
	{3, "\u00c9cho Caf\u00e9"},
	{17, "Blues for Alice"},
	{500, "So What"},
	{8, "Green Onions"},
}

// seedSearch inserts searchRows as tracks of one album and indexes them
// as the scanner will: the rowid of the full-text row is the seq of the
// track. It returns the id of each track by seq.
func seedSearch(t *testing.T, s *Store, f *fixture) map[int]string {
	t.Helper()
	album := f.insert("albums")
	ids := map[int]string{}
	for _, sr := range searchRows {
		r := f.valid("tracks")
		maps.Copy(r, row{"seq": sr.seq, "album_id": album, "title": sr.title})
		if err := insertRow(t.Context(), s.write, "tracks", r); err != nil {
			t.Fatal(err)
		}
		mustExec(t, s.write, `INSERT INTO search_tracks (rowid, title, artist, album) VALUES (?, ?, ?, ?)`,
			sr.seq, sr.title, "Miles Davis", "Kind of Blue")
		ids[sr.seq] = r["id"].(string)
	}
	return ids
}

// searchTracks returns the ids of the tracks that match, through the
// rowid of the full-text table.
func searchTracks(t *testing.T, db queryer, match string) []string {
	t.Helper()
	return strings1(t, db, `SELECT t.id FROM search_tracks JOIN tracks t ON t.rowid = search_tracks.rowid
		WHERE search_tracks MATCH ? ORDER BY t.id`, match)
}

// The full-text tables of §10.1 exist and answer MATCH as the search
// needs: whole words and prefixes, without case and without diacritics.
func TestSearchTablesMatch(t *testing.T) {
	s := newStore(t)
	ids := seedSearch(t, s, &fixture{t: t, db: s.write})

	if got := strings1(t, s.read, `SELECT name FROM sqlite_master WHERE sql LIKE 'CREATE VIRTUAL TABLE%' ORDER BY name`); !slices.Equal(got, []string{"search_albums", "search_artists", "search_tracks"}) {
		t.Fatalf("full-text tables: %v", got)
	}
	for table, columns := range map[string][]string{
		"search_artists": {"name"},
		"search_albums":  {"title", "artist"},
		"search_tracks":  {"title", "artist", "album"},
	} {
		if got := strings1(t, s.read, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table); !slices.Equal(got, columns) {
			t.Errorf("%s has the columns %v, want %v", table, got, columns)
		}
	}
	mustExec(t, s.write, `INSERT INTO search_artists (rowid, name) VALUES (7, 'Bj`+"\u00f6"+`rk')`)
	mustExec(t, s.write, `INSERT INTO search_albums (rowid, title, artist) VALUES (9, 'Hom`+"\u00f3"+`genic', 'Bj`+"\u00f6"+`rk')`)

	for _, tc := range []struct {
		match string
		want  []int // seq of the tracks
	}{
		{`"so"*`, []int{500}},
		{`"kind"`, []int{3, 8, 17, 40, 500}}, // every track is of the album Kind of Blue
		{`title : "blue"*`, []int{17, 40}},
		{`title : "blue"`, []int{40}}, // the whole word
		{`title : "BLUE"`, []int{40}},
		{`title : "bl"*`, []int{17, 40}},              // the prefix index of 2
		{`title : "blu"*`, []int{17, 40}},             // the prefix index of 3
		{`title : "echo"* title : "cafe"*`, []int{3}}, // diacritics removed, both terms required
		{"title : \"\u00c9CHO\"", []int{3}},
		{`title : "gre"*`, []int{8, 40}},
		{`title : "green"* title : "blue"*`, []int{40}},
		{`artist : "miles"*`, []int{3, 8, 17, 40, 500}},
		{`title : "miles"*`, nil},
		{`"nothing"*`, nil},
	} {
		var want []string
		for _, seq := range tc.want {
			want = append(want, ids[seq])
		}
		slices.Sort(want)
		if got := searchTracks(t, s.read, tc.match); !slices.Equal(got, want) {
			t.Errorf("MATCH %s: %v, want %v", tc.match, got, want)
		}
	}
	if got := string1(t, s.read, `SELECT rowid FROM search_artists WHERE search_artists MATCH '"bjork"*'`); got != "7" {
		t.Errorf("search_artists: rowid %s, want 7", got)
	}
	if got := string1(t, s.read, `SELECT rowid FROM search_albums WHERE search_albums MATCH 'title : "homogenic" artist : "bjo"*'`); got != "9" {
		t.Errorf("search_albums: rowid %s, want 9", got)
	}
}

// DESIGN.md T6: the backup copies the database with VACUUM INTO, which
// rebuilds every table. The full-text rows point to the rowid of the
// tracks, so the copy must keep every rowid: it does because seq is
// declared as the INTEGER PRIMARY KEY. The search gives the same tracks
// in the copy.
func TestVacuumIntoKeepsRowidsAndSearch(t *testing.T) {
	s := newStore(t)
	f := &fixture{t: t, db: s.write}
	seedSearch(t, s, f)
	for _, table := range []string{"artists", "albums"} {
		for _, seq := range []int{900, 77, 5000} {
			r := f.valid(table)
			r["seq"] = seq
			if err := insertRow(t.Context(), s.write, table, r); err != nil {
				t.Fatal(err)
			}
		}
	}

	matches := []string{`title : "blue"*`, `title : "echo"*`, `title : "gre"*`, `"what"`, `artist : "miles"`}
	state := func(db queryer) []string {
		var out []string
		for _, table := range []string{"artists", "albums", "tracks"} {
			// seq is the rowid, for every row.
			if got := string1(t, db, fmt.Sprintf(`SELECT count(*) FROM %q WHERE rowid IS NOT seq`, table)); got != "0" {
				t.Fatalf("%s: %s rows whose rowid is not their seq", table, got)
			}
			out = append(out, strings1(t, db, fmt.Sprintf(`SELECT rowid || ' ' || id FROM %q ORDER BY rowid`, table))...)
		}
		out = append(out, strings1(t, db, `SELECT rowid || ' ' || title FROM search_tracks ORDER BY rowid`)...)
		for _, m := range matches {
			out = append(out, m+" -> "+strings.Join(searchTracks(t, db, m), ","))
		}
		return out
	}
	before := state(s.read)
	for _, m := range matches {
		if len(searchTracks(t, s.read, m)) == 0 {
			t.Fatalf("MATCH %s finds nothing: the test would prove nothing", m)
		}
	}

	backup := filepath.Join(t.TempDir(), "backup.db")
	// On the write handle: a connection that cannot write (query_only)
	// refuses VACUUM INTO, although it only writes another file.
	mustExec(t, s.write, `VACUUM INTO ?`, backup)

	copied := rawOpen(t, backup)
	if got := state(copied); !slices.Equal(got, before) {
		t.Fatalf("the copy differs:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(before, "\n     "))
	}
	if got := string1(t, copied, `PRAGMA integrity_check`); got != "ok" {
		t.Fatalf("integrity_check of the copy: %s", got)
	}
	mustExec(t, copied, `INSERT INTO search_tracks (search_tracks) VALUES ('integrity-check')`)
	// The copy is a database this binary opens as it is.
	if err := copied.Close(); err != nil {
		t.Fatal(err)
	}
	again := openStore(t, backup)
	defer closeStore(t, again)
	if got := state(again.read); !slices.Equal(got, before) {
		t.Fatalf("the copy differs once opened:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(before, "\n     "))
	}
}
