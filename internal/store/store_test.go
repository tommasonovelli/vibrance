package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"

	"vibrance/migrations"
)

// The tests of this package use a real SQLite database in t.TempDir(),
// which the gate puts on an ext4 volume (DESIGN.md §12.1). The SQL they
// need beyond the queries of sql/ is written here: it sets up and inspects
// what the product code must never do itself.

// The result codes of SQLite the tests expect.
const (
	sqliteBusy              = 5
	sqliteReadOnly          = 8
	sqliteConstraintCheck   = 275
	sqliteConstraintFK      = 787
	sqliteConstraintNotNull = 1299
	sqliteConstraintPK      = 1555
	sqliteConstraintTrigger = 1811
	sqliteConstraintUnique  = 2067
)

// dbPath is the database file of a test, in a folder of its own.
func dbPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "vibrance.db")
}

// openStore opens the database at path; the test closes it.
func openStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func closeStore(t *testing.T, s *Store) {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// newStore is a new, migrated database, closed when the test ends.
func newStore(t *testing.T) *Store {
	t.Helper()
	s := openStore(t, dbPath(t))
	t.Cleanup(func() { closeStore(t, s) })
	return s
}

// rawOpen opens a database file as another program would: no settings, no
// migrations. It is closed when the test ends.
func rawOpen(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

// queryer is what a test queries: a handle, a connection or a transaction.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// strings1 returns the first column of every row of a query.
func strings1(t *testing.T, db queryer, query string, args ...any) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		out = append(out, s)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out
}

// string1 returns the single value of a query, as text.
func string1(t *testing.T, db queryer, query string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRowContext(t.Context(), query, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return s
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// sqliteCode is the result code of the SQLite error in err's tree, or 0.
func sqliteCode(err error) int {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code()
	}
	return 0
}

// wantCode checks that err is an *Error of the store with the given code.
func wantCode(t *testing.T, err error, code string) *Error {
	t.Helper()
	var se *Error
	if !errors.As(err, &se) || se.Code != code {
		t.Fatalf("error %v (code %q), want an *Error with code %q", err, Code(err), code)
	}
	return se
}

// appliedVersions are the migrations recorded in the database, goose's
// version 0 included.
func appliedVersions(t *testing.T, db queryer) []string {
	t.Helper()
	return strings1(t, db, `SELECT version_id FROM goose_db_version WHERE is_applied = 1 ORDER BY version_id`)
}

// migrationFiles are the names of the embedded migrations, in order.
func migrationFiles(t *testing.T) []string {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	return names
}

// schemaText is every object of the schema with its definition, except
// the statistics tables that PRAGMA optimize may add when the store closes.
func schemaText(t *testing.T, db queryer) []string {
	t.Helper()
	return strings1(t, db, `SELECT type || ' ' || name || ' ' || coalesce(sql, '') FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_stat%' ORDER BY name`)
}

// A new database gets every embedded migration, once and in order, and is
// in WAL mode. Opening it again applies nothing and keeps its data.
func TestOpenMigratesFromEmptyAndIsIdempotent(t *testing.T) {
	path := dbPath(t)
	s := openStore(t, path)

	want := []string{"0"}
	for i := range migrationFiles(t) {
		want = append(want, strconv.Itoa(i+1))
	}
	if got := appliedVersions(t, s.read); !slices.Equal(got, want) {
		t.Fatalf("applied versions %v, want %v", got, want)
	}
	if got := string1(t, s.read, `PRAGMA journal_mode`); got != "wal" {
		t.Fatalf("journal mode %q, want wal", got)
	}
	if got := string1(t, s.read, `PRAGMA integrity_check`); got != "ok" {
		t.Fatalf("integrity_check: %s", got)
	}
	if err := s.WithWriteTx(t.Context(), func(q *Queries) error {
		return q.SetMeta(t.Context(), SetMetaParams{Key: "ffmpeg_version", Value: "8.1.3-musiclib1"})
	}); err != nil {
		t.Fatal(err)
	}
	schema := schemaText(t, s.read)
	closeStore(t, s)

	for range 2 {
		again := openStore(t, path)
		if got := appliedVersions(t, again.read); !slices.Equal(got, want) {
			t.Fatalf("applied versions after reopening %v, want %v", got, want)
		}
		if got := schemaText(t, again.read); !slices.Equal(got, schema) {
			t.Fatalf("the schema changed by reopening:\n%v\nwas\n%v", got, schema)
		}
		var value string
		if err := again.Read(t.Context(), func(q *Queries) (err error) {
			value, err = q.GetMeta(t.Context(), "ffmpeg_version")
			return err
		}); err != nil || value != "8.1.3-musiclib1" {
			t.Fatalf("meta after reopening: %q, %v", value, err)
		}
		closeStore(t, again)
	}
}

// The migrations are numbered 00001, 00002, ... without gaps and only go
// forward (I13). sqlc reads each of them except those that create the
// full-text tables (T7), and sqlc.yaml must say so file by file.
func TestMigrationFiles(t *testing.T) {
	sqlcConfig, err := os.ReadFile(filepath.Join("..", "..", "sqlc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	name := regexp.MustCompile(`^(\d{5})_[a-z0-9_]+\.sql$`)
	for i, file := range migrationFiles(t) {
		m := name.FindStringSubmatch(file)
		if m == nil {
			t.Fatalf("migration %q is not named 0000N_name.sql", file)
		}
		if n, err := strconv.Atoi(m[1]); err != nil || n != i+1 {
			t.Fatalf("migration %q: version %s, want %d (no gaps)", file, m[1], i+1)
		}
		text, err := fs.ReadFile(migrations.FS, file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(text), "\n-- +goose Up\n") {
			t.Errorf("migration %q has no `-- +goose Up` line", file)
		}
		if strings.Contains(string(text), "+goose Down") {
			t.Errorf("migration %q has a Down section: migrations only go forward", file)
		}
		virtual := strings.Contains(string(text), "CREATE VIRTUAL TABLE")
		listed := strings.Contains(string(sqlcConfig), "\n      - migrations/"+file+"\n")
		if virtual == listed {
			t.Errorf("migration %q: creates virtual tables = %v, listed in sqlc.yaml = %v; "+
				"sqlc.yaml must list every migration except the full-text ones", file, virtual, listed)
		}
	}
}

// sqlc 1.31.1 cuts the text of a query at the wrong byte when its file
// holds a character outside ASCII (NOTES.md N-023): the generated SQL
// then loses its last characters, and `sqlc diff` does not notice.
func TestQueryFilesAreASCII(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "sql", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query files found: %v", err)
	}
	for _, file := range files {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, b := range text {
			if b >= 0x80 {
				t.Errorf("%s: byte %d is not ASCII (line %d)", file, i, 1+strings.Count(string(text[:i]), "\n"))
				break
			}
		}
	}
}

// A database migrated by a newer binary is refused (I13), and left as it
// was.
func TestOpenRefusesANewerSchema(t *testing.T) {
	path := dbPath(t)
	closeStore(t, openStore(t, path))

	newer := len(migrationFiles(t)) + 1
	raw := rawOpen(t, path)
	mustExec(t, raw, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, newer)
	mustExec(t, raw, `CREATE TABLE from_the_future (x integer)`)
	before := strings1(t, raw, `SELECT name FROM sqlite_master ORDER BY name`)

	s, err := Open(t.Context(), path)
	if err == nil {
		closeStore(t, s)
		t.Fatal("Open accepted a database with a newer schema")
	}
	se := wantCode(t, err, CodeSchemaTooNew)
	want := "schema version " + strconv.Itoa(newer) + " and this binary knows up to " + strconv.Itoa(newer-1)
	if !strings.Contains(se.Error(), want) {
		t.Fatalf("the error does not say %q: %v", want, se)
	}

	if got := strings1(t, raw, `SELECT name FROM sqlite_master ORDER BY name`); !slices.Equal(got, before) {
		t.Fatalf("the refused database changed:\n%v\nwas\n%v", got, before)
	}
	if got := string1(t, raw, `SELECT max(version_id) FROM goose_db_version`); got != strconv.Itoa(newer) {
		t.Fatalf("the refused database has version %s, want %d", got, newer)
	}
	// The refusal holds no lock on the file: the newer binary can use it.
	mustExec(t, raw, `INSERT INTO from_the_future (x) VALUES (1)`)
}

// A migration that fails is store_migrate, and leaves nothing of itself
// behind: a migration is one transaction.
func TestOpenMigrationFailureAppliesNothing(t *testing.T) {
	path := dbPath(t)
	raw := rawOpen(t, path)
	// The last table of the first migration is already there.
	mustExec(t, raw, `CREATE TABLE playlist_items (x integer)`)

	s, err := Open(t.Context(), path)
	if err == nil {
		closeStore(t, s)
		t.Fatal("Open succeeded on a database where a migration cannot apply")
	}
	wantCode(t, err, CodeMigrate)
	if !strings.Contains(err.Error(), "playlist_items") {
		t.Fatalf("the error does not name the table: %v", err)
	}
	if got := strings1(t, raw, `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`); !slices.Equal(got, []string{"goose_db_version", "playlist_items", "sqlite_sequence"}) {
		t.Fatalf("tables after the failed migration: %v", got)
	}
	if got := appliedVersions(t, raw); !slices.Equal(got, []string{"0"}) {
		t.Fatalf("applied versions after the failed migration: %v", got)
	}
}

// What cannot be opened is store_open: a missing folder, a file that is
// not a database, a folder in place of the file.
func TestOpenFailures(t *testing.T) {
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(garbage, []byte(strings.Repeat("this is not a database\n", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path string }{
		{"missing folder", filepath.Join(dir, "missing", "vibrance.db")},
		{"not a database", garbage},
		{"a folder", dir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(t.Context(), tc.path)
			if err == nil {
				closeStore(t, s)
				t.Fatal("Open succeeded")
			}
			se := wantCode(t, err, CodeOpen)
			if se.Err == nil {
				t.Fatalf("the error has no cause: %v", err)
			}
		})
	}
	// Nothing was created or changed by the refusals.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "garbage.db" {
		t.Fatalf("the folder holds %v after the refusals", entries)
	}
}

// A cancelled context stops Open with one of its typed errors.
func TestOpenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s, err := Open(ctx, dbPath(t))
	if err == nil {
		closeStore(t, s)
		t.Fatal("Open succeeded with a cancelled context")
	}
	if Code(err) == "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v (code %q), want a typed error that wraps context.Canceled", err, Code(err))
	}
}

