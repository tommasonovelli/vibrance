package app

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"vibrance/internal/api"
	"vibrance/internal/auth"
	"vibrance/internal/buildinfo"
)

// The tests of the operations of DESIGN.md §8.3 on the server, account and
// users: each success and each error the table lists, through the whole
// stack (boundary, validation, authentication, handler, service, SQLite),
// every answer checked against the specification by world.do.

var tokenForm = regexp.MustCompile(`^vb_[A-Za-z0-9_-]{43}$`)

// parseTime reads a Timestamp of the API.
func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse("2006-01-02T15:04:05.000Z", s)
	if err != nil {
		t.Fatalf("the date %q: %v", s, err)
	}
	return v
}

func TestGetServerInfo(t *testing.T) {
	w := newWorld(t, apiOrigin)
	for _, c := range []credential{nobody, w.as(w.anna)} {
		rec := w.do("GET", "/server", nil, c)
		wantStatus(t, "getServerInfo", rec, http.StatusOK)
		got := decode[api.ServerInfo](t, rec)
		if got != (api.ServerInfo{Name: "Vibrance", Version: buildinfo.Version, ApiVersion: 1}) {
			t.Fatalf("getServerInfo: %+v", got)
		}
	}
}

// §7.3, T14: the session cookie is HttpOnly, SameSite=Strict, Path=/, lasts
// 30 days, and is Secure if and only if the public origin is https: with
// http and Secure a browser in the LAN would drop it.
func TestLoginSetsTheCookie(t *testing.T) {
	for origin, secure := range map[string]bool{"http://vibrance.lan:8090": false, apiOrigin: true} {
		w := newWorld(t, origin)
		rec := w.do("POST", "/auth/login", map[string]any{"username": "ANNA", "password": w.anna.password, "device_name": "Firefox"}, nobody)
		wantStatus(t, "login on "+origin, rec, http.StatusOK)
		raw := rec.Header().Values("Set-Cookie")
		if len(raw) != 1 {
			t.Fatalf("%s: %d Set-Cookie headers", origin, len(raw))
		}
		c, err := http.ParseSetCookie(raw[0])
		if err != nil {
			t.Fatal(err)
		}
		if c.Name != auth.CookieName || !tokenForm.MatchString(c.Value) || c.Path != "/" || c.Domain != "" ||
			c.MaxAge != 30*24*3600 || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Secure != secure {
			t.Errorf("%s: the cookie has the flags path=%q domain=%q max-age=%d httponly=%v samesite=%v secure=%v",
				origin, c.Path, c.Domain, c.MaxAge, c.HttpOnly, c.SameSite, c.Secure)
		}
		if strings.Contains(raw[0], "Secure") != secure {
			t.Errorf("%s: Secure in the header is %v", origin, !secure)
		}
		got := decode[api.LoginResult](t, rec)
		if got.User.Id != w.anna.id || got.User.Username != "anna" || got.User.Role != "user" || got.User.Disabled ||
			got.Session.Kind != "cookie" || got.Session.DeviceName == nil || *got.Session.DeviceName != "Firefox" || !got.Session.Current {
			t.Errorf("%s: the body is %+v", origin, got)
		}
		created, expires := parseTime(t, got.Session.CreatedAt), parseTime(t, got.Session.ExpiresAt)
		if expires.Sub(created) != auth.CookieLifetime || got.Session.LastUsedAt != got.Session.CreatedAt {
			t.Errorf("%s: the session lasts %s", origin, expires.Sub(created))
		}
		if strings.Contains(rec.Body.String(), c.Value) {
			t.Errorf("%s: the token of the cookie is in the body", origin)
		}
		// The cookie is the session; as a bearer token it is not (N-099).
		me := w.do("GET", "/me", nil, sessionCookie(c.Value))
		wantStatus(t, "getMe with the cookie", me, http.StatusOK)
		if decode[api.User](t, me).Id != w.anna.id {
			t.Errorf("%s: the cookie is not of anna", origin)
		}
		wantCode(t, "the cookie as a bearer token", w.do("GET", "/me", nil, bearer(c.Value)), http.StatusUnauthorized, auth.CodeLoginRequired)

		// Without a device name, the session has none.
		rec = w.do("POST", "/auth/login", map[string]any{"username": "anna", "password": w.anna.password}, nobody)
		wantStatus(t, "login without a device", rec, http.StatusOK)
		if got := decode[api.LoginResult](t, rec); got.Session.DeviceName != nil {
			t.Errorf("%s: device_name %q, want null", origin, *got.Session.DeviceName)
		}
	}
}

