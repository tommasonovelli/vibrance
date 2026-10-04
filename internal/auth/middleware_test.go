package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"vibrance/internal/httpx"
)

// served is what the operation behind the middleware saw.
type served struct {
	mu        sync.Mutex
	calls     int
	principal Principal
	found     bool
}

func (s *served) last() (int, Principal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.principal, s.found
}

// The routes of the tests: one of each access, and one the access rules do
// not know.
const (
	publicRoute  = "GET /public"
	userRoute    = "GET /user"
	writeRoute   = "POST /user"
	adminRoute   = "GET /admin/users"
	unknownRoute = "GET /forgotten"
)

// guarded is a router whose routes are behind the middleware of f, with the
// access log of the server around it, and what its operations saw.
func guarded(t *testing.T, f *fixture) (http.Handler, *served) {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	seen := &served{}
	operation := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.mu.Lock()
		seen.calls++
		seen.principal, seen.found = PrincipalOf(r.Context())
		seen.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mw := f.Middleware(map[string]Access{publicRoute: Public, userRoute: Authenticated, writeRoute: Authenticated, adminRoute: AdminOnly}, log)
	mux := http.NewServeMux()
	for _, route := range []string{publicRoute, userRoute, writeRoute, adminRoute, unknownRoute} {
		mux.Handle(route, mw(operation))
	}
	return httpx.AccessLog(log, nil)(mux), seen
}

