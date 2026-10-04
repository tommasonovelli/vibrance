package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// I5, T28: a whole life of two accounts, every refusal included, with the
// log at DEBUG. Neither the log nor any error holds a password, a token, a
// cookie value or a hash; a refused sign-in logs the name it was for and the
// address it came from, and nothing else of what the client sent.
func TestLogAndErrorsHoldNoSecrets(t *testing.T) {
	const (
		adminPassword   = "the first password of the admin"
		newPassword     = "the second password of the admin"
		resetPassword   = "the password after the reset 123"
		guessedPassword = "a guess that is wrong: hunter2-hunter2"
		// A password typed where the name goes.
		misplaced  = "Tr0ub4dor&3 typed in the wrong field"
		remoteAddr = "192.0.2.7:51234"
	)
	f := newFixture(t)
	var errs []error
	note := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	note(f.Bootstrap(t.Context(), envOf(map[string]string{"VIBRANCE_ADMIN_PASSWORD": "short"})))
	note(f.Bootstrap(t.Context(), envOf(map[string]string{"VIBRANCE_ADMIN_PASSWORD": adminPassword + "\n"})))
	if err := f.Bootstrap(t.Context(), envOf(map[string]string{"VIBRANCE_ADMIN_PASSWORD": adminPassword})); err != nil {
		t.Fatal(err)
	}
	_, err := f.CreateUser(t.Context(), "alice", alicePassword, RoleUser)
	note(err)
	_, err = f.CreateUser(t.Context(), "alice", bobPassword, RoleUser)
	note(err)
	_, err = f.CreateUser(t.Context(), misplaced, bobPassword, RoleUser)
	note(err)
	_, err = f.CreateUser(t.Context(), "bob", "short\tpw", RoleUser)
	note(err)

	var signIns []SignIn
	signIn := func(fn func(context.Context, string, string, string, string) (SignIn, error), username, password string) {
		in, err := fn(t.Context(), username, password, "the phone of the tests", remoteAddr)
		note(err)
		if err == nil {
			signIns = append(signIns, in)
		}
	}
	signIn(f.Login, "admin", adminPassword)
	signIn(f.CreateToken, "admin", adminPassword)
	signIn(f.Login, "alice", alicePassword)
	signIn(f.Login, "admin", guessedPassword)
	signIn(f.CreateToken, "alice", guessedPassword)
	signIn(f.Login, "nobody", guessedPassword)
	signIn(f.Login, misplaced, adminPassword)
	signIn(f.Login, adminPassword, adminPassword)
	if len(signIns) != 3 {
		t.Fatalf("%d sign-ins were accepted, want 3", len(signIns))
	}
	admin, adminToken, alice := signIns[0], signIns[1], signIns[2]

	var hashes []string
	for _, in := range signIns {
		hashes = append(hashes, f.session(t, in.Session.ID).TokenHash)
	}
	hashes = append(hashes, f.passwordHash(t, "admin"), f.passwordHash(t, "alice"), f.dummy)

	// The requests of the same life, through the middleware.
	h, _ := guarded(t, f)
	for _, header := range []http.Header{cookieOf(admin.Token), bearerOf(adminToken.Token), cookieOf(alice.Token), bearerOf(guessedPassword)} {
		for _, route := range []string{userRoute, adminRoute, publicRoute, unknownRoute} {
			ask(t, h, route, header)
		}
	}

	p := f.principal(t, admin)
	for _, token := range []string{"vb_" + strings.Repeat("A", 43), admin.Token + "x", "", hashes[0]} {
		_, err := f.Authenticate(t.Context(), KindCookie, token)
		note(err)
	}
	_, err = f.ListSessions(t.Context(), p)
	note(err)
	note(f.Revoke(t.Context(), p, alice.Session.ID))
	note(f.Revoke(t.Context(), p, adminToken.Token))
	note(f.ChangePassword(t.Context(), p, guessedPassword, newPassword))
	note(f.ChangePassword(t.Context(), p, adminPassword, "short"))
	if err := f.ChangePassword(t.Context(), p, adminPassword, newPassword); err != nil {
		t.Fatal(err)
	}
	hashes = append(hashes, f.passwordHash(t, "admin"))
	_, err = f.Authenticate(t.Context(), adminToken.Session.Kind, adminToken.Token) // revoked by the change
	note(err)
	note(f.ResetPassword(t.Context(), "nobody", resetPassword))
	note(f.ResetPassword(t.Context(), "admin", "short"))
	if err := f.ResetPassword(t.Context(), "admin", resetPassword); err != nil {
		t.Fatal(err)
	}
	hashes = append(hashes, f.passwordHash(t, "admin"))
	_, err = f.Authenticate(t.Context(), admin.Session.Kind, admin.Token) // revoked by the reset
	note(err)
	note(f.Logout(t.Context(), f.principal(t, alice)))
	f.clock.advance(TokenLifetime)
	if _, err := f.CleanupExpired(t.Context()); err != nil {
		t.Fatal(err)
	}

	if len(errs) < 15 {
		t.Fatalf("only %d refusals were collected: the test did not go where it wants", len(errs))
	}
	var said strings.Builder
	for _, err := range errs {
		// Every way an error can be printed.
		fmt.Fprintf(&said, "%s\n%v\n%+v\n%#v\n", err.Error(), err, err, err)
	}
	logged := f.logs.String()

	secrets := map[string]string{
		"the password of the admin": adminPassword, "the new password": newPassword, "the reset password": resetPassword,
		"the password of alice": alicePassword, "a guessed password": guessedPassword, "a password typed as a name": misplaced,
		"a word of a password": "hunter2", "another word of a password": "Tr0ub4dor", "the scheme": "Bearer", "the name of the cookie": CookieName,
		"the prefix of a hash": "$argon2id", "the prefix of a token": "vb_",
	}
	for i, in := range signIns {
		secrets[fmt.Sprintf("the token %d", i)] = in.Token
		secrets[fmt.Sprintf("the token %d without its prefix", i)] = strings.TrimPrefix(in.Token, "vb_")
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(in.Token, "vb_"))
		if err != nil {
			t.Fatal(err)
		}
		secrets[fmt.Sprintf("the bytes of the token %d", i)] = string(raw)
	}
	for i, hash := range hashes {
		secrets[fmt.Sprintf("the hash %d", i)] = hash
		if fields := strings.Split(hash, "$"); len(fields) == 6 {
			secrets[fmt.Sprintf("the salt of the hash %d", i)] = fields[4]
			secrets[fmt.Sprintf("the digest of the hash %d", i)] = fields[5]
		}
	}
	for name, secret := range secrets {
		if strings.Contains(logged, secret) {
			t.Errorf("the log holds %s", name)
		}
		if strings.Contains(said.String(), secret) {
			t.Errorf("an error holds %s", name)
		}
	}

	// What the log does say of the sign-ins (§11.5): the name and the
	// address, at INFO when accepted and at WARN when refused.
	var accepted, refused, unnamed int
	for _, ev := range f.logs.events(t) {
		switch ev["msg"] {
		case "signed in":
			accepted++
			if ev["level"] != "INFO" || ev["remote_addr"] != remoteAddr || (ev["username"] != "admin" && ev["username"] != "alice") || ev["user_id"] == "" {
				t.Errorf("an accepted sign-in is logged as %v", ev)
			}
		case "sign-in refused":
			refused++
			if ev["level"] != "WARN" || ev["remote_addr"] != remoteAddr {
				t.Errorf("a refused sign-in is logged as %v", ev)
			}
			switch ev["username"] {
			case "admin", "alice", "nobody":
			case nil:
				// What was sent as a name cannot be one: it is not logged.
				unnamed++
			default:
				t.Errorf("a refused sign-in logged a name that is none")
			}
		}
		for key := range ev {
			switch key {
			case "time", "level", "msg", "code", "err", "username", "user_id", "role", "kind", "remote_addr", "sessions",
				"request_id", "method", "route", "status", "duration_ms", "bytes":
			default:
				t.Errorf("the log has the field %q", key)
			}
		}
	}
	if accepted != 3 || refused != 5 || unnamed != 2 {
		t.Fatalf("the log has %d accepted and %d refused sign-ins, %d without a name; want 3, 5 and 2", accepted, refused, unnamed)
	}
}