// §7.2: a wrong password, a name that no account has and a disabled
// account are one answer, with no cookie.
func TestLoginRefusals(t *testing.T) {
	w := newWorld(t, apiOrigin)
	disabled := w.newAccount(auth.RoleUser)
	if _, err := w.sessions.UpdateUser(t.Context(), auth.Principal{UserID: w.admin.id, Role: auth.RoleAdmin}, disabled.id, auth.RoleUser, true); err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for name, body := range map[string]map[string]any{
		"a wrong password": {"username": "anna", "password": w.bob.password},
		"nobody":           {"username": "nobody", "password": w.anna.password},
		"a disabled user":  {"username": disabled.name, "password": disabled.password},
		"not a name":       {"username": "", "password": ""},
	} {
		for _, path := range []string{"/auth/login", "/auth/tokens"} {
			body := maps(body, "device_name", "phone")
			rec := w.do("POST", path, body, nobody)
			wantCode(t, name+" on "+path, rec, http.StatusUnauthorized, auth.CodeInvalidCredentials)
			if rec.Header().Get("Set-Cookie") != "" {
				t.Errorf("%s on %s: a cookie was set", name, path)
			}
			bodies = append(bodies, rec.Body.String())
		}
	}
	for _, b := range bodies {
		if b != bodies[0] {
			t.Fatalf("the refusals differ: %q and %q", b, bodies[0])
		}
	}
}

// maps is m with one more key.
func maps(m map[string]any, k string, v any) map[string]any {
	out := map[string]any{k: v}
	for key, value := range m {
		out[key] = value
	}
	return out
}

// §7.3: a token for a client that is not a browser, in the body, once; it
// is a session as a bearer token, and not as a cookie.
func TestCreateToken(t *testing.T) {
	w := newWorld(t, "http://vibrance.lan")
	rec := w.do("POST", "/auth/tokens", map[string]any{"username": "anna", "password": w.anna.password, "device_name": "Anna's phone"}, nobody)
	wantStatus(t, "createToken", rec, http.StatusCreated)
	if rec.Header().Get("Set-Cookie") != "" {
		t.Error("createToken set a cookie")
	}
	got := decode[api.TokenResult](t, rec)
	if !tokenForm.MatchString(got.Token) || got.User.Id != w.anna.id || got.Session.Kind != "token" ||
		got.Session.DeviceName == nil || *got.Session.DeviceName != "Anna's phone" || !got.Session.Current {
		t.Fatalf("the body is not the token of anna: kind %s", got.Session.Kind)
	}
	if lasts := parseTime(t, got.Session.ExpiresAt).Sub(parseTime(t, got.Session.CreatedAt)); lasts != auth.TokenLifetime {
		t.Fatalf("the token lasts %s", lasts)
	}
	wantStatus(t, "getMe with the token", w.do("GET", "/me", nil, bearer(got.Token)), http.StatusOK)
	wantCode(t, "the token as a cookie", w.do("GET", "/me", nil, sessionCookie(got.Token)), http.StatusUnauthorized, auth.CodeLoginRequired)
}