// ask sends a request with the given headers and returns the status and
// the code of the error, if the answer is one.
func ask(t *testing.T, h http.Handler, route string, header http.Header) (int, string) {
	t.Helper()
	method, path, _ := strings.Cut(route, " ")
	req := httptest.NewRequest(method, path, nil)
	for k, values := range header {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusNoContent {
		return rec.Code, ""
	}
	var body struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Message == "" || body.Details == nil {
		t.Fatalf("%s: the answer %d is not in the error model: %v", route, rec.Code, err)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("%s: the middleware set a cookie", route)
	}
	return rec.Code, body.Code
}

func bearerOf(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

func cookieOf(tokens ...string) http.Header {
	h := http.Header{}
	for _, token := range tokens {
		h.Add("Cookie", CookieName+"="+token)
	}
	return h
}

// The middleware finds the session in the cookie or in the bearer token,
// puts the Principal in the context, and answers 401 login_required
// without one. A public operation needs none.
func TestMiddleware(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", alicePassword, RoleUser)
	cookie := f.login(t, "alice", alicePassword)
	token, err := f.CreateToken(t.Context(), "alice", alicePassword, "phone", "")
	if err != nil {
		t.Fatal(err)
	}
	h, seen := guarded(t, f)

	for _, tc := range []struct {
		name    string
		header  http.Header
		session string // "" when the request must be refused
	}{
		{"no credentials", nil, ""},
		{"the cookie", cookieOf(cookie.Token), cookie.Session.ID},
		{"the bearer token", bearerOf(token.Token), token.Session.ID},
		// A session is used only the way it was made (NOTES.md N-099).
		{"a cookie session as a bearer token", bearerOf(cookie.Token), ""},
		{"a token session as a cookie", cookieOf(token.Token), ""},
		{"the scheme in lower case", http.Header{"Authorization": {"bearer " + token.Token}}, token.Session.ID},
		{"the scheme in upper case", http.Header{"Authorization": {"BEARER " + token.Token}}, token.Session.ID},
		{"a cookie among others", http.Header{"Cookie": {"theme=dark; " + CookieName + "=" + cookie.Token + "; musiclib_session=abc"}}, cookie.Session.ID},
		{"a cookie that is no session", cookieOf("vb_" + strings.Repeat("A", 43)), ""},
		{"an empty cookie", cookieOf(""), ""},
		{"a token that is no session", bearerOf("vb_" + strings.Repeat("A", 43)), ""},
		{"an empty bearer token", http.Header{"Authorization": {"Bearer "}}, ""},
		{"the scheme alone", http.Header{"Authorization": {"Bearer"}}, ""},
		{"two spaces after the scheme", http.Header{"Authorization": {"Bearer  " + token.Token}}, ""},
		{"a token without a scheme", http.Header{"Authorization": {token.Token}}, ""},
		{"basic credentials", http.Header{"Authorization": {"Basic YWxpY2U6cGFzc3dvcmQ="}}, ""},
		{"the cookie of MusicLib", http.Header{"Cookie": {"musiclib_session=" + cookie.Token}}, ""},
		{"a cookie with another case", http.Header{"Cookie": {"Vibrance_Session=" + cookie.Token}}, ""},
		{"the token in the query", nil, ""},
		{"the token in another header", http.Header{"X-Auth-Token": {token.Token}, "X-Vibrance-Session": {cookie.Token}}, ""},
		// A second cookie of the same name, set by another site of a
		// parent domain: the one that is a session counts, in either order.
		{"a stale cookie before the session", cookieOf("vb_"+strings.Repeat("B", 43), cookie.Token), cookie.Session.ID},
		{"a stale cookie after the session", cookieOf(cookie.Token, "garbage"), cookie.Session.ID},
		{"two stale cookies", cookieOf("vb_"+strings.Repeat("B", 43), "garbage"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, route := range []string{userRoute, writeRoute} {
				target := route
				if tc.name == "the token in the query" {
					target += "?access_token=" + token.Token + "&" + CookieName + "=" + cookie.Token
				}
				before, _, _ := seen.last()
				status, code := ask(t, h, target, tc.header)
				calls, p, found := seen.last()
				if tc.session == "" {
					if status != http.StatusUnauthorized || code != CodeLoginRequired {
						t.Fatalf("%s: %d %s, want 401 login_required", route, status, code)
					}
					if calls != before {
						t.Fatalf("%s: the operation ran without a session", route)
					}
					continue
				}
				if status != http.StatusNoContent || calls != before+1 {
					t.Fatalf("%s: %d %s, want the operation", route, status, code)
				}
				if want := (Principal{UserID: alice.ID, Role: RoleUser, SessionID: tc.session}); !found || p != want {
					t.Fatalf("%s: the principal in the context is %+v (%t), want %+v", route, p, found, want)
				}
			}
			// A public operation is served whatever the request carries,
			// and has no principal: its credentials are not looked at.
			before, _, _ := seen.last()
			status, _ := ask(t, h, publicRoute, tc.header)
			calls, _, found := seen.last()
			if status != http.StatusNoContent || calls != before+1 || found {
				t.Fatalf("the public operation: status %d, principal %t", status, found)
			}
		})
	}
}

// §7.3: with a bearer token and a cookie in one request the bearer token
// counts, alone: the cookie is not a second chance.
func TestBearerWinsOverTheCookie(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", alicePassword, RoleUser)
	bob := f.user(t, "bob", bobPassword, RoleAdmin)
	aliceCookie := f.login(t, "alice", alicePassword)
	bobToken, err := f.CreateToken(t.Context(), "bob", bobPassword, "phone", "")
	if err != nil {
		t.Fatal(err)
	}
	aliceToken, err := f.CreateToken(t.Context(), "alice", alicePassword, "phone", "")
	if err != nil {
		t.Fatal(err)
	}
	bobCookie := f.login(t, "bob", bobPassword)
	h, seen := guarded(t, f)

	both := func(bearer, cookie string) http.Header {
		h := cookieOf(cookie)
		h.Set("Authorization", "Bearer "+bearer)
		return h
	}
	// The bearer of bob and the cookie of alice: the request is of bob.
	if status, code := ask(t, h, userRoute, both(bobToken.Token, aliceCookie.Token)); status != http.StatusNoContent {
		t.Fatalf("a bearer token and a cookie: %d %s", status, code)
	}
	if _, p, _ := seen.last(); p != (Principal{UserID: bob.ID, Role: RoleAdmin, SessionID: bobToken.Session.ID}) {
		t.Fatalf("the principal is %+v, want the one of the bearer token", p)
	}
	// And the other way round: the request is of alice, who is no admin,
	// whatever the cookie of the admin says.
	if status, code := ask(t, h, adminRoute, both(aliceToken.Token, bobCookie.Token)); status != http.StatusForbidden || code != CodeForbidden {
		t.Fatalf("the bearer token of a user and the cookie of an admin on an admin operation: %d %s, want 403 forbidden", status, code)
	}
	if status, _ := ask(t, h, userRoute, both(aliceToken.Token, bobCookie.Token)); status != http.StatusNoContent {
		t.Fatalf("status %d", status)
	}
	if _, p, _ := seen.last(); p.UserID != alice.ID || p.SessionID != aliceToken.Session.ID {
		t.Fatalf("the principal is %+v, want the one of the bearer token", p)
	}

	// A bearer token that is no session is not rescued by a good cookie.
	calls, _, _ := seen.last()
	for name, bearer := range map[string]string{
		"not a session": "vb_" + strings.Repeat("A", 43),
		"empty":         "",
		"garbage":       "x",
	} {
		if status, code := ask(t, h, userRoute, both(bearer, aliceCookie.Token)); status != http.StatusUnauthorized || code != CodeLoginRequired {
			t.Fatalf("a bearer token that is %s with a good cookie: %d %s, want 401 login_required", name, status, code)
		}
	}
	// Two Authorization headers are no token at all, even if both are good.
	twice := cookieOf(aliceCookie.Token)
	twice.Add("Authorization", "Bearer "+bobToken.Token)
	twice.Add("Authorization", "Bearer "+bobToken.Token)
	if status, code := ask(t, h, userRoute, twice); status != http.StatusUnauthorized || code != CodeLoginRequired {
		t.Fatalf("two Authorization headers: %d %s, want 401 login_required", status, code)
	}
	basicAndBearer := http.Header{"Authorization": {"Basic YWxpY2U6cGFzc3dvcmQ=", "Bearer " + bobToken.Token}}
	if status, code := ask(t, h, userRoute, basicAndBearer); status != http.StatusUnauthorized || code != CodeLoginRequired {
		t.Fatalf("a Basic and a Bearer header: %d %s, want 401 login_required", status, code)
	}
	if after, _, _ := seen.last(); after != calls {
		t.Fatal("an operation ran for a request whose bearer token is no session")
	}
	// An Authorization header of another scheme is not a bearer token: the
	// cookie counts.
	basic := cookieOf(aliceCookie.Token)
	basic.Set("Authorization", "Basic YWxpY2U6cGFzc3dvcmQ=")
	if status, _ := ask(t, h, userRoute, basic); status != http.StatusNoContent {
		t.Fatalf("a Basic header with a good cookie: %d", status)
	}
}

// I6, §8.4: an operation for admins answers 403 forbidden to a user and 401
// to nobody; the role is the one of the account at the request.
func TestMiddlewareChecksTheRole(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	admin := f.user(t, "root", bobPassword, RoleAdmin)
	user := f.login(t, "alice", alicePassword)
	root := f.login(t, "root", bobPassword)
	userToken := f.token(t, "alice", alicePassword)
	rootToken := f.token(t, "root", bobPassword)
	h, seen := guarded(t, f)

	if status, code := ask(t, h, adminRoute, nil); status != http.StatusUnauthorized || code != CodeLoginRequired {
		t.Fatalf("nobody on an admin operation: %d %s, want 401 login_required", status, code)
	}
	for _, header := range []http.Header{cookieOf(user.Token), bearerOf(userToken.Token)} {
		if status, code := ask(t, h, adminRoute, header); status != http.StatusForbidden || code != CodeForbidden {
			t.Fatalf("a user on an admin operation: %d %s, want 403 forbidden", status, code)
		}
	}
	if calls, _, _ := seen.last(); calls != 0 {
		t.Fatal("an admin operation ran for a request that is not of an admin")
	}
	for _, in := range []SignIn{root, rootToken} {
		header := cookieOf(in.Token)
		if in.Session.Kind == KindToken {
			header = bearerOf(in.Token)
		}
		if status, code := ask(t, h, adminRoute, header); status != http.StatusNoContent {
			t.Fatalf("an admin on an admin operation: %d %s", status, code)
		}
		if _, p, found := seen.last(); !found || p != (Principal{UserID: admin.ID, Role: RoleAdmin, SessionID: in.Session.ID}) {
			t.Fatalf("the principal of the admin is %+v", p)
		}
	}
	// An admin is a user too.
	if status, _ := ask(t, h, userRoute, cookieOf(root.Token)); status != http.StatusNoContent {
		t.Fatalf("an admin on a user operation: %d", status)
	}

	// Made a user, the admin is refused at the next request; made an admin,
	// the user is served, with the sessions they had.
	f.exec(t, `UPDATE users SET role = CASE username WHEN 'root' THEN 'user' ELSE 'admin' END`)
	if status, code := ask(t, h, adminRoute, cookieOf(root.Token)); status != http.StatusForbidden || code != CodeForbidden {
		t.Fatalf("an admin made a user: %d %s, want 403 forbidden", status, code)
	}
	if status, _ := ask(t, h, adminRoute, cookieOf(user.Token)); status != http.StatusNoContent {
		t.Fatalf("a user made an admin: %d", status)
	}
}

// A session that is revoked, expired, or of an account that is disabled or
// deleted is refused at the next request (§7.3).
func TestMiddlewareRefusesADeadSession(t *testing.T) {
	for name, kill := range map[string]func(t *testing.T, f *fixture, in SignIn){
		"signed out": func(t *testing.T, f *fixture, in SignIn) {
			if err := f.Logout(t.Context(), f.principal(t, in)); err != nil {
				t.Fatal(err)
			}
		},
		"expired":  func(_ *testing.T, f *fixture, _ SignIn) { f.clock.advance(CookieLifetime) },
		"disabled": func(t *testing.T, f *fixture, _ SignIn) { f.exec(t, `UPDATE users SET disabled = 1`) },
		"deleted":  func(t *testing.T, f *fixture, _ SignIn) { f.exec(t, `DELETE FROM users`) },
		"password reset": func(t *testing.T, f *fixture, _ SignIn) {
			if err := f.ResetPassword(t.Context(), "alice", otherPassword); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.user(t, "alice", alicePassword, RoleAdmin)
			in := f.login(t, "alice", alicePassword)
			h, seen := guarded(t, f)
			for _, route := range []string{userRoute, adminRoute} {
				if status, _ := ask(t, h, route, cookieOf(in.Token)); status != http.StatusNoContent {
					t.Fatalf("%s before: %d", route, status)
				}
			}
			kill(t, f, in)
			calls, _, _ := seen.last()
			for _, route := range []string{userRoute, writeRoute, adminRoute} {
				for _, header := range []http.Header{cookieOf(in.Token), bearerOf(in.Token)} {
					if status, code := ask(t, h, route, header); status != http.StatusUnauthorized || code != CodeLoginRequired {
						t.Fatalf("%s after: %d %s, want 401 login_required", route, status, code)
					}
				}
			}
			if after, _, _ := seen.last(); after != calls {
				t.Fatal("an operation ran with a dead session")
			}
		})
	}
}

// A route the access rules do not know is refused with 500, never served:
// an operation added without a rule is not public by accident.
func TestMiddlewareRefusesARouteWithoutARule(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleAdmin)
	in := f.login(t, "alice", alicePassword)
	h, seen := guarded(t, f)
	for _, header := range []http.Header{nil, cookieOf(in.Token), bearerOf(in.Token)} {
		if status, code := ask(t, h, unknownRoute, header); status != http.StatusInternalServerError || code != "internal" {
			t.Fatalf("a route without a rule: %d %s, want 500 internal", status, code)
		}
	}
	if calls, _, _ := seen.last(); calls != 0 {
		t.Fatal("an operation without an access rule ran")
	}
	// The zero value of Access, and a value that is none of the three, are
	// no rule either.
	for _, access := range []Access{0, AdminOnly + 1, -1} {
		mw := f.Middleware(map[string]Access{userRoute: access}, slog.New(slog.DiscardHandler))
		mux := http.NewServeMux()
		ran := false
		mux.Handle(userRoute, mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true })))
		for _, header := range []http.Header{nil, cookieOf(in.Token)} {
			if status, code := ask(t, mux, userRoute, header); status != http.StatusInternalServerError || code != "internal" || ran {
				t.Fatalf("a route with the access %d: %d %s, served: %t; want 500 internal", access, status, code, ran)
			}
		}
	}
}

