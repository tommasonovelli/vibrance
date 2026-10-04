package app

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"vibrance/internal/store"
)

// dirNames are the names of the entries of a folder, sorted.
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

func wantEmptyDir(t *testing.T, dir string) {
	t.Helper()
	if names := dirNames(t, dir); len(names) != 0 {
		t.Fatalf("%s holds %q, want nothing", dir, names)
	}
}

// Step 3 of the startup (DESIGN.md §11.2): the database is created in the
// state folder and migrated before the server is ready, and it stays open
// while the server runs. The stop closes it after the HTTP server, leaving
// one complete file.
func TestStartupOpensTheDatabase(t *testing.T) {
	r := startServer(t, nil)
	waitReady(t, "http://"+r.addr)
	dir := r.s.stateDir
	path := filepath.Join(dir, databaseFile)

	if names := dirNames(t, dir); !slices.Contains(names, databaseFile) || slices.Contains(names, writeProbe) {
		t.Fatalf("the state folder holds %q while the server runs", names)
	}
	// Another process, as a subcommand will be, finds the schema there and
	// can write while the server runs.
	ctx := t.Context()
	command, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.WithWriteTx(ctx, func(q *store.Queries) error {
		return q.SetMeta(ctx, store.SetMetaParams{Key: "ffmpeg_version", Value: "8.1.3-musiclib1"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := command.Close(); err != nil {
		t.Fatal(err)
	}

	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	if got := dirNames(t, dir); !slices.Equal(got, []string{databaseFile}) {
		t.Fatalf("the state folder holds %q after the stop, want only the database", got)
	}
	msgs := r.logs.messages(t)
	if i, j := slices.Index(msgs, "http server stopped"), slices.Index(msgs, "database closed"); i < 0 || j < i {
		t.Fatalf("log events %q: the database must be closed after the HTTP server stopped", msgs)
	}
	// Closed means closed: the server's store refuses to be used.
	if err := r.s.store.Read(ctx, func(*store.Queries) error { return nil }); err == nil {
		t.Fatal("the store is still open after the stop")
	}

	// The file is the database of the next start, with what was written.
	again, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := again.Read(ctx, func(q *store.Queries) (err error) {
		value, err = q.GetMeta(ctx, "ffmpeg_version")
		return err
	}); err != nil || value != "8.1.3-musiclib1" {
		t.Fatalf("meta after the restart: %q, %v", value, err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
}

// A probe file left by a process that was killed in the middle of the
// check is not in the way of the next start, and does not stay.
func TestStartupTakesOverALeftoverProbe(t *testing.T) {
	var dir string
	r := startServer(t, func(s *server) {
		dir = s.stateDir
		if err := os.WriteFile(filepath.Join(dir, writeProbe), []byte("left behind"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	waitReady(t, "http://"+r.addr)
	if names := dirNames(t, dir); slices.Contains(names, writeProbe) {
		t.Fatalf("the state folder still holds the probe: %q", names)
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}

// newerDatabase creates the database in dir as a newer Vibrance would
// have left it.
func newerDatabase(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, databaseFile)
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, execErr := db.ExecContext(t.Context(), `INSERT INTO goose_db_version (version_id, is_applied) VALUES (9999, 1)`)
	if err := errors.Join(execErr, db.Close()); err != nil {
		t.Fatal(err)
	}
}

// A startup that step 3 refuses ends the run with the code of the
// refusal. The server answered on HTTP in the meantime, but it was never
// ready, and it leaves nothing running.
func TestStartupRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, dir string) string // returns the state folder
		code    string
		want    string // substring of the error
	}{
		{
			name:    "the state folder does not exist",
			prepare: func(_ *testing.T, dir string) string { return filepath.Join(dir, "missing") },
			code:    CodeStateUnwritable,
			want:    "is not writable",
		},
		{
			name: "the state folder is read-only",
			prepare: func(t *testing.T, dir string) string {
				if err := os.Chmod(dir, 0o555); err != nil {
					t.Fatal(err)
				}
				// So that the test can remove it.
				t.Cleanup(func() {
					if err := os.Chmod(dir, 0o755); err != nil {
						t.Error(err)
					}
				})
				return dir
			},
			code: CodeStateUnwritable,
			want: "VIBRANCE_UID",
		},
		{
			name: "the database is from a newer version",
			prepare: func(t *testing.T, dir string) string {
				newerDatabase(t, dir)
				return dir
			},
			code: store.CodeSchemaTooNew,
			want: "opening the database: the database has schema version 9999",
		},
		{
			name: "the database file is not a database",
			prepare: func(t *testing.T, dir string) string {
				if err := os.WriteFile(filepath.Join(dir, databaseFile), []byte(strings.Repeat("garbage\n", 1000)), 0o644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			code: store.CodeOpen,
			want: "opening the database: cannot open the database file",
		},
		{
			name: "a migration cannot be applied",
			prepare: func(t *testing.T, dir string) string {
				db, err := sql.Open("sqlite", filepath.Join(dir, databaseFile))
				if err != nil {
					t.Fatal(err)
				}
				_, execErr := db.ExecContext(t.Context(), `CREATE TABLE users (x integer)`)
				if err := errors.Join(execErr, db.Close()); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			code: store.CodeMigrate,
			want: "opening the database: cannot apply the migrations",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := tc.prepare(t, t.TempDir())
			logs := &syncBuffer{}
			s := mustServer(t, newLogger(logs), testOrigin, stateDir, t.TempDir())
			ln := listen(t)

			done := make(chan error, 1)
			go func() { done <- s.run(t.Context(), ln) }()
			var err error
			select {
			case err = <-done:
			case <-time.After(30 * time.Second):
				t.Fatalf("the refused startup did not end the run within 30s; logs:\n%s", logs)
			}
			var ae *Error
			if !errors.As(err, &ae) || ae.Code != tc.code || Code(err) != tc.code {
				t.Fatalf("run returned %v (code %q), want code %q", err, Code(err), tc.code)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the error does not contain %q: %v", tc.want, err)
			}

			msgs := logs.messages(t)
			if !slices.Equal(msgs, []string{"http listening", "http server stopped"}) {
				t.Fatalf("log events %q, want the HTTP server started and stopped and nothing else", msgs)
			}
			if got := s.state.Load(); got != stateStopping {
				t.Fatalf("state %d, want stopping", got)
			}
			wantJSON(t, serveDirectly(s, "/health/ready"), 503, shuttingDownJSON)
			if conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err == nil {
				closeOnce(t, conn)
				t.Fatal("the server still accepts connections after the refused startup")
			}
			if s.store != nil {
				t.Fatal("the server kept a store it could not open")
			}
		})
	}
}

// A stop asked for during the startup interrupts step 3. It is a stop like
// any other, not a failure: the run ends without an error, and the server
// was never ready.
func TestStopDuringTheStartup(t *testing.T) {
	logs := &syncBuffer{}
	dir := t.TempDir()
	s := mustServer(t, newLogger(logs), testOrigin, dir, t.TempDir())
	ln := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := s.run(ctx, ln); err != nil {
		t.Fatalf("run returned %v, want a normal stop", err)
	}
	if got := logs.messages(t); !slices.Equal(got, []string{"http listening", "stopping", "http server stopped"}) {
		t.Fatalf("log events %q", got)
	}
	stopping := eventsOf(t, logs, "stopping")[0]
	if interrupted, _ := stopping["interrupted"].(string); stopping["level"] != "INFO" || !strings.Contains(interrupted, "context canceled") {
		t.Fatalf("the stopping event does not say what was interrupted: %v", stopping)
	}
	if s.store != nil {
		t.Fatal("the server kept a store")
	}
	// What a refused startup leaves is what the next one starts from.
	r := startServer(t, func(s *server) { s.stateDir = dir })
	waitReady(t, "http://"+r.addr)
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}

// The code of a run that failed is that of its first failure, whatever
// failed after it while stopping.
func TestCodeOfTheFirstFailure(t *testing.T) {
	startup := &Error{Code: store.CodeSchemaTooNew, Msg: "opening the database", Err: &store.Error{Code: store.CodeSchemaTooNew, Msg: "too new"}}
	stop := &Error{Code: CodeHTTPListen, Msg: "closing the HTTP server", Err: errors.New("x")}
	if got := Code(errors.Join(startup, stop)); got != store.CodeSchemaTooNew {
		t.Fatalf("Code = %q, want %q", got, store.CodeSchemaTooNew)
	}
	if got := Code(errors.Join(stop, &Error{Code: store.CodeClose, Msg: "closing the database"})); got != CodeHTTPListen {
		t.Fatalf("Code = %q, want %q", got, CodeHTTPListen)
	}
}