// §7.3: the sign-out revokes the session of the request at once, and drops
// the cookie; the other sessions of the user live on.
func TestLogout(t *testing.T) {
	for _, secure := range []bool{false, true} {
		origin := "http://vibrance.lan"
		if secure {
			origin = apiOrigin
		}
		w := newWorld(t, origin)
		other := w.token(w.anna)
		for kind, c := range map[string]auth.SignIn{"cookie": w.cookie(w.anna), "token": w.token(w.anna)} {
			present := bearer(c.Token)
			if kind == "cookie" {
				present = sessionCookie(c.Token)
			}
			rec := w.do("POST", "/auth/logout", nil, present)
			wantStatus(t, "logout of a "+kind, rec, http.StatusNoContent)
			dropped, err := http.ParseSetCookie(rec.Header().Get("Set-Cookie"))
			if err != nil || dropped.Name != auth.CookieName || dropped.Value != "" || dropped.MaxAge >= 0 || dropped.Path != "/" ||
				!dropped.HttpOnly || dropped.SameSite != http.SameSiteStrictMode || dropped.Secure != secure {
				t.Errorf("logout of a %s: Set-Cookie %q", kind, rec.Header().Get("Set-Cookie"))
			}
			if !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
				t.Errorf("logout of a %s: the cookie is not expired: %q", kind, rec.Header().Get("Set-Cookie"))
			}
			// Revoked: the session is useless at once, for every operation.
			wantCode(t, "getMe after the logout", w.do("GET", "/me", nil, present), http.StatusUnauthorized, auth.CodeLoginRequired)
			wantCode(t, "logout again", w.do("POST", "/auth/logout", nil, present), http.StatusUnauthorized, auth.CodeLoginRequired)
		}
		wantStatus(t, "another session of anna", w.do("GET", "/me", nil, bearer(other.Token)), http.StatusOK)
		wantCode(t, "logout without a session", w.do("POST", "/auth/logout", nil, nobody), http.StatusUnauthorized, auth.CodeLoginRequired)
	}
}

func TestGetMe(t *testing.T) {
	w := newWorld(t, apiOrigin)
	for _, a := range []*account{w.admin, w.anna} {
		rec := w.do("GET", "/me", nil, w.as(a))
		wantStatus(t, "getMe", rec, http.StatusOK)
		got := decode[api.User](t, rec)
		role := api.Role(auth.RoleUser)
		if a == w.admin {
			role = auth.RoleAdmin
		}
		if got.Id != a.id || got.Username != a.name || got.Role != role || got.Disabled {
			t.Errorf("getMe of %s: %+v", a.name, got)
		}
	}
	wantCode(t, "getMe without a session", w.do("GET", "/me", nil, nobody), http.StatusUnauthorized, auth.CodeLoginRequired)
	wantCode(t, "getMe with a token that is no session", w.do("GET", "/me", nil, bearer("vb_"+strings.Repeat("A", 43))), http.StatusUnauthorized, auth.CodeLoginRequired)
}

// §7.3: a change of password revokes every other session of the user; the
// one of the request stays. 422 for a wrong current password and for a new
// one that is not valid; nothing changes then.
func TestChangePassword(t *testing.T) {
	w := newWorld(t, apiOrigin)
	current := w.token(w.anna)
	others := []auth.SignIn{w.token(w.anna), w.cookie(w.anna)}
	bob := w.token(w.bob)
	const replacement = "the new password of anna"

	for name, body := range map[string]map[string]any{
		"a wrong current password": {"current_password": w.bob.password, "new_password": replacement},
		"a short new password":     {"current_password": w.anna.password, "new_password": "short"},
		"a new one with a tab":     {"current_password": w.anna.password, "new_password": "a password\twith a tab"},
		"a new one too long":       {"current_password": w.anna.password, "new_password": strings.Repeat("p", 1025)},
	} {
		rec := w.do("PUT", "/me/password", body, bearer(current.Token))
		code := auth.CodePasswordInvalid
		if name == "a wrong current password" {
			code = auth.CodeCurrentPasswordInvalid
		}
		wantCode(t, name, rec, http.StatusUnprocessableEntity, code)
	}
	for _, in := range others {
		wantStatus(t, "another session after a refused change", w.do("GET", "/me", nil, credentialOf(in)), http.StatusOK)
	}

	rec := w.do("PUT", "/me/password", map[string]any{"current_password": w.anna.password, "new_password": replacement}, bearer(current.Token))
	wantStatus(t, "changePassword", rec, http.StatusNoContent)
	wantStatus(t, "the session of the change", w.do("GET", "/me", nil, bearer(current.Token)), http.StatusOK)
	for _, in := range others {
		wantCode(t, "another session after the change", w.do("GET", "/me", nil, credentialOf(in)), http.StatusUnauthorized, auth.CodeLoginRequired)
	}
	wantStatus(t, "a session of bob", w.do("GET", "/me", nil, bearer(bob.Token)), http.StatusOK)
	wantCode(t, "the old password", w.do("POST", "/auth/login", map[string]any{"username": "anna", "password": w.anna.password}, nobody),
		http.StatusUnauthorized, auth.CodeInvalidCredentials)
	wantStatus(t, "the new password", w.do("POST", "/auth/login", map[string]any{"username": "anna", "password": replacement}, nobody), http.StatusOK)
	wantCode(t, "without a session", w.do("PUT", "/me/password", map[string]any{"current_password": replacement, "new_password": replacement}, nobody),
		http.StatusUnauthorized, auth.CodeLoginRequired)
}