// The path is a path, whatever characters it holds: `?`, `#` and `%` mean
// something in the URI that SQLite is given.
func TestOpenPathWithSpecialCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a b?mode=memory#c%41&_pragma=x")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "vibrance?.db")
	s := openStore(t, path)
	if err := s.WithWriteTx(t.Context(), func(q *Queries) error {
		return q.SetMeta(t.Context(), SetMetaParams{Key: "k", Value: "v"})
	}); err != nil {
		t.Fatal(err)
	}
	closeStore(t, s)
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("the database is not at %s: %v", path, err)
	}
}

// A database that stays out of WAL mode is refused. SQLite reports no
// error when it cannot switch: only the mode it kept.
func TestCheckWAL(t *testing.T) {
	path := dbPath(t)
	raw := rawOpen(t, path)
	mustExec(t, raw, `CREATE TABLE x (y integer)`)
	err := checkWAL(t.Context(), raw)
	se := wantCode(t, err, CodeOpen)
	if !strings.Contains(se.Error(), `journal mode "delete"`) {
		t.Fatalf("the error does not name the mode: %v", se)
	}

	mustExec(t, raw, `PRAGMA journal_mode = WAL`)
	if err := checkWAL(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
}

// connSettings reads the settings of one connection.
func connSettings(t *testing.T, conn *sql.Conn) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, name := range []string{"foreign_keys", "busy_timeout", "journal_mode", "synchronous", "query_only"} {
		got[name] = string1(t, conn, `PRAGMA `+name)
	}
	return got
}