// §11.5: the access log has the id of the user of an authenticated request,
// refused for its role or not, and none for the others. T28: it never has
// the token, the cookie or their hash.
func TestAccessLogHasTheUser(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", alicePassword, RoleUser)
	in := f.login(t, "alice", alicePassword)
	hash := f.session(t, in.Session.ID).TokenHash
	h, _ := guarded(t, f)

	type line struct {
		route  string
		status float64
		user   any
	}
	var want []line
	send := func(route string, header http.Header, status int, user any) {
		t.Helper()
		if got, code := ask(t, h, route, header); got != status {
			t.Fatalf("%s: %d %s, want %d", route, got, code, status)
		}
		want = append(want, line{route, float64(status), user})
	}
	send(userRoute, cookieOf(in.Token), 204, alice.ID)
	send(userRoute, bearerOf(f.token(t, "alice", alicePassword).Token), 204, alice.ID)
	send(adminRoute, cookieOf(in.Token), 403, alice.ID)
	send(userRoute, nil, 401, nil)
	send(userRoute, bearerOf("vb_"+strings.Repeat("A", 43)), 401, nil)
	send(publicRoute, cookieOf(in.Token), 204, nil)
	send(publicRoute, nil, 204, nil)
	send(unknownRoute, cookieOf(in.Token), 500, nil)

	var got []line
	for _, ev := range f.logs.events(t) {
		if ev["msg"] == "request" {
			got = append(got, line{ev["route"].(string), ev["status"].(float64), ev["user_id"]})
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%d access lines, want %d:\n%s", len(got), len(want), f.logs)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("access line %d is %+v, want %+v", i, got[i], want[i])
		}
	}
	text := f.logs.String()
	for name, secret := range map[string]string{"the token": in.Token, "the token without its prefix": in.Token[3:], "the hash of the token": hash,
		"the cookie name": CookieName, "the scheme": "Bearer", "a password": alicePassword} {
		if strings.Contains(text, secret) {
			t.Errorf("the log holds %s", name)
		}
	}
}