// credentialOf presents a session the way it was made.
func credentialOf(in auth.SignIn) credential {
	if in.Session.Kind == auth.KindCookie {
		return sessionCookie(in.Token)
	}
	return bearer(in.Token)
}

// §7.3: the live sessions of the user of the request, and only those.
func TestListSessions(t *testing.T) {
	w := newWorld(t, apiOrigin)
	rec := w.do("POST", "/auth/login", map[string]any{"username": "anna", "password": w.anna.password, "device_name": "laptop"}, nobody)
	wantStatus(t, "login", rec, http.StatusOK)
	browser := decode[api.LoginResult](t, rec).Session
	phone := w.token(w.anna)
	w.token(w.bob)

	rec = w.do("GET", "/me/sessions", nil, bearer(phone.Token))
	wantStatus(t, "listSessions", rec, http.StatusOK)
	got := decode[api.SessionList](t, rec).Sessions
	if len(got) != 2 {
		t.Fatalf("%d sessions, want the 2 of anna", len(got))
	}
	byID := map[string]api.Session{}
	for _, s := range got {
		byID[s.Id] = s
	}
	if s := byID[phone.Session.ID]; !s.Current || s.Kind != "token" || s.DeviceName == nil || *s.DeviceName != "tests" {
		t.Errorf("the session of the request: %+v", s)
	}
	if s := byID[browser.Id]; s.Current || s.Kind != "cookie" || s.DeviceName == nil || *s.DeviceName != "laptop" {
		t.Errorf("the other session: %+v", s)
	}
	wantCode(t, "without a session", w.do("GET", "/me/sessions", nil, nobody), http.StatusUnauthorized, auth.CodeLoginRequired)
}

// §7.3, I6: a revoked session is useless at once; the session of another
// user is a 404, as one that does not exist, and stays alive.
func TestRevokeSession(t *testing.T) {
	w := newWorld(t, apiOrigin)
	current, lost, bob := w.token(w.anna), w.cookie(w.anna), w.token(w.bob)

	wantStatus(t, "revoke a session", w.do("DELETE", "/me/sessions/"+lost.Session.ID, nil, bearer(current.Token)), http.StatusNoContent)
	wantCode(t, "the revoked session", w.do("GET", "/me", nil, sessionCookie(lost.Token)), http.StatusUnauthorized, auth.CodeLoginRequired)
	sessions := decode[api.SessionList](t, w.do("GET", "/me/sessions", nil, bearer(current.Token))).Sessions
	if len(sessions) != 1 || sessions[0].Id != current.Session.ID {
		t.Fatalf("the sessions left are %+v", sessions)
	}
	wantCode(t, "revoke it again", w.do("DELETE", "/me/sessions/"+lost.Session.ID, nil, bearer(current.Token)), http.StatusNotFound, auth.CodeSessionNotFound)
	wantCode(t, "a session of bob", w.do("DELETE", "/me/sessions/"+bob.Session.ID, nil, bearer(current.Token)), http.StatusNotFound, auth.CodeSessionNotFound)
	wantStatus(t, "bob after anna tried", w.do("GET", "/me", nil, bearer(bob.Token)), http.StatusOK)
	wantCode(t, "a session that never was", w.do("DELETE", "/me/sessions/"+someID, nil, bearer(current.Token)), http.StatusNotFound, auth.CodeSessionNotFound)
	// The admin is no exception (§8.6, I6).
	wantCode(t, "a session of bob, by the admin", w.do("DELETE", "/me/sessions/"+bob.Session.ID, nil, w.as(w.admin)), http.StatusNotFound, auth.CodeSessionNotFound)

	// The current session, too.
	wantStatus(t, "revoke the current session", w.do("DELETE", "/me/sessions/"+current.Session.ID, nil, bearer(current.Token)), http.StatusNoContent)
	wantCode(t, "after revoking itself", w.do("GET", "/me", nil, bearer(current.Token)), http.StatusUnauthorized, auth.CodeLoginRequired)
}