// discard makes database/sql throw the connection away instead of keeping
// it in the pool.
func discard(t *testing.T, conn *sql.Conn) {
	t.Helper()
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		t.Fatal(err)
	}
}

// DESIGN.md T4: the settings are per connection, so every connection of
// both handles must have them: those open at the same time, and those
// that replace a connection that went away.
func TestSettingsOnEveryConnection(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	wantRead := map[string]string{"foreign_keys": "1", "busy_timeout": "5000", "journal_mode": "wal", "synchronous": "2", "query_only": "1"}
	wantWrite := map[string]string{"foreign_keys": "1", "busy_timeout": "5000", "journal_mode": "wal", "synchronous": "2", "query_only": "0"}
	check := func(name string, conn *sql.Conn, want map[string]string) {
		t.Helper()
		got := connSettings(t, conn)
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s = %s, want %s (all: %v)", name, k, got[k], v, got)
			}
		}
	}

	for round := range 3 {
		// Eight read connections at once: the pool has to open new ones.
		var conns []*sql.Conn
		for i := range 8 {
			conn, err := s.read.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			conns = append(conns, conn)
			check("read connection "+strconv.Itoa(round)+"/"+strconv.Itoa(i), conn, wantRead)
		}
		if got := s.read.Stats().OpenConnections; got != 8 {
			t.Fatalf("%d read connections open, want 8", got)
		}
		for _, conn := range conns {
			discard(t, conn)
		}

		conn, err := s.write.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		check("write connection "+strconv.Itoa(round), conn, wantWrite)
		// Foreign keys are enforced, not only switched on.
		_, err = conn.ExecContext(ctx, `INSERT INTO sessions (id, token_hash, user_id, kind, created_at, last_used_at, expires_at)
			VALUES ('s', 'h', 'nobody', 'cookie', 1, 1, 1)`)
		if sqliteCode(err) != sqliteConstraintFK {
			t.Fatalf("write connection %d: a session of a missing user: %v", round, err)
		}
		// The next round gets a new write connection.
		discard(t, conn)
	}
}

