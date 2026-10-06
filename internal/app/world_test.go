package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"vibrance/internal/api"
	"vibrance/internal/auth"
	"vibrance/internal/catalog"
	"vibrance/internal/covers"
	"vibrance/internal/httpx"
	"vibrance/internal/library"
	"vibrance/internal/store"
)

// account is an account of a world, with its password.
type account struct {
	name, password, id string
}

// world is a server that is not started, as net/http would call it, with
// its service of the sessions and its catalog published on a database of
// its own and three accounts: the first admin and two users, anna and bob.
// The index is empty until the test fills it. Its log and that
// of the service are in logs. It is ready: the health says so.
type world struct {
	t        *testing.T
	s        *server
	host     string
	sessions *auth.Service
	// store is the database of the world, with its index; stateDir is the
	// folder of its file.
	store    *store.Store
	stateDir string
	logs     *syncBuffer
	admin    *account
	anna     *account
	bob      *account
	// accounts counts the accounts newAccount made.
	accounts int
	// musiclib is the folder of MusicLib the files are served from: one
	// without library/ until indexFixture gives it a copy of the fixture
	// library. rescans are the albums the operations asked the scanner to
	// look at again.
	musiclib string
	rescans  *rechecks
	// indexed is whether indexFixture ran.
	indexed bool
}

// newWorld makes a world whose public origin is origin.
func newWorld(t *testing.T, origin string) *world {
	t.Helper()
	return newWorldAt(t, origin, time.Now)
}

// newWorldAt is newWorld with the clock of the sessions: now says when a
// session is made, used and over.
func newWorldAt(t *testing.T, origin string, now func() time.Time) *world {
	t.Helper()
	logs := &syncBuffer{}
	s := mustServer(t, newLogger(logs), origin, t.TempDir(), t.TempDir())
	u, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	st, err := store.Open(t.Context(), filepath.Join(stateDir, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	sessions, err := auth.NewService(st, testCost, now, newLogger(logs))
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Bootstrap(t.Context(), env(adminEnv)); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, s: s, host: u.Host, sessions: sessions, store: st, stateDir: stateDir, logs: logs}
	users, err := sessions.ListUsers(t.Context())
	if err != nil || len(users) != 1 {
		t.Fatalf("the first admin: %v, %v", users, err)
	}
	w.admin = &account{name: adminName, password: adminPassword, id: users[0].ID}
	w.anna = w.newNamedAccount("anna", "the password of anna", auth.RoleUser)
	w.bob = w.newNamedAccount("bob", "the password of bob", auth.RoleUser)
	w.musiclib = t.TempDir()
	w.rescans = publishMedia(t, s, st, w.musiclib)
	s.catalog.Store(catalog.New(st, time.Now))
	s.sessions.Store(sessions)
	s.state.Store(stateReady)
	return w
}

func (w *world) newNamedAccount(name, password, role string) *account {
	w.t.Helper()
	u, err := w.sessions.CreateUser(w.t.Context(), name, password, role)
	if err != nil {
		w.t.Fatal(err)
	}
	return &account{name: name, password: password, id: u.ID}
}

// newAccount makes another account of the role, with a name of its own.
func (w *world) newAccount(role string) *account {
	w.t.Helper()
	w.accounts++
	return w.newNamedAccount(fmt.Sprintf("user%d", w.accounts), fmt.Sprintf("the password of user %d", w.accounts), role)
}

// token signs a in with a new session of kind token.
func (w *world) token(a *account) auth.SignIn {
	w.t.Helper()
	in, err := w.sessions.CreateToken(w.t.Context(), a.name, a.password, "tests", "")
	if err != nil {
		w.t.Fatalf("signing in as %s: %v", a.name, err)
	}
	return in
}

// cookie signs a in with a new session of kind cookie.
func (w *world) cookie(a *account) auth.SignIn {
	w.t.Helper()
	in, err := w.sessions.Login(w.t.Context(), a.name, a.password, "", "")
	if err != nil {
		w.t.Fatalf("signing in as %s: %v", a.name, err)
	}
	return in
}

// credential is how a request presents a session; nobody presents none.
type credential func(*http.Request)

func nobody(*http.Request) {}

// bearer presents a session of kind token.
func bearer(token string) credential {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// sessionCookie presents a session of kind cookie.
func sessionCookie(token string) credential {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token}) }
}

// as presents a new session of kind token of a; nobody when a is nil.
func (w *world) as(a *account) credential {
	if a == nil {
		return nobody
	}
	return bearer(w.token(a).Token)
}

// request is a request to an operation under /api/v1, as a client of the
// server sends it: the right Host, X-Vibrance-Request with a method that
// is not GET or HEAD, and body, when not nil, as JSON.
func (w *world) request(method, path string, body any) *http.Request {
	w.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			w.t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, api.BasePath+path, reader)
	req.Host = w.host
	req.RemoteAddr = "192.0.2.7:4321"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set(httpx.RequestHeader, "1")
	}
	return req
}

// do sends a request with a credential and checks that the answer conforms
// to the specification.
func (w *world) do(method, path string, body any, c credential) *httptest.ResponseRecorder {
	w.t.Helper()
	req := w.request(method, path, body)
	c(req)
	rec := send(w.s.http.Handler, req)
	assertConforms(w.t, method+" "+path, req, rec)
	return rec
}

// wantStatus checks the status of a response that is not an error.
func wantStatus(t *testing.T, where string, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("%s: status %d, want %d (%s)", where, rec.Code, status, redacted(rec))
	}
}

// decode reads the JSON body of a response as T, strictly: a key T does not
// know fails the test.
func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	var v T
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("the body is not a %T: %s (%s)", v, redact(err.Error()), redacted(rec))
	}
	return v
}

// publishMedia gives s, which is not started, the files of the library in
// musiclib on the index of st, as the startup would publish them, and
// returns the albums the operations ask the scanner to look at again. The
// cache of the thumbnails is a folder of the test. It also publishes a
// scanner of that library which nobody runs: its state is the one of a
// scanner before its first cycle, and a cycle asked of it waits. The tests
// of the state of the library start a server, with a scanner that runs.
func publishMedia(t *testing.T, s *server, st *store.Store, musiclib string) *rechecks {
	t.Helper()
	root, err := library.OpenRoot(musiclib)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	rescans := &rechecks{}
	s.media.Store(&api.Media{Files: catalog.NewFiles(root, st, s.log), Covers: covers.New(root, st, t.TempDir(), s.log),
		Recheck: rescans.add})
	s.scanner.Store(library.NewScanner(library.NewIndexer(root, st, nil, library.NoCoverWarmer{}, time.Now), testWorkers,
		testScanInterval, s.log))
	return rescans
}

// rechecks are the ids of the albums the operations of the files asked the
// scanner to index again, in order.
type rechecks struct {
	mu  sync.Mutex
	ids []string
}

func (r *rechecks) add(albumID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, albumID)
}

// Load is how many were asked for.
func (r *rechecks) Load() int32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int32(len(r.ids))
}

// last is the album of the last one, "" before the first.
func (r *rechecks) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.ids) == 0 {
		return ""
	}
	return r.ids[len(r.ids)-1]
}