func TestListUsers(t *testing.T) {
	w := newWorld(t, apiOrigin)
	carl := w.newAccount(auth.RoleUser)
	if _, err := w.sessions.UpdateUser(t.Context(), auth.Principal{UserID: w.admin.id, Role: auth.RoleAdmin}, carl.id, auth.RoleUser, true); err != nil {
		t.Fatal(err)
	}
	rec := w.do("GET", "/admin/users", nil, w.as(w.admin))
	wantStatus(t, "listUsers", rec, http.StatusOK)
	var names []string
	for _, u := range decode[api.UserList](t, rec).Users {
		names = append(names, u.Username)
		if u.Username == carl.name && !u.Disabled {
			t.Error("the disabled account is not listed as disabled")
		}
	}
	if !slices.Equal(names, []string{"admin", "anna", "bob", carl.name}) {
		t.Fatalf("the accounts are %v", names)
	}
	wantCode(t, "listUsers as a user", w.do("GET", "/admin/users", nil, w.as(w.anna)), http.StatusForbidden, auth.CodeForbidden)
}

// §7.1, §7.5: an admin creates the accounts; the name is checked, and taken
// at most once.
func TestCreateUser(t *testing.T) {
	w := newWorld(t, apiOrigin)
	admin := w.as(w.admin)
	rec := w.do("POST", "/admin/users", map[string]any{"username": "carl.d-2_x", "password": "the password of carl", "role": "admin"}, admin)
	wantStatus(t, "createUser", rec, http.StatusCreated)
	carl := decode[api.User](t, rec)
	if carl.Username != "carl.d-2_x" || carl.Role != "admin" || carl.Disabled {
		t.Fatalf("the account is %+v", carl)
	}
	signIn := w.do("POST", "/auth/tokens", map[string]any{"username": "carl.d-2_x", "password": "the password of carl", "device_name": "x"}, nobody)
	wantStatus(t, "the new account signs in", signIn, http.StatusCreated)
	wantStatus(t, "the new admin lists the accounts", w.do("GET", "/admin/users", nil, bearer(decode[api.TokenResult](t, signIn).Token)), http.StatusOK)

	for _, name := range []string{"anna", "carl.d-2_x"} {
		wantCode(t, "a name taken: "+name, w.do("POST", "/admin/users", map[string]any{"username": name, "password": "a password long enough", "role": "user"}, admin),
			http.StatusConflict, auth.CodeUsernameTaken)
	}
	for _, name := range []string{"Anna", "an", "-anna", ".anna", "anna smith", strings.Repeat("a", 33), "ànna", ""} {
		wantCode(t, "the name "+name, w.do("POST", "/admin/users", map[string]any{"username": name, "password": "a password long enough", "role": "user"}, admin),
			http.StatusUnprocessableEntity, auth.CodeUsernameInvalid)
	}
	for _, password := range []string{"", "eleven char", "a password\nwith a line break", strings.Repeat("p", 1025)} {
		wantCode(t, "a password that is not one", w.do("POST", "/admin/users", map[string]any{"username": "dora", "password": password, "role": "user"}, admin),
			http.StatusUnprocessableEntity, auth.CodePasswordInvalid)
	}
	wantCode(t, "createUser as a user", w.do("POST", "/admin/users", map[string]any{"username": "dora", "password": "a password long enough", "role": "admin"}, w.as(w.anna)),
		http.StatusForbidden, auth.CodeForbidden)
	users, err := w.sessions.ListUsers(t.Context())
	if err != nil || len(users) != 4 {
		t.Fatalf("%d accounts after the refusals, want 4: %v", len(users), err)
	}
}

func TestGetUser(t *testing.T) {
	w := newWorld(t, apiOrigin)
	rec := w.do("GET", "/admin/users/"+w.anna.id, nil, w.as(w.admin))
	wantStatus(t, "getUser", rec, http.StatusOK)
	if got := decode[api.User](t, rec); got.Id != w.anna.id || got.Username != "anna" || got.Role != "user" {
		t.Fatalf("getUser: %+v", got)
	}
	wantCode(t, "getUser of nobody", w.do("GET", "/admin/users/"+someID, nil, w.as(w.admin)), http.StatusNotFound, auth.CodeUserNotFound)
	wantCode(t, "getUser as a user", w.do("GET", "/admin/users/"+w.anna.id, nil, w.as(w.anna)), http.StatusForbidden, auth.CodeForbidden)
}

