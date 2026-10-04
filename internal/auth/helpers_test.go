package auth

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// testCost is argon2id as in production, with little memory and one pass:
// the tests run hundreds of hashes. One test alone uses ProductionCost.
var testCost = Cost{MemoryKiB: 64, Time: 1, Threads: 1}

// The passwords of the tests. They protect nothing.
const (
	alicePassword = "alice: correct horse battery"
	bobPassword   = "bob: staple tree window sand"
	otherPassword = "another password, also long"
)

// syncBuffer is an io.Writer safe for the concurrent writes of a logger and
// the reads of a test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// events decodes the log, one JSON object per line.
func (s *syncBuffer) events(t *testing.T) []map[string]any {
	t.Helper()
	var events []map[string]any
	for line := range strings.Lines(s.String()) {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		events = append(events, ev)
	}
	return events
}

// clock is the clock of a test: it moves only when the test moves it.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fixture is a Service on a real database in a temporary folder.
type fixture struct {
	*Service
	store *store.Store
	path  string
	clock *clock
	logs  *syncBuffer
	// pauses are the delays the Service asked for after a refusal. The
	// fixture does not wait them.
	mu     sync.Mutex
	pauses []time.Duration
}

func (f *fixture) paused() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.pauses...)
}

// newFixture opens a new database and a Service on it, with the cost and
// the clock of the tests. The delay of a refusal is recorded, not waited.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vibrance.db")
	st, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	f := &fixture{store: st, path: path, clock: newClock(), logs: &syncBuffer{}}
	f.Service = f.service(t)
	return f
}

// service is another Service on the database of the fixture: another
// process, such as `vibrance user`, as far as the database is concerned.
func (f *fixture) service(t *testing.T) *Service {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s, err := NewService(f.store, testCost, f.clock.Now, log)
	if err != nil {
		t.Fatal(err)
	}
	s.pause = func(d time.Duration) {
		// A refusal waits inside the slot.
		if len(s.slot) != 1 {
			t.Error("a refusal paused outside the slot")
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.pauses = append(f.pauses, d)
	}
	return s
}

// user creates an account.
func (f *fixture) user(t *testing.T, username, password, role string) User {
	t.Helper()
	u, err := f.CreateUser(t.Context(), username, password, role)
	if err != nil {
		t.Fatalf("creating the account %s: %v", username, err)
	}
	return u
}

// login signs in with a cookie session, and fails the test if it is
// refused. The token is in the result: a test never prints it.
func (f *fixture) login(t *testing.T, username, password string) SignIn {
	t.Helper()
	in, err := f.Login(t.Context(), username, password, "", "192.0.2.1:5000")
	if err != nil {
		t.Fatalf("signing in as %s: %v", username, err)
	}
	return in
}

// token signs in with a session of kind token, and fails the test if it is
// refused.
func (f *fixture) token(t *testing.T, username, password string) SignIn {
	t.Helper()
	in, err := f.CreateToken(t.Context(), username, password, "phone", "192.0.2.1:5000")
	if err != nil {
		t.Fatalf("creating a token for %s: %v", username, err)
	}
	return in
}

// principal authenticates the token of a sign-in, presented as the kind of
// its session, and fails the test if it is refused.
func (f *fixture) principal(t *testing.T, in SignIn) Principal {
	t.Helper()
	p, err := f.Authenticate(t.Context(), in.Session.Kind, in.Token)
	if err != nil {
		t.Fatalf("authenticating a session: %v", err)
	}
	return p
}

// exec runs a statement of the test on the database, on a connection of
// its own: what an admin of S14, or another process, would do.
func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	_, execErr := db.ExecContext(t.Context(), query, args...)
	if err := errors.Join(execErr, db.Close()); err != nil {
		t.Fatal(err)
	}
}

// sessionRow is a row of sessions, as the database has it.
type sessionRow struct {
	ID, TokenHash, UserID, Kind string
	DeviceName                  sql.NullString
	CreatedAt, LastUsedAt       int64
	ExpiresAt                   int64
}

// sessions reads the table of the sessions, by id.
func (f *fixture) sessions(t *testing.T) []sessionRow {
	t.Helper()
	db, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	rows, err := db.QueryContext(t.Context(),
		`SELECT id, token_hash, user_id, kind, device_name, created_at, last_used_at, expires_at FROM sessions ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var out []sessionRow
	for rows.Next() {
		var r sessionRow
		if err := rows.Scan(&r.ID, &r.TokenHash, &r.UserID, &r.Kind, &r.DeviceName, &r.CreatedAt, &r.LastUsedAt, &r.ExpiresAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return out
}

// session returns the row of one session.
func (f *fixture) session(t *testing.T, id string) sessionRow {
	t.Helper()
	for _, r := range f.sessions(t) {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("the session %s is not in the database", id)
	return sessionRow{}
}

// passwordHash reads the stored hash of an account.
func (f *fixture) passwordHash(t *testing.T, username string) string {
	t.Helper()
	db, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	scanErr := db.QueryRowContext(t.Context(), `SELECT password_hash FROM users WHERE username = ?`, username).Scan(&hash)
	if err := errors.Join(scanErr, db.Close()); err != nil {
		t.Fatal(err)
	}
	return hash
}

// databaseBytes is every byte of the database on disk: the file, its
// write-ahead log and its shared memory.
func (f *fixture) databaseBytes(t *testing.T) []byte {
	t.Helper()
	var all []byte
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(f.path + suffix)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		all = append(all, b...)
	}
	return all
}

// wantRefusal checks that err is the refusal of the API with that status
// and code, and returns it.
func wantRefusal(t *testing.T, err error, status int, code string) *httpx.Error {
	t.Helper()
	var e *httpx.Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v, want the refusal %d %s", err, status, code)
	}
	if e.Status != status || e.Code != code {
		t.Fatalf("refusal %d %s, want %d %s", e.Status, e.Code, status, code)
	}
	return e
}

// refusalCode is the code of a refusal of the API, or "" for another error.
func refusalCode(err error) string {
	var e *httpx.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// query reads one row with a statement of the test.
func (f *fixture) query(t *testing.T, query string, dest ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	scanErr := db.QueryRowContext(t.Context(), query).Scan(dest...)
	if err := errors.Join(scanErr, db.Close()); err != nil {
		t.Fatal(err)
	}
}