// Requests of two users at once, each with its own session: each operation
// sees its own principal.
func TestMiddlewareConcurrently(t *testing.T) {
	f := newFixture(t)
	users := map[string][2]SignIn{}
	for _, name := range []string{"alice", "bob", "carol", "dave"} {
		f.user(t, name, alicePassword, RoleUser)
		users[name] = [2]SignIn{f.login(t, name, alicePassword), f.token(t, name, alicePassword)}
	}
	log := slog.New(slog.DiscardHandler)
	mw := f.Middleware(map[string]Access{userRoute: Authenticated}, log)
	mux := http.NewServeMux()
	mux.Handle(userRoute, mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalOf(r.Context())
		w.Header().Set("X-User", p.UserID)
		// Leave time for another request to overwrite a shared value, if
		// there were one.
		time.Sleep(time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})))
	h := httpx.AccessLog(log, nil)(mux)

	var wg sync.WaitGroup
	for name, both := range users {
		for i := range 10 {
			wg.Go(func() {
				in := both[i%2]
				header := cookieOf(in.Token)
				if in.Session.Kind == KindToken {
					header = bearerOf(in.Token)
				}
				req := httptest.NewRequest("GET", "/user", nil)
				req.Header = header
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusNoContent || rec.Header().Get("X-User") != in.User.ID {
					t.Errorf("a request of %s: status %d, served as another user: %t", name, rec.Code, rec.Header().Get("X-User") != in.User.ID)
				}
			})
		}
	}
	wg.Wait()
}