// §7.5: the role counts at the next request; a disabled account loses its
// sessions at once and cannot sign in.
func TestUpdateUser(t *testing.T) {
	w := newWorld(t, apiOrigin)
	admin := w.as(w.admin)
	anna := w.token(w.anna)
	update := func(id, role string, disabled bool, c credential) *httptest.ResponseRecorder {
		return w.do("PUT", "/admin/users/"+id, map[string]any{"role": role, "disabled": disabled}, c)
	}

	wantCode(t, "a user on the admin operations", w.do("GET", "/admin/users", nil, bearer(anna.Token)), http.StatusForbidden, auth.CodeForbidden)
	rec := update(w.anna.id, "admin", false, admin)
	wantStatus(t, "promote anna", rec, http.StatusOK)
	if got := decode[api.User](t, rec); got.Id != w.anna.id || got.Role != "admin" || got.Disabled {
		t.Fatalf("after the promotion: %+v", got)
	}
	wantStatus(t, "anna, admin, with the session she had", w.do("GET", "/admin/users", nil, bearer(anna.Token)), http.StatusOK)

	rec = update(w.anna.id, "user", true, admin)
	wantStatus(t, "demote and disable anna", rec, http.StatusOK)
	if got := decode[api.User](t, rec); got.Role != "user" || !got.Disabled {
		t.Fatalf("after the change: %+v", got)
	}
	wantCode(t, "the session of a disabled account", w.do("GET", "/me", nil, bearer(anna.Token)), http.StatusUnauthorized, auth.CodeLoginRequired)
	wantCode(t, "a disabled account signs in", w.do("POST", "/auth/login", map[string]any{"username": "anna", "password": w.anna.password}, nobody),
		http.StatusUnauthorized, auth.CodeInvalidCredentials)
	wantStatus(t, "enable anna", update(w.anna.id, "user", false, admin), http.StatusOK)
	wantStatus(t, "anna signs in again", w.do("POST", "/auth/login", map[string]any{"username": "anna", "password": w.anna.password}, nobody), http.StatusOK)

	wantCode(t, "updateUser of nobody", update(someID, "user", false, admin), http.StatusNotFound, auth.CodeUserNotFound)
	wantCode(t, "updateUser as a user", update(w.bob.id, "admin", false, w.as(w.bob)), http.StatusForbidden, auth.CodeForbidden)
	if got := decode[api.User](t, w.do("GET", "/me", nil, w.as(w.bob))); got.Role != "user" {
		t.Fatal("a user promoted themselves")
	}
}

// §7.5: the last enabled admin cannot be demoted, disabled or deleted; an
// admin cannot disable or delete their own account. Nothing changes then.
func TestAdministrationRules(t *testing.T) {
	w := newWorld(t, apiOrigin)
	self := func() credential { return w.as(w.admin) }
	update := func(id, role string, disabled bool) *httptest.ResponseRecorder {
		return w.do("PUT", "/admin/users/"+id, map[string]any{"role": role, "disabled": disabled}, self())
	}
	// The only admin.
	wantCode(t, "demote the last admin", update(w.admin.id, "user", false), http.StatusConflict, auth.CodeLastAdmin)
	wantCode(t, "disable the last admin", update(w.admin.id, "admin", true), http.StatusConflict, auth.CodeLastAdmin)
	wantCode(t, "delete the last admin", w.do("DELETE", "/admin/users/"+w.admin.id, nil, self()), http.StatusConflict, auth.CodeLastAdmin)
	wantStatus(t, "the values it has", update(w.admin.id, "admin", false), http.StatusOK)

	// With another admin, disabled: still the last enabled one.
	other := w.newAccount(auth.RoleAdmin)
	wantStatus(t, "disable the other admin", update(other.id, "admin", true), http.StatusOK)
	wantCode(t, "demote the last enabled admin", update(w.admin.id, "user", false), http.StatusConflict, auth.CodeLastAdmin)

	// With another enabled admin: not oneself, but a demotion is allowed.
	wantStatus(t, "enable the other admin", update(other.id, "admin", false), http.StatusOK)
	wantCode(t, "disable oneself", update(w.admin.id, "admin", true), http.StatusConflict, auth.CodeCannotModifySelf)
	wantCode(t, "delete oneself", w.do("DELETE", "/admin/users/"+w.admin.id, nil, self()), http.StatusConflict, auth.CodeCannotModifySelf)
	me := decode[api.User](t, w.do("GET", "/me", nil, self()))
	if me.Role != "admin" || me.Disabled {
		t.Fatalf("a refused change changed the admin: %+v", me)
	}
	wantStatus(t, "demote oneself", update(w.admin.id, "user", false), http.StatusOK)
	wantCode(t, "a demoted admin", w.do("GET", "/admin/users", nil, self()), http.StatusForbidden, auth.CodeForbidden)
	// The other admin is now the last one, and is protected in turn.
	byOther := w.as(other)
	wantCode(t, "the new last admin demotes itself", w.do("PUT", "/admin/users/"+other.id, map[string]any{"role": "user", "disabled": false}, byOther),
		http.StatusConflict, auth.CodeLastAdmin)
}

