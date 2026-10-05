package app

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"vibrance/internal/api"
	"vibrance/internal/auth"
)

// Until the startup is complete the operations answer 503 not_ready, the
// public ones too: the service of the sessions needs the database. Once the
// stop has begun, a server that never published it answers shutting_down.
func TestOperationsWaitForTheStartup(t *testing.T) {
	_, ops := specOperations(t)
	logs := &syncBuffer{}
	s := mustServer(t, newLogger(logs), apiOrigin, t.TempDir(), t.TempDir())
	for _, state := range []struct {
		state int32
		code  string
	}{{stateStarting, "not_ready"}, {stateStopping, "shutting_down"}} {
		s.state.Store(state.state)
		for _, o := range ops {
			req := o.example(t)
			req.Header.Set("Authorization", "Bearer vb_"+strings.Repeat("A", 43))
			rec := send(s.http.Handler, req)
			where := o.op.OperationID + " (" + state.code + ")"
			wantCode(t, where, rec, http.StatusServiceUnavailable, state.code)
			assertConforms(t, where, req, rec)
		}
	}
	// A request the boundary refuses is refused before: the 503 is not a
	// way around it.
	s.state.Store(stateStarting)
	req := ops[0].example(t)
	req.Host = "evil.example"
	wantCode(t, "another host", send(s.http.Handler, req), http.StatusMisdirectedRequest, "host_not_allowed")
}

// The authentication on a server that runs, through a real connection: the
// first admin of the startup signs in, a request without a session is 401
// on every operation that is not public, a user is 403 on the operations
// for admins, and the access log has the id of the user (§11.5).
func TestAuthenticationOverTheNetwork(t *testing.T) {
	doc, ops := specOperations(t)
	r := startServer(t, nil)
	base := "http://" + r.addr
	waitReady(t, base)
	sessions := r.s.sessions.Load()
	if sessions == nil {
		t.Fatal("the server is ready and has no service of the sessions")
	}
	first, err := sessions.CreateToken(t.Context(), adminName, adminPassword, "tests", "192.0.2.1:1")
	if err != nil {
		t.Fatalf("the first admin of the startup cannot sign in: %v", err)
	}
	if first.User.Role != auth.RoleAdmin {
		t.Fatalf("the first admin is a %s", first.User.Role)
	}
	const annaPassword = "the password of anna"
	anna, err := sessions.CreateUser(t.Context(), "anna", annaPassword, auth.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	// Each request has a session of its own: an operation may end the one
	// it is asked with (a sign-out).
	var tokens []string
	adminSession := func() string {
		in, err := sessions.CreateToken(t.Context(), adminName, adminPassword, "tests", "")
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, in.Token)
		return in.Token
	}
	userSession := func() string {
		in, err := sessions.Login(t.Context(), "anna", annaPassword, "", "")
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, in.Token)
		return in.Token
	}

	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	ask := func(o operation, credentials map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		example := o.example(t)
		req, err := http.NewRequestWithContext(t.Context(), example.Method, base+example.URL.RequestURI(), example.Body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header = example.Header.Clone()
		req.Host = r.addr
		for k, v := range credentials {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		rec.Code = resp.StatusCode
		for k, v := range resp.Header {
			rec.Header()[k] = v
		}
		_, rerr := io.Copy(rec.Body, resp.Body)
		if err := errors.Join(rerr, resp.Body.Close()); err != nil {
			t.Fatal(err)
		}
		assertConforms(t, o.op.OperationID, example, rec)
		return rec
	}

	access := api.Access(doc)
	for _, o := range ops {
		needs := access[o.method+" "+api.BasePath+o.path]
		where := o.op.OperationID
		// Nobody.
		if rec := ask(o, nil); needs == auth.Public {
			wantReached(t, where+" without a session", rec)
		} else {
			wantCode(t, where+" without a session", rec, http.StatusUnauthorized, auth.CodeLoginRequired)
		}
		// A user, with a cookie.
		rec := ask(o, map[string]string{"Cookie": auth.CookieName + "=" + userSession()})
		if needs == auth.AdminOnly {
			wantCode(t, where+" as a user", rec, http.StatusForbidden, auth.CodeForbidden)
		} else {
			wantReached(t, where+" as a user", rec)
		}
		// The admin, with a bearer token, and with a cookie of the user
		// that the bearer token wins over.
		rec = ask(o, map[string]string{"Authorization": "Bearer " + adminSession(), "Cookie": auth.CookieName + "=" + userSession()})
		wantReached(t, where+" as the admin", rec)
	}

	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	// A public operation does not look at the credentials: it has no user.
	authenticated := 0
	for _, needs := range access {
		if needs != auth.Public {
			authenticated++
		}
	}
	users := map[string]int{}
	for _, ev := range eventsOf(t, r.logs, "request") {
		if id, ok := ev["user_id"].(string); ok {
			users[id]++
		}
	}
	if authenticated != len(ops)-3 || users[first.User.ID] != authenticated || users[anna.ID] != authenticated || len(users) != 2 {
		t.Fatalf("the access log names the users %v, want each of the two on %d requests", users, authenticated)
	}
	text := r.logs.String()
	for i, secret := range append(tokens, first.Token, adminPassword, annaPassword) {
		if strings.Contains(text, secret) {
			t.Fatalf("the log holds the token or password #%d", i)
		}
	}
}

// §11.2 step 5: a database without accounts and no valid password stops the
// startup with the code of the refusal; the database is closed, and nothing
// is served.
func TestStartupRefusesWithoutTheFirstAdmin(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		code string
	}{
		"no password":     {nil, auth.CodeAdminPasswordMissing},
		"a short one":     {map[string]string{"VIBRANCE_ADMIN_PASSWORD": "short"}, auth.CodeAdminPasswordInvalid},
		"an invalid name": {map[string]string{"VIBRANCE_ADMIN_USERNAME": "-x", "VIBRANCE_ADMIN_PASSWORD": adminPassword}, auth.CodeAdminUsernameInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			r := startServer(t, func(s *server) { s.getenv = env(tc.env) })
			err := r.wait(t)
			if Code(err) != tc.code {
				t.Fatalf("run returned %v (code %q), want code %q", err, Code(err), tc.code)
			}
			if r.s.sessions.Load() != nil || r.s.scanner != nil {
				t.Fatal("a refused startup published its services")
			}
			want := []string{"http listening", "database open", "media tools verified", "http server stopped", "database closed"}
			if msgs := r.logs.messages(t); !slices.Equal(msgs, want) {
				t.Fatalf("log events %q, want %q", msgs, want)
			}
		})
	}
}

