package store

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pressly/goose/v3"

	"vibrance/migrations"
)

// openReader opens a reader, which the test closes.
func openReader(t *testing.T, path string, frozen bool) *Reader {
	t.Helper()
	r, err := OpenReader(t.Context(), path, frozen)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

// olderDatabase makes a database at path that an older binary left: only
// the first migration is applied.
func olderDatabase(t *testing.T, path string) {
	t.Helper()
	db := rawOpen(t, path)
	p, err := goose.NewProvider(goose.DialectSQLite3, db, migrations.FS, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
}

// DESIGN.md §11.4: the commands that only read open the database without
// migrating it or creating it, and say which schema it has.
func TestOpenReaderSchemas(t *testing.T) {
	current := dbPath(t)
	closeStore(t, openStore(t, current))
	r := openReader(t, current, false)
	if err := r.CheckCurrent(); err != nil {
		t.Fatalf("CheckCurrent of a current database: %v", err)
	}
	if got, want := r.SchemaVersion(), int64(len(migrationFiles(t))); got != want {
		t.Fatalf("SchemaVersion %d, want %d", got, want)
	}

	older := dbPath(t)
	olderDatabase(t, older)
	r = openReader(t, older, false)
	wantCode(t, r.CheckCurrent(), CodeSchemaOld)
	if r.SchemaVersion() != 1 {
		t.Fatalf("SchemaVersion %d, want 1", r.SchemaVersion())
	}
	// Nothing was migrated.
	if got := string1(t, rawOpen(t, older), `SELECT max(version_id) FROM goose_db_version`); got != "1" {
		t.Fatalf("the older database has version %s after it was read", got)
	}

	newer := dbPath(t)
	closeStore(t, openStore(t, newer))
	// Closed, so that the change is in the file and not in the write-ahead
	// log, which a frozen reader does not read: a frozen database is a copy
	// made by CopyInto, which has none.
	raw, err := sql.Open("sqlite", newer)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.ExecContext(t.Context(), `INSERT INTO goose_db_version (version_id, is_applied) VALUES (9999, 1)`)
	if err := errors.Join(err, raw.Close()); err != nil {
		t.Fatal(err)
	}
	for _, frozen := range []bool{false, true} {
		if _, err := OpenReader(t.Context(), newer, frozen); err == nil {
			t.Fatal("OpenReader accepted a newer database")
		} else {
			wantCode(t, err, CodeSchemaTooNew)
		}
	}
}

// What is not a database of Vibrance is refused, and never created or
// changed.
func TestOpenReaderRefusals(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	empty := filepath.Join(dir, "empty.db")
	junk := filepath.Join(dir, "junk.db")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	junkBytes := bytes.Repeat([]byte("not a database "), 100)
	if err := os.WriteFile(junk, junkBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{missing, empty, junk} {
		for _, frozen := range []bool{false, true} {
			if _, err := OpenReader(t.Context(), path, frozen); err == nil {
				t.Fatalf("OpenReader(%s, %v) accepted it", filepath.Base(path), frozen)
			} else {
				wantCode(t, err, CodeOpen)
			}
		}
	}
	if got := dirNames(t, dir); !slices.Equal(got, []string{"empty.db", "junk.db"}) {
		t.Fatalf("the folder has %v after the refusals", got)
	}
	if b, err := os.ReadFile(empty); err != nil || len(b) != 0 {
		t.Fatalf("the empty file is now %d bytes, %v", len(b), err)
	}
	if b, err := os.ReadFile(junk); err != nil || !bytes.Equal(b, junkBytes) {
		t.Fatalf("the other file changed: %v", err)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// A reader cannot write; a frozen one leaves the file as it was, byte for
// byte, and no file next to it.
func TestReaderOnlyReads(t *testing.T) {
	path := dbPath(t)
	s := openStore(t, path)
	seed(t, s)
	closeStore(t, s)
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	if err := CopyInto(t.Context(), path, copyPath); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		path   string
		frozen bool
	}{{path, false}, {copyPath, true}} {
		r := openReader(t, p.path, p.frozen)
		err := r.Read(t.Context(), func(q *Queries) error {
			_, err := q.Conn().ExecContext(t.Context(), `DELETE FROM artists`)
			return err
		})
		if err == nil {
			t.Fatalf("a reader of %s deleted rows", filepath.Base(p.path))
		}
		var counts CountRowsRow
		if err := r.Read(t.Context(), func(q *Queries) error {
			var err error
			counts, err = q.CountRows(t.Context())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if counts.Artists != 1 || counts.Albums != 1 {
			t.Fatalf("counts %+v", counts)
		}
	}
	if got := dirNames(t, filepath.Dir(copyPath)); !slices.Equal(got, []string{"copy.db"}) {
		t.Fatalf("reading the copy left %v", got)
	}
	if after, err := os.ReadFile(copyPath); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("reading the copy changed it: %v", err)
	}
}

// CopyInto writes a new file only, and never creates its source.
func TestCopyIntoRefusals(t *testing.T) {
	path := dbPath(t)
	closeStore(t, openStore(t, path))
	existing := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CopyInto(t.Context(), path, existing); err == nil {
		t.Fatal("CopyInto overwrote a file")
	}
	if b, err := os.ReadFile(existing); err != nil || string(b) != "keep" {
		t.Fatalf("the file is now %q, %v", b, err)
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if err := CopyInto(t.Context(), missing, filepath.Join(t.TempDir(), "copy.db")); err == nil {
		t.Fatal("CopyInto copied a database that does not exist")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("CopyInto created its source: %v", err)
	}
}

// The checks of the doctor (DESIGN.md §11.4) find nothing in a sound
// database, and find a broken foreign key and wrong counters.
func TestIntegrityQueries(t *testing.T) {
	path := dbPath(t)
	s := openStore(t, path)
	t.Cleanup(func() { closeStore(t, s) })
	seed(t, s)
	ctx := t.Context()
	write(t, s, func(q *Queries) error {
		return errors.Join(
			q.UpsertTrack(ctx, trackRow(testTrack, testAlbum, "f1", 1, 1000, 1)),
			q.UpsertTrack(ctx, trackRow(testTrack2, testAlbum, "f2", 1, 0, 1)),
			q.UpdateAlbumCounters(ctx, testAlbum),
		)
	})
	inspect := func() (damage []string, violations []ForeignKeyViolation, counters []ListAlbumsWithWrongCountersRow) {
		t.Helper()
		r := openReader(t, path, false)
		err := r.Read(ctx, func(q *Queries) error {
			var err error
			if damage, err = q.IntegrityCheck(ctx); err != nil {
				return err
			}
			if violations, err = q.ForeignKeyCheck(ctx); err != nil {
				return err
			}
			counters, err = q.ListAlbumsWithWrongCounters(ctx)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return damage, violations, counters
	}
	if d, v, c := inspect(); len(d)+len(v)+len(c) != 0 {
		t.Fatalf("a sound database: %v %v %v", d, v, c)
	}

	raw := rawOpen(t, path)
	// One connection: the PRAGMA below is a setting of a connection.
	raw.SetMaxOpenConns(1)
	mustExec(t, raw, `UPDATE albums SET duration_ms = 999 WHERE id = ?`, testAlbum)
	// A favorite of an account that does not exist, as only a connection
	// without foreign keys can write it.
	mustExec(t, raw, `PRAGMA foreign_keys = OFF`)
	mustExec(t, raw, `INSERT INTO favorites (user_id, track_id, created_at) VALUES ('nobody', ?, 1)`, testTrack)
	_, v, c := inspect()
	if len(v) != 1 || v[0].Table != "favorites" || v[0].Parent != "users" || v[0].Rowid == 0 {
		t.Fatalf("violations %+v", v)
	}
	want := []ListAlbumsWithWrongCountersRow{{ID: testAlbum, TrackCount: 2, DurationMs: 999, AvailableTracks: 2, AvailableDurationMs: 1000}}
	if !slices.Equal(c, want) {
		t.Fatalf("counters %+v, want %+v", c, want)
	}
	mustExec(t, raw, `UPDATE albums SET duration_ms = 1000, track_count = 1 WHERE id = ?`, testAlbum)
	if _, _, c := inspect(); len(c) != 1 || c[0].TrackCount != 1 || c[0].AvailableTracks != 2 {
		t.Fatalf("counters %+v", c)
	}
}