// §7.5: deleting an account deletes its sessions at once (and its favorites
// and playlists: internal/auth tests the cascade on every table).
func TestDeleteUser(t *testing.T) {
	w := newWorld(t, apiOrigin)
	anna := []auth.SignIn{w.token(w.anna), w.cookie(w.anna)}
	wantStatus(t, "deleteUser", w.do("DELETE", "/admin/users/"+w.anna.id, nil, w.as(w.admin)), http.StatusNoContent)
	wantCode(t, "getUser of the deleted", w.do("GET", "/admin/users/"+w.anna.id, nil, w.as(w.admin)), http.StatusNotFound, auth.CodeUserNotFound)
	for _, in := range anna {
		wantCode(t, "a session of the deleted", w.do("GET", "/me", nil, credentialOf(in)), http.StatusUnauthorized, auth.CodeLoginRequired)
	}
	wantCode(t, "the deleted signs in", w.do("POST", "/auth/login", map[string]any{"username": "anna", "password": w.anna.password}, nobody),
		http.StatusUnauthorized, auth.CodeInvalidCredentials)
	wantCode(t, "deleteUser again", w.do("DELETE", "/admin/users/"+w.anna.id, nil, w.as(w.admin)), http.StatusNotFound, auth.CodeUserNotFound)
	wantCode(t, "deleteUser as a user", w.do("DELETE", "/admin/users/"+w.admin.id, nil, w.as(w.bob)), http.StatusForbidden, auth.CodeForbidden)
	// The name is free again.
	wantStatus(t, "anna again", w.do("POST", "/admin/users", map[string]any{"username": "anna", "password": "a new password of anna", "role": "user"}, w.as(w.admin)),
		http.StatusCreated)
}

// §7.3: the reset of an admin revokes every session of the account.
func TestResetUserPassword(t *testing.T) {
	w := newWorld(t, apiOrigin)
	anna := []auth.SignIn{w.token(w.anna), w.cookie(w.anna)}
	const replacement = "a password the admin chose"
	reset := func(id, password string, c credential) *httptest.ResponseRecorder {
		return w.do("PUT", "/admin/users/"+id+"/password", map[string]any{"password": password}, c)
	}
	wantCode(t, "a password that is not one", reset(w.anna.id, "short", w.as(w.admin)), http.StatusUnprocessableEntity, auth.CodePasswordInvalid)
	wantStatus(t, "a session before the reset", w.do("GET", "/me", nil, credentialOf(anna[0])), http.StatusOK)

	wantStatus(t, "resetUserPassword", reset(w.anna.id, replacement, w.as(w.admin)), http.StatusNoContent)
	for _, in := range anna {
		wantCode(t, "a session after the reset", w.do("GET", "/me", nil, credentialOf(in)), http.StatusUnauthorized, auth.CodeLoginRequired)
	}
	wantCode(t, "the old password", w.do("POST", "/auth/login", map[string]any{"username": "anna", "password": w.anna.password}, nobody),
		http.StatusUnauthorized, auth.CodeInvalidCredentials)
	wantStatus(t, "the new password", w.do("POST", "/auth/login", map[string]any{"username": "anna", "password": replacement}, nobody), http.StatusOK)

	wantCode(t, "reset of nobody", reset(someID, replacement, w.as(w.admin)), http.StatusNotFound, auth.CodeUserNotFound)
	wantCode(t, "reset as a user", reset(w.anna.id, replacement, w.as(w.bob)), http.StatusForbidden, auth.CodeForbidden)
}