// §11.2 step 6: the sessions that expired while the server was stopped are
// deleted before it serves; the live ones stay.
func TestStartupDeletesTheExpiredSessions(t *testing.T) {
	stateDir := t.TempDir()
	first := startServerIn(t, stateDir)
	waitReady(t, "http://"+first.addr)
	sessions := first.s.sessions.Load()
	live, err := sessions.CreateToken(t.Context(), adminName, adminPassword, "phone", "")
	if err != nil {
		t.Fatal(err)
	}
	dead, err := sessions.Login(t.Context(), adminName, adminPassword, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.stop(t); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(stateDir, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	_, execErr := db.ExecContext(t.Context(), `UPDATE sessions SET expires_at = 1 WHERE id = ?`, dead.Session.ID)
	if err := errors.Join(execErr, db.Close()); err != nil {
		t.Fatal(err)
	}

	again := startServerIn(t, stateDir)
	waitReady(t, "http://"+again.addr)
	if err := again.stop(t); err != nil {
		t.Fatal(err)
	}
	if ev := eventsOf(t, again.logs, "expired sessions deleted"); len(ev) != 1 || ev[0]["sessions"] != float64(1) {
		t.Fatalf("the startup logged %v, want one expired session deleted", ev)
	}
	if slices.Contains(again.logs.messages(t), "first admin created") {
		t.Fatal("the second start created an admin again")
	}
	db, err = sql.Open("sqlite", filepath.Join(stateDir, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	rows, err := db.QueryContext(t.Context(), `SELECT id FROM sessions`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close(), db.Close()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []string{live.Session.ID}) {
		t.Fatalf("the sessions after the start are %v, want only the live one", ids)
	}
}

// startServerIn is startServer on a given state folder.
func startServerIn(t *testing.T, stateDir string) *running {
	t.Helper()
	logs := &syncBuffer{}
	ln := listen(t)
	s := mustServer(t, newLogger(logs), "http://"+ln.Addr().String(), stateDir, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{s: s, logs: logs, addr: ln.Addr().String(), cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- s.run(ctx, ln) }()
	t.Cleanup(cancel)
	return r
}

// The middleware the server gives the router is the one of the service it
// published, built once: requests at the same time all see a service, and
// the access log is not confused between them.
func TestAuthenticatedConcurrently(t *testing.T) {
	logs := &syncBuffer{}
	s := mustServer(t, newLogger(logs), apiOrigin, t.TempDir(), t.TempDir())
	token := adminToken(t, publishSessions(t, s))
	_, ops := specOperations(t)
	done := make(chan struct{})
	for i := range 40 {
		go func() {
			defer func() { done <- struct{}{} }()
			req := ops[i%len(ops)].example(t)
			if i%2 == 0 {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			rec := send(s.http.Handler, req)
			if rec.Code >= 500 && rec.Code != http.StatusNotImplemented {
				t.Errorf("a concurrent request answered %d: %s", rec.Code, redacted(rec))
			}
		}()
	}
	for range 40 {
		<-done
	}
}
