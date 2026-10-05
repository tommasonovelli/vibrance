package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"vibrance/internal/buildinfo"
	"vibrance/internal/search"
	"vibrance/internal/store"
	"vibrance/migrations"
)

// The operational commands of the database (DESIGN.md §11.4, step S21),
// on the database of a world: the fixture library indexed by the real
// indexer, accounts, sessions, playlists and favorites made over the API.

// backupClock is the time the backups of the tests are made at.
func backupClock() time.Time { return time.Date(2026, 10, 5, 21, 30, 0, 0, time.UTC) }

// wantAppError checks that err is an *Error with that code, a refusal or
// not, and returns it.
func wantAppError(t *testing.T, where string, err error, code string, refused bool) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("%s: error %v, want an *Error %s", where, err, code)
	}
	if e.Code != code || e.Refusal != refused || e.Advice == "" {
		t.Fatalf("%s: code %s, refusal %v, advice %q (%v); want %s, refusal %v, an advice", where, e.Code, e.Refusal, e.Advice, err, code, refused)
	}
	return e
}

// readBackupManifest reads the manifest of a backup as a strict reader.
func readBackupManifest(t *testing.T, dir string) Manifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, backupManifest))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func fileSHA256(t *testing.T, path string) (string, int64) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), int64(len(b))
}

// userData makes what a backup must keep: a playlist of anna with three
// items, one of them twice, and favorites of anna and bob.
func (w *world) userData() (playlistID string) {
	w.t.Helper()
	tracks := w.fixtureTracks(w.anna)
	svc := w.s.catalog.Load()
	p, err := svc.CreatePlaylist(w.t.Context(), w.anna.id, "Evening", "for the backup")
	if err != nil {
		w.t.Fatal(err)
	}
	if _, _, err := svc.AddPlaylistItems(w.t.Context(), w.anna.id, p.ID, []string{tracks[0].Id, tracks[5].Id, tracks[0].Id}, nil, nil); err != nil {
		w.t.Fatal(err)
	}
	w.setFavorite(http.MethodPut, w.anna, tracks[2].Id)
	w.setFavorite(http.MethodPut, w.anna, tracks[9].Id)
	w.setFavorite(http.MethodPut, w.bob, tracks[2].Id)
	return p.ID
}

// The answers a restore must keep, as the users see them.
func restorePaths(playlistID string) []string {
	return []string{
		"/me", "/playlists", "/playlists/" + playlistID, "/playlists/" + playlistID + "/items?limit=100",
		"/me/favorites/tracks?limit=100", "/artists?limit=100", "/albums?limit=100",
		"/albums/" + albumA, "/search?q=alpha", "/search?q=echo&types=track",
	}
}

// answers reads paths as each credential through handler, for the Host
// host, and returns the bodies, which must be 200 and conform.
func (w *world) answers(handler http.Handler, host string, paths []string, as map[string]credential) []string {
	w.t.Helper()
	var out []string
	for _, name := range []string{"admin", "anna", "bob"} {
		for _, path := range append(slices.Clip(paths), "/admin/users?limit=100") {
			if path == "/admin/users?limit=100" && name != "admin" {
				continue
			}
			req := w.request(http.MethodGet, path, nil)
			req.Host = host
			as[name](req)
			rec := send(handler, req)
			assertConforms(w.t, path, req, rec)
			// The playlist is private to anna: the others are answered 404.
			if name == "anna" || !strings.HasPrefix(path, "/playlists/") {
				wantStatus(w.t, name+" GET "+path, rec, http.StatusOK)
			}
			out = append(out, fmt.Sprintf("%s %s %d %s", name, path, rec.Code, rec.Body))
		}
	}
	return out
}