// S14: two admins who delete each other at the same moment, through the
// API: one deletion wins and the other is refused; one admin is left.
func TestAdminsDeleteEachOtherOverTheAPI(t *testing.T) {
	for range 10 {
		w := newWorld(t, apiOrigin)
		other := w.newAccount(auth.RoleAdmin)
		first, second := w.as(w.admin), w.as(other)
		var wg sync.WaitGroup
		recs := make([]*httptest.ResponseRecorder, 2)
		start := make(chan struct{})
		wg.Go(func() {
			<-start
			req := w.request("DELETE", "/admin/users/"+other.id, nil)
			first(req)
			recs[0] = send(w.s.http.Handler, req)
		})
		wg.Go(func() {
			<-start
			req := w.request("DELETE", "/admin/users/"+w.admin.id, nil)
			second(req)
			recs[1] = send(w.s.http.Handler, req)
		})
		close(start)
		wg.Wait()
		// The deletion that lost is refused by the rule, 409 last_admin, or,
		// when its request is authenticated after the other deletion took its
		// account and its sessions away, 401 login_required.
		statuses := []int{recs[0].Code, recs[1].Code}
		slices.Sort(statuses)
		if statuses[0] != http.StatusNoContent || (statuses[1] != http.StatusUnauthorized && statuses[1] != http.StatusConflict) {
			t.Fatalf("the two deletions answered %v, want one 204 and one 409 or 401", statuses)
		}
		for _, rec := range recs {
			switch rec.Code {
			case http.StatusConflict:
				wantCode(t, "the deletion that lost", rec, http.StatusConflict, auth.CodeLastAdmin)
			case http.StatusUnauthorized:
				wantCode(t, "the deletion that lost", rec, http.StatusUnauthorized, auth.CodeLoginRequired)
			}
		}
		users, err := w.sessions.ListUsers(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		admins := 0
		for _, u := range users {
			if u.Role == auth.RoleAdmin && !u.Disabled {
				admins++
			}
		}
		if admins != 1 {
			t.Fatalf("%d enabled admins are left, want 1", admins)
		}
	}
}

// T28, I5: the log of a whole use of these operations holds none of the
// passwords and tokens that crossed them.
func TestAccountsLogHoldsNoSecrets(t *testing.T) {
	w := newWorld(t, apiOrigin)
	const (
		first  = "the first secret password"
		second = "the second secret password"
		third  = "the third secret password"
	)
	var secrets []string
	admin := w.token(w.admin)
	secrets = append(secrets, admin.Token)
	rec := w.do("POST", "/admin/users", map[string]any{"username": "carl", "password": first, "role": "user"}, bearer(admin.Token))
	carl := decode[api.User](t, rec)
	rec = w.do("POST", "/auth/login", map[string]any{"username": "carl", "password": first}, nobody)
	cookie, err := http.ParseSetCookie(rec.Header().Get("Set-Cookie"))
	if err != nil {
		t.Fatal(err)
	}
	secrets = append(secrets, cookie.Value)
	rec = w.do("POST", "/auth/tokens", map[string]any{"username": "carl", "password": first, "device_name": "phone"}, nobody)
	token := decode[api.TokenResult](t, rec).Token
	secrets = append(secrets, token)
	w.do("POST", "/auth/login", map[string]any{"username": "carl", "password": second}, nobody)
	w.do("POST", "/auth/login", map[string]any{"username": second, "password": first}, nobody)
	w.do("PUT", "/me/password", map[string]any{"current_password": second, "new_password": third}, bearer(token))
	w.do("PUT", "/me/password", map[string]any{"current_password": first, "new_password": second}, bearer(token))
	w.do("PUT", "/admin/users/"+carl.Id+"/password", map[string]any{"password": third}, bearer(admin.Token))
	w.do("GET", "/me", nil, sessionCookie(cookie.Value))
	w.do("POST", "/auth/logout", nil, bearer(admin.Token))

	text := w.logs.String()
	if !strings.Contains(text, "sign-in refused") || !strings.Contains(text, "password reset") {
		t.Fatal("the log does not have the events of the test")
	}
	for i, secret := range append(secrets, first, second, third, "secret") {
		if strings.Contains(text, secret) {
			t.Errorf("the log holds the secret #%d", i)
		}
	}
}