// There is one write connection (I11), and the read handle cannot write.
func TestOneWriterAndReadOnlyReaders(t *testing.T) {
	s := newStore(t)
	if got := s.write.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("the write handle allows %d connections, want 1", got)
	}
	if got := s.read.Stats().MaxOpenConnections; got == 1 {
		t.Fatal("the read handle is limited to one connection: it is a pool")
	}

	err := s.Read(t.Context(), func(q *Queries) error {
		return q.SetMeta(t.Context(), SetMetaParams{Key: "k", Value: "v"})
	})
	if sqliteCode(err) != sqliteReadOnly {
		t.Fatalf("a write in a read transaction: %v, want SQLITE_READONLY", err)
	}
	_, err = s.read.ExecContext(t.Context(), `DELETE FROM meta`)
	if sqliteCode(err) != sqliteReadOnly {
		t.Fatalf("a write on the read handle: %v, want SQLITE_READONLY", err)
	}
	err = s.Read(t.Context(), func(q *Queries) error {
		_, err := q.GetMeta(t.Context(), "k")
		return err
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("the refused write left something: %v", err)
	}
}

// Close moves the write-ahead log into the database file and empties it:
// what is left is the one file, complete. The database opens again with
// its data.
func TestCloseCheckpoints(t *testing.T) {
	path := dbPath(t)
	s := openStore(t, path)
	for i := range 50 {
		if err := s.WithWriteTx(t.Context(), func(q *Queries) error {
			return q.SetMeta(t.Context(), SetMetaParams{Key: "k" + strconv.Itoa(i), Value: strings.Repeat("v", 1000)})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("no write-ahead log while the database is open: %v", err)
	}
	closeStore(t, s)

	for _, suffix := range []string{"-wal", "-shm"} {
		if info, err := os.Stat(path + suffix); err == nil && info.Size() != 0 {
			t.Fatalf("%s holds %d bytes after Close", path+suffix, info.Size())
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
	}
	// The file alone is the whole database: copied elsewhere, it has
	// everything.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	copied := dbPath(t)
	if err := os.WriteFile(copied, data, 0o644); err != nil {
		t.Fatal(err)
	}
	again := openStore(t, copied)
	defer closeStore(t, again)
	if got := string1(t, again.read, `SELECT count(*) FROM meta`); got != "50" {
		t.Fatalf("%s rows of meta in the copied file, want 50", got)
	}

	// The closed store refuses everything.
	if err := s.WithWriteTx(t.Context(), func(*Queries) error { return nil }); err == nil {
		t.Fatal("a write transaction on a closed store succeeded")
	}
	if err := s.Read(t.Context(), func(*Queries) error { return nil }); err == nil {
		t.Fatal("a read transaction on a closed store succeeded")
	}
}

// A checkpoint that another connection keeps from emptying the log is not
// passed over in silence: Close reports store_close. Nothing is lost.
func TestCloseReportsABlockedCheckpoint(t *testing.T) {
	path := dbPath(t)
	s := openStore(t, path)
	if err := s.WithWriteTx(t.Context(), func(q *Queries) error {
		return q.SetMeta(t.Context(), SetMetaParams{Key: "k", Value: "before"})
	}); err != nil {
		t.Fatal(err)
	}

	// Another process in the middle of a read, which the log must keep
	// serving.
	other := rawOpen(t, path)
	tx, err := other.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string1(t, tx, `SELECT value FROM meta WHERE "key" = 'k'`); got != "before" {
		t.Fatalf("the other reader sees %q", got)
	}
	if err := s.WithWriteTx(t.Context(), func(q *Queries) error {
		return q.SetMeta(t.Context(), SetMetaParams{Key: "k", Value: "after"})
	}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err = s.Close()
	se := wantCode(t, err, CodeClose)
	if !strings.Contains(se.Error(), "checkpointing the write-ahead log") {
		t.Fatalf("the error does not name the checkpoint: %v", se)
	}
	// It waited for the reader as long as any statement waits for a lock.
	if elapsed := time.Since(start); elapsed < 4*time.Second || elapsed > 30*time.Second {
		t.Fatalf("Close took %s, want about the busy timeout of 5s", elapsed)
	}

	if got := string1(t, tx, `SELECT value FROM meta WHERE "key" = 'k'`); got != "before" {
		t.Fatalf("the other reader now sees %q", got)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	again := openStore(t, path)
	defer closeStore(t, again)
	if got := string1(t, again.read, `SELECT value FROM meta WHERE "key" = 'k'`); got != "after" {
		t.Fatalf("after the unclean close the value is %q, want after", got)
	}
}

// meta: a value is read back, replaced, and a key never set is
// sql.ErrNoRows.
func TestMeta(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	get := func(key string) (value string, err error) {
		err = s.Read(ctx, func(q *Queries) (err error) {
			value, err = q.GetMeta(ctx, key)
			return err
		})
		return value, err
	}
	set := func(key, value string) {
		t.Helper()
		if err := s.WithWriteTx(ctx, func(q *Queries) error {
			return q.SetMeta(ctx, SetMetaParams{Key: key, Value: value})
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := get("collate_version"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a key never set: %v, want sql.ErrNoRows", err)
	}
	set("collate_version", "v0.41.0")
	set("ffmpeg_version", "8.1.3-musiclib1")
	set("collate_version", "v0.42.0")
	set("empty", "")
	for key, want := range map[string]string{"collate_version": "v0.42.0", "ffmpeg_version": "8.1.3-musiclib1", "empty": ""} {
		if got, err := get(key); err != nil || got != want {
			t.Fatalf("meta %q = %q, %v; want %q", key, got, err, want)
		}
	}
	if got := string1(t, s.read, `SELECT count(*) FROM meta`); got != "3" {
		t.Fatalf("%s rows of meta, want 3", got)
	}
}

func TestErrorAndCode(t *testing.T) {
	cause := errors.New("disk on fire")
	for _, tc := range []struct {
		err  error
		code string
		text string
	}{
		{&Error{Code: CodeSchemaTooNew, Msg: "too new"}, "store_schema_too_new", "too new"},
		{&Error{Code: CodeOpen, Msg: "cannot open", Err: cause}, "store_open", "cannot open: disk on fire"},
		{errors.Join(errors.New("first"), &Error{Code: CodeMigrate, Msg: "x"}), "store_migrate", "first\nx"},
		{&Error{Code: CodeClose, Msg: "x"}, "store_close", "x"},
		{cause, "", "disk on fire"},
		{nil, "", ""},
	} {
		if got := Code(tc.err); got != tc.code {
			t.Errorf("Code(%v) = %q, want %q", tc.err, got, tc.code)
		}
		if tc.err != nil && tc.err.Error() != tc.text {
			t.Errorf("Error() = %q, want %q", tc.err.Error(), tc.text)
		}
	}
	if err := (&Error{Code: CodeOpen, Msg: "x", Err: cause}); !errors.Is(err, cause) {
		t.Error("an *Error does not unwrap to its cause")
	}
}