// DESIGN.md §11.4 and step S21: a backup restored into an empty state
// folder gives a server with the same users, sessions, playlists,
// favorites and ids, which answers the same; the search finds the same
// rows (T6). The manifest names the copy by its SHA-256 and counts it.
func TestBackupAndRestoreKeepEverything(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	playlistID := w.userData()
	as := map[string]credential{"admin": w.as(w.admin), "anna": w.as(w.anna), "bob": w.as(w.bob)}
	paths := restorePaths(playlistID)
	before := w.answers(w.s.http.Handler, w.host, paths, as)

	backups := t.TempDir()
	dest := filepath.Join(backups, "2026-10-05-2130")
	m, err := Backup(t.Context(), w.stateDir, backups, dest, backupClock)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirNames(t, dest); !slices.Equal(got, []string{backupManifest, backupDatabase}) {
		t.Fatalf("the backup has %v", got)
	}
	if got := dirNames(t, backups); !slices.Equal(got, []string{"2026-10-05-2130"}) {
		t.Fatalf("the backup folder has %v: a temporary folder is left", got)
	}
	sum, size := fileSHA256(t, filepath.Join(dest, backupDatabase))
	want := Manifest{AppVersion: buildinfo.Version, SchemaVersion: 2, CreatedAt: "2026-10-05T21:30:00.000Z",
		Database: ManifestDatabase{File: backupDatabase, Size: size, SHA256: sum},
		Counts:   ManifestCounts{Users: 3, Playlists: 1, PlaylistItems: 3, Favorites: 3, Artists: 6, Albums: 6, Tracks: 14}}
	if m != want || readBackupManifest(t, dest) != want {
		t.Fatalf("the manifest is\n%+v, it says\n%+v, want\n%+v", m, readBackupManifest(t, dest), want)
	}
	for _, f := range []string{dest, filepath.Join(dest, backupDatabase), filepath.Join(dest, backupManifest)} {
		fi, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s has mode %v: the backup holds the hashes of the passwords", f, fi.Mode())
		}
	}
	// The copy is sound by the checks of the doctor.
	if findings, err := Inspect(t.Context(), filepath.Join(dest, backupDatabase)); err != nil || len(findings) != 0 {
		t.Fatalf("the copy: %+v, %v", findings, err)
	}

	restored := t.TempDir()
	got, err := Restore(t.Context(), restored, backups, dest)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Restore read the manifest %+v", got)
	}
	if got := dirNames(t, restored); !slices.Equal(got, []string{databaseFile}) {
		t.Fatalf("the state folder has %v", got)
	}
	if s, _ := fileSHA256(t, filepath.Join(restored, databaseFile)); s != sum {
		t.Fatal("the restored database is not the copy of the backup")
	}

	// The server of the restored database, on the same library, serves the
	// same answers to the sessions made before the backup.
	logs := &syncBuffer{}
	ln := listen(t)
	s := mustServer(t, newLogger(logs), apiOrigin, restored, w.musiclib)
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{s: s, logs: logs, addr: ln.Addr().String(), cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- s.run(ctx, ln) }()
	t.Cleanup(cancel)
	if st := waitScanned(t, r); !st.LastScan.OK || st.Albums.Available != 6 {
		t.Fatalf("the first scan of the restored server: %+v", st)
	}
	after := w.answers(s.http.Handler, w.host, paths, as)
	if !slices.Equal(after, before) {
		for i := range before {
			if i < len(after) && after[i] != before[i] {
				t.Errorf("after the restore:\n%s\nbefore:\n%s", redact(after[i]), redact(before[i]))
			}
		}
		t.FailNow()
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}

// writeIndexAndUsers changes the database of w, as the server does, until
// stop is closed: album B leaves and comes back with its full-text rows in
// the same transaction (§10.1), and anna adds and removes a favorite.
func (w *world) writeIndexAndUsers(stop <-chan struct{}, trackID string) (*sync.WaitGroup, *atomic.Int64) {
	var running sync.WaitGroup
	var writes atomic.Int64
	ctx := context.Background()
	toggle := func(available int) error {
		return w.store.WithWriteTx(ctx, func(q *store.Queries) error {
			db := q.Conn()
			for _, stmt := range []string{`UPDATE tracks SET available = ? WHERE album_id = ?`, `UPDATE albums SET available = ? WHERE id = ?`} {
				if _, err := db.ExecContext(ctx, stmt, available, albumB); err != nil {
					return err
				}
			}
			album, err := q.GetIndexedAlbum(ctx, albumB)
			if err != nil {
				return err
			}
			return errors.Join(q.UpdateAlbumCounters(ctx, albumB), search.SyncAlbum(ctx, db, albumB),
				search.SyncArtist(ctx, db, album.Album.ArtistID))
		})
	}
	running.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := toggle(i % 2); err != nil {
				w.t.Error(err)
				return
			}
			writes.Add(1)
		}
	})
	running.Go(func() {
		svc := w.s.catalog.Load()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			var err error
			if i%2 == 0 {
				err = svc.AddFavorite(ctx, w.anna.id, trackID)
			} else {
				err = svc.RemoveFavorite(ctx, w.anna.id, trackID)
			}
			if err != nil {
				w.t.Error(err)
				return
			}
			writes.Add(1)
		}
	})
	return &running, &writes
}

// Step S21: a backup made while the server writes is one committed state:
// the copy passes integrity_check and every check of the doctor, the
// full-text rows agreeing with what they describe.
func TestBackupWhileTheServerWrites(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	trackID := w.fixtureTracks(w.anna)[3].Id
	backups := t.TempDir()
	stop := make(chan struct{})
	running, writes := w.writeIndexAndUsers(stop, trackID)
	var names []string
	for i := range 4 {
		// Some writes between two copies.
		for n := writes.Load(); writes.Load() < n+5; {
			time.Sleep(time.Millisecond)
		}
		name := filepath.Join(backups, "b"+string(rune('0'+i)))
		if _, err := Backup(t.Context(), w.stateDir, backups, name, backupClock); err != nil {
			close(stop)
			running.Wait()
			t.Fatal(err)
		}
		names = append(names, name)
	}
	close(stop)
	running.Wait()
	if writes.Load() < 20 {
		t.Fatalf("only %d writes ran with the backups", writes.Load())
	}
	for _, name := range names {
		if findings, err := Inspect(t.Context(), filepath.Join(name, backupDatabase)); err != nil || len(findings) != 0 {
			t.Fatalf("the copy %s: %+v, %v", filepath.Base(name), findings, err)
		}
		m := readBackupManifest(t, name)
		if sum, size := fileSHA256(t, filepath.Join(name, backupDatabase)); m.Database.SHA256 != sum || m.Database.Size != size {
			t.Fatalf("the manifest of %s does not name its copy", filepath.Base(name))
		}
	}
}

// olderDatabaseIn makes the database of dir as an older Vibrance left it:
// only the first migration.
func olderDatabaseIn(t *testing.T, dir string) {
	t.Helper()
	db, err := sqlOpen(filepath.Join(dir, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, migrations.FS, goose.WithDisableGlobalRegistry(true))
	if err == nil {
		_, err = p.UpTo(t.Context(), 1)
	}
	if err := errors.Join(err, db.Close()); err != nil {
		t.Fatal(err)
	}
}

// sqlOpen opens a database file as another program would: no settings.
func sqlOpen(path string) (*sql.DB, error) { return sql.Open("sqlite", path) }

// execRaw runs SQL of the test on a database file, as another program.
func execRaw(t *testing.T, path, query string, args ...any) {
	t.Helper()
	db, err := sqlOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	_, execErr := db.ExecContext(t.Context(), query, args...)
	if err := errors.Join(execErr, db.Close()); err != nil {
		t.Fatal(err)
	}
}

// Backup refuses before it writes anything: a destination outside the
// backup folder, a name that exists, a parent that does not, a state
// folder without a database, a schema this binary does not read as it is.
func TestBackupRefusals(t *testing.T) {
	state := t.TempDir()
	s, err := store.Open(t.Context(), filepath.Join(state, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	backups := t.TempDir()
	if err := os.Mkdir(filepath.Join(backups, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backups, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	older, newer, empty := t.TempDir(), t.TempDir(), t.TempDir()
	olderDatabaseIn(t, older)
	newerDatabase(t, newer)
	for _, tc := range []struct {
		name, state, dest, code string
	}{
		{"outside", state, "/tmp/backup", CodeBackupOutside},
		{"the folder itself", state, backups, CodeBackupOutside},
		{"dot dot", state, backups + "/../x", CodeBackupOutside},
		{"trailing slash", state, backups + "/x/", CodeBackupOutside},
		{"double slash", state, backups + "//x", CodeBackupOutside},
		{"relative", state, "x", CodeBackupOutside},
		{"a folder that exists", state, backups + "/taken", CodeBackupExists},
		{"a file that exists", state, backups + "/file", CodeBackupExists},
		{"no parent", state, backups + "/missing/x", CodeBackupDestination},
		{"no database", empty, backups + "/x", CodeDatabaseMissing},
		{"an older schema", older, backups + "/x", store.CodeSchemaOld},
		{"a newer schema", newer, backups + "/x", store.CodeSchemaTooNew},
	} {
		_, err := Backup(t.Context(), tc.state, backups, tc.dest, backupClock)
		wantAppError(t, tc.name, err, tc.code, true)
		if got := dirNames(t, backups); !slices.Equal(got, []string{"file", "taken"}) {
			t.Fatalf("%s: the backup folder has %v", tc.name, got)
		}
	}
	if got := dirNames(t, empty); len(got) != 0 {
		t.Fatalf("a refused backup created %v in the state folder", got)
	}
}

// makeBackup makes a backup of a new database with the first admin, and
// returns its folder in backups.
func makeBackup(t *testing.T, backups, name string) string {
	t.Helper()
	w := newWorld(t, apiOrigin)
	dest := filepath.Join(backups, name)
	if _, err := Backup(t.Context(), w.stateDir, backups, dest, backupClock); err != nil {
		t.Fatal(err)
	}
	return dest
}

// writeManifest replaces the manifest of a backup.
func writeManifest(t *testing.T, dir string, edit func(m map[string]any)) {
	t.Helper()
	var m map[string]any
	b, err := os.ReadFile(filepath.Join(dir, backupManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	if b, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, backupManifest), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// copyDir copies a backup to another folder of backups.
func copyDir(t *testing.T, from, to string) string {
	t.Helper()
	if err := os.CopyFS(to, os.DirFS(from)); err != nil {
		t.Fatal(err)
	}
	return to
}

// Restore refuses, writing nothing, a state folder with a database or what
// is left of one, a backup outside the backup folder, a manifest that is
// missing, malformed or that does not describe its copy, a copy that
// changed, and a copy of a newer schema.
func TestRestoreRefusals(t *testing.T) {
	backups := t.TempDir()
	good := makeBackup(t, backups, "good")
	variant := func(name string) string { return copyDir(t, good, filepath.Join(backups, name)) }

	missing := variant("no-manifest")
	if err := os.Remove(filepath.Join(missing, backupManifest)); err != nil {
		t.Fatal(err)
	}
	junk := variant("junk-manifest")
	if err := os.WriteFile(filepath.Join(junk, backupManifest), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	unknown := variant("unknown-key")
	writeManifest(t, unknown, func(m map[string]any) { m["password"] = "x" })
	badSum := variant("bad-sum")
	writeManifest(t, badSum, func(m map[string]any) { m["database"].(map[string]any)["sha256"] = strings.Repeat("0", 64) })
	upperSum := variant("upper-sum")
	writeManifest(t, upperSum, func(m map[string]any) {
		d := m["database"].(map[string]any)
		d["sha256"] = strings.ToUpper(d["sha256"].(string))
	})
	badSize := variant("bad-size")
	writeManifest(t, badSize, func(m map[string]any) { m["database"].(map[string]any)["size"] = 1 })
	badSchema := variant("bad-schema")
	writeManifest(t, badSchema, func(m map[string]any) { m["schema_version"] = 1 })
	badFile := variant("bad-file")
	writeManifest(t, badFile, func(m map[string]any) { m["database"].(map[string]any)["file"] = "../vibrance.db" })
	tampered := variant("tampered")
	b, err := os.ReadFile(filepath.Join(tampered, backupDatabase))
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-1] ^= 1
	if err := os.WriteFile(filepath.Join(tampered, backupDatabase), b, 0o600); err != nil {
		t.Fatal(err)
	}

	// A backup made by a newer Vibrance, whose manifest describes it.
	newerState := t.TempDir()
	newerDatabase(t, newerState)
	newer := filepath.Join(backups, "newer")
	if err := os.Mkdir(newer, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.CopyInto(t.Context(), filepath.Join(newerState, databaseFile), filepath.Join(newer, backupDatabase)); err != nil {
		t.Fatal(err)
	}
	copyManifest(t, good, newer)
	sum, size := fileSHA256(t, filepath.Join(newer, backupDatabase))
	writeManifest(t, newer, func(m map[string]any) {
		m["schema_version"] = 9999
		m["database"] = map[string]any{"file": backupDatabase, "size": size, "sha256": sum}
	})

	withDatabase, withWAL := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(withDatabase, databaseFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(withWAL, databaseFile+"-wal"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, state, from, code string
	}{
		{"a database exists", withDatabase, good, CodeRestoreDatabase},
		{"a write-ahead log is left", withWAL, good, CodeRestoreDatabase},
		{"outside", "", "/tmp/good", CodeRestoreOutside},
		{"dot dot", "", backups + "/../good", CodeRestoreOutside},
		{"no such backup", "", backups + "/nothing", CodeRestoreManifest},
		{"no manifest", "", missing, CodeRestoreManifest},
		{"a manifest that is not JSON", "", junk, CodeRestoreManifest},
		{"an unknown key", "", unknown, CodeRestoreManifest},
		{"a SHA-256 in upper case", "", upperSum, CodeRestoreManifest},
		{"another file", "", badFile, CodeRestoreManifest},
		{"another SHA-256", "", badSum, CodeRestoreHash},
		{"another size", "", badSize, CodeRestoreHash},
		{"a copy that changed", "", tampered, CodeRestoreHash},
		{"another schema", "", badSchema, CodeRestoreManifest},
		{"a newer schema", "", newer, CodeRestoreSchemaTooNew},
	} {
		state := tc.state
		if state == "" {
			state = t.TempDir()
		}
		before := dirNames(t, state)
		_, err := Restore(t.Context(), state, backups, tc.from)
		wantAppError(t, tc.name, err, tc.code, true)
		if got := dirNames(t, state); !slices.Equal(got, before) {
			t.Fatalf("%s: the state folder has %v, it had %v", tc.name, got, before)
		}
	}
	// The good one restores, once.
	state := t.TempDir()
	if _, err := Restore(t.Context(), state, backups, good); err != nil {
		t.Fatal(err)
	}
	_, err = Restore(t.Context(), state, backups, good)
	wantAppError(t, "a second restore", err, CodeRestoreDatabase, true)
}

// copyManifest copies the manifest of one backup into another folder.
func copyManifest(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(from, backupManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(to, backupManifest), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A backup of an older schema is restored as it is, and the server
// migrates it when it starts (I13: only forward).
func TestRestoreAnOlderSchema(t *testing.T) {
	older := t.TempDir()
	olderDatabaseIn(t, older)
	backups := t.TempDir()
	dest := filepath.Join(backups, "older")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.CopyInto(t.Context(), filepath.Join(older, databaseFile), filepath.Join(dest, backupDatabase)); err != nil {
		t.Fatal(err)
	}
	sum, size := fileSHA256(t, filepath.Join(dest, backupDatabase))
	m, err := json.Marshal(Manifest{AppVersion: "0.0.1", SchemaVersion: 1, CreatedAt: "2026-01-01T00:00:00.000Z",
		Database: ManifestDatabase{File: backupDatabase, Size: size, SHA256: sum}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, backupManifest), m, 0o600); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	if _, err := Restore(t.Context(), state, backups, dest); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), filepath.Join(state, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := store.OpenReader(t.Context(), filepath.Join(state, databaseFile), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(r.CheckCurrent(), r.Close()); err != nil {
		t.Fatalf("the server did not migrate the restored database: %v", err)
	}
}

// execOn runs SQL of the test on the database of w, in a write
// transaction: it breaks the database as the server never does.
func (w *world) execOn(query string, args ...any) {
	w.t.Helper()
	err := w.store.WithWriteTx(w.t.Context(), func(q *store.Queries) error {
		_, err := q.Conn().ExecContext(w.t.Context(), query, args...)
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

func findingCodes(findings []Finding) []string {
	var codes []string
	for _, f := range findings {
		codes = append(codes, f.Code)
		if f.Entity == "" || f.Message == "" || f.Advice == "" {
			panic("a finding without its entity, message or advice")
		}
	}
	slices.Sort(codes)
	return slices.Compact(codes)
}

// DESIGN.md §11.4: the doctor finds nothing in a sound database, while the
// server runs; it finds the full-text rows out of step with the index,
// wrong counters and no enabled admin, each with its code, and changes
// nothing.
func TestDoctor(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	w.userData()
	doctor := func() []Finding {
		t.Helper()
		findings, err := Doctor(t.Context(), w.stateDir)
		if err != nil {
			t.Fatal(err)
		}
		return findings
	}
	if got := doctor(); len(got) != 0 {
		t.Fatalf("a sound database: %+v", got)
	}

	// Album B is unavailable, without its full-text rows following: the
	// state that made every search that meets them fail (S17).
	w.unavailable(albumB)
	got := doctor()
	if codes := findingCodes(got); !slices.Equal(codes, []string{CodeDoctorAlbumCounters, CodeDoctorSearchExtra}) {
		t.Fatalf("findings %v: %+v", codes, got)
	}
	extra := 0
	for _, f := range got {
		if f.Code == CodeDoctorSearchExtra {
			extra++
		}
	}
	// The album, its two tracks and its artist, who has no other album.
	if extra != 4 {
		t.Fatalf("%d extra full-text rows, want 4: %+v", extra, got)
	}

	w.execOn(`UPDATE users SET disabled = 1 WHERE role = 'admin'`)
	w.execOn(`DELETE FROM search_tracks WHERE rowid = (SELECT seq FROM tracks WHERE album_id = ? LIMIT 1)`, albumA)
	w.execOn(`UPDATE tracks SET title = 'Renamed' WHERE seq = (SELECT max(seq) FROM tracks WHERE album_id = ?)`, albumA)
	want := []string{CodeDoctorAlbumCounters, CodeDoctorNoAdmin, CodeDoctorSearchExtra, CodeDoctorSearchMissing, CodeDoctorSearchStale}
	if codes := findingCodes(doctor()); !slices.Equal(codes, want) {
		t.Fatalf("findings %v, want %v", codes, want)
	}
	// The doctor wrote nothing: what it found is still there.
	if codes := findingCodes(doctor()); !slices.Equal(codes, want) {
		t.Fatalf("a second doctor: %v", codes)
	}
}

// The doctor finds a broken foreign key and a damaged full-text index.
func TestDoctorFindsDamage(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	path := filepath.Join(w.stateDir, databaseFile)
	execRaw(t, path, `UPDATE search_albums_content SET c0 = 'nothing like it' WHERE id = (SELECT min(id) FROM search_albums_content)`)
	db, err := sqlOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	_, err1 := db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`)
	_, err2 := db.ExecContext(t.Context(), `INSERT INTO favorites (user_id, track_id, created_at) VALUES (?, 'no-such-track', 1)`, w.anna.id)
	if err := errors.Join(err1, err2, db.Close()); err != nil {
		t.Fatal(err)
	}
	got, err := Doctor(t.Context(), w.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{CodeDoctorForeignKey, CodeDoctorIntegrity, CodeDoctorSearchIndex}
	if codes := findingCodes(got); !slices.Equal(codes, want) {
		t.Fatalf("findings %v, want %v: %+v", codes, want, got)
	}
}

// The doctor refuses a state folder without a database, creating none, and
// a schema it does not read as it is.
func TestDoctorRefusals(t *testing.T) {
	empty, older, newer := t.TempDir(), t.TempDir(), t.TempDir()
	olderDatabaseIn(t, older)
	newerDatabase(t, newer)
	for _, tc := range []struct{ name, state, code string }{
		{"no database", empty, CodeDatabaseMissing},
		{"an older schema", older, store.CodeSchemaOld},
		{"a newer schema", newer, store.CodeSchemaTooNew},
	} {
		_, err := Doctor(t.Context(), tc.state)
		wantAppError(t, tc.name, err, tc.code, true)
	}
	if got := dirNames(t, empty); len(got) != 0 {
		t.Fatalf("the doctor created %v", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w := newWorld(t, apiOrigin)
	_, err := Inspect(ctx, filepath.Join(w.stateDir, databaseFile))
	if err == nil {
		t.Fatal("an inspection with a cancelled context succeeded")
	}
}

// A copy that integrity_check finds damaged is not a backup: Backup fails
// (1, not a refusal), the name does not exist, and the temporary folder is
// left for the operator.
func TestBackupOfADamagedDatabase(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	execRaw(t, filepath.Join(w.stateDir, databaseFile),
		`UPDATE search_tracks_content SET c0 = 'nothing like it' WHERE id = (SELECT min(id) FROM search_tracks_content)`)
	backups := t.TempDir()
	_, err := Backup(t.Context(), w.stateDir, backups, filepath.Join(backups, "damaged"), backupClock)
	wantAppError(t, "a damaged database", err, CodeBackupVerify, false)
	names := dirNames(t, backups)
	if len(names) != 1 || !strings.HasPrefix(names[0], backupTemporaryPrefix) || !strings.HasSuffix(names[0], temporarySuffix) {
		t.Fatalf("the backup folder has %v, want only the temporary folder", names)
	}
}
