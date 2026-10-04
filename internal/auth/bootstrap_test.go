package auth

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// wantBootstrapRefusal checks that err is the refusal of Bootstrap with
// that code, and returns its message.
func wantBootstrapRefusal(t *testing.T, err error, code string) string {
	t.Helper()
	var e *BootstrapError
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("Bootstrap returned %v, want the refusal %s", err, code)
	}
	return e.Error()
}

// DESIGN.md §7.4: a server without accounts creates the admin from the two
// variables, with the name `admin` when none is given.
func TestBootstrapCreatesTheFirstAdmin(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"the default name", map[string]string{"VIBRANCE_ADMIN_PASSWORD": alicePassword}, "admin"},
		{"an empty name is an unset one", map[string]string{"VIBRANCE_ADMIN_USERNAME": "", "VIBRANCE_ADMIN_PASSWORD": alicePassword}, "admin"},
		{"a name", map[string]string{"VIBRANCE_ADMIN_USERNAME": "tommaso", "VIBRANCE_ADMIN_PASSWORD": alicePassword}, "tommaso"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if err := f.Bootstrap(t.Context(), envOf(tc.env)); err != nil {
				t.Fatal(err)
			}
			users, err := f.ListUsers(t.Context())
			if err != nil || len(users) != 1 {
				t.Fatalf("%d accounts after the bootstrap: %v", len(users), err)
			}
			if u := users[0]; u.Username != tc.want || u.Role != RoleAdmin || u.Disabled || u.CreatedAt != f.clock.Now() {
				t.Fatalf("the first account is %+v", u)
			}
			if in := f.login(t, tc.want, alicePassword); in.User.Role != RoleAdmin {
				t.Fatal("the first admin does not sign in as an admin")
			}
			if !strings.HasPrefix(f.passwordHash(t, tc.want), "$argon2id$v=19$") {
				t.Fatal("the password of the first admin is not stored as a PHC argon2id hash")
			}
			if strings.Contains(f.logs.String(), alicePassword) {
				t.Fatal("the log holds the password of the admin")
			}
		})
	}
}

// Without accounts and without a valid password the server does not start:
// the refusal has a code, and never repeats the value of a variable.
func TestBootstrapRefuses(t *testing.T) {
	const secret = "s3cret\tvalue-of-the-variable"
	for _, tc := range []struct {
		name string
		env  map[string]string
		code string
		want string
	}{
		{"no variable", nil, CodeAdminPasswordMissing, "VIBRANCE_ADMIN_PASSWORD is required"},
		{"an empty password", map[string]string{"VIBRANCE_ADMIN_PASSWORD": ""}, CodeAdminPasswordMissing, "VIBRANCE_ADMIN_PASSWORD is required"},
		{"only a name", map[string]string{"VIBRANCE_ADMIN_USERNAME": "tommaso"}, CodeAdminPasswordMissing, "VIBRANCE_ADMIN_PASSWORD is required"},
		{"a short password", map[string]string{"VIBRANCE_ADMIN_PASSWORD": "short"}, CodeAdminPasswordInvalid, "VIBRANCE_ADMIN_PASSWORD: A password has at least 12 bytes."},
		{"eleven bytes", map[string]string{"VIBRANCE_ADMIN_PASSWORD": "elevenbytes"}, CodeAdminPasswordInvalid, "at least 12 bytes"},
		{"a long password", map[string]string{"VIBRANCE_ADMIN_PASSWORD": strings.Repeat("a", 1025)}, CodeAdminPasswordInvalid, "at most 1024 bytes"},
		{"a control character", map[string]string{"VIBRANCE_ADMIN_PASSWORD": secret}, CodeAdminPasswordInvalid, "no control characters"},
		{"a line break left in .env", map[string]string{"VIBRANCE_ADMIN_PASSWORD": "a good password\r"}, CodeAdminPasswordInvalid, "no control characters"},
		{"not UTF-8", map[string]string{"VIBRANCE_ADMIN_PASSWORD": "a good password\xff"}, CodeAdminPasswordInvalid, "valid UTF-8"},
		{"a name in upper case", map[string]string{"VIBRANCE_ADMIN_USERNAME": "Admin", "VIBRANCE_ADMIN_PASSWORD": alicePassword}, CodeAdminUsernameInvalid, "VIBRANCE_ADMIN_USERNAME: A user name has 3 to 32 characters"},
		{"a short name", map[string]string{"VIBRANCE_ADMIN_USERNAME": "ab", "VIBRANCE_ADMIN_PASSWORD": alicePassword}, CodeAdminUsernameInvalid, "VIBRANCE_ADMIN_USERNAME: "},
		{"an invalid name and no password", map[string]string{"VIBRANCE_ADMIN_USERNAME": "a b"}, CodeAdminUsernameInvalid, "VIBRANCE_ADMIN_USERNAME: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			msg := wantBootstrapRefusal(t, f.Bootstrap(t.Context(), envOf(tc.env)), tc.code)
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("the refusal %q does not contain %q", msg, tc.want)
			}
			for _, value := range tc.env {
				if len(value) >= 5 && strings.Contains(msg+f.logs.String(), value) {
					t.Fatal("the refusal or the log repeats the value of a variable")
				}
			}
			if users, err := f.ListUsers(t.Context()); err != nil || len(users) != 0 {
				t.Fatalf("a refused bootstrap left %d accounts: %v", len(users), err)
			}
			if f.logs.String() != "" {
				t.Fatalf("a refused bootstrap logged:\n%s", f.logs)
			}
		})
	}
}

// With any account in the database the two variables are ignored: missing,
// not valid, or those of another admin, nothing is refused and nothing is
// created or changed.
func TestBootstrapIgnoresTheVariablesOnceThereIsAUser(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"no variable":              nil,
		"an invalid password":      {"VIBRANCE_ADMIN_PASSWORD": "short"},
		"an invalid name":          {"VIBRANCE_ADMIN_USERNAME": "Not A Name", "VIBRANCE_ADMIN_PASSWORD": "x"},
		"another admin":            {"VIBRANCE_ADMIN_USERNAME": "root", "VIBRANCE_ADMIN_PASSWORD": otherPassword},
		"a new password for alice": {"VIBRANCE_ADMIN_USERNAME": "alice", "VIBRANCE_ADMIN_PASSWORD": otherPassword},
	} {
		for _, role := range []string{RoleAdmin, RoleUser} {
			t.Run(name+" with one "+role, func(t *testing.T) {
				f := newFixture(t)
				alice := f.user(t, "alice", alicePassword, role)
				in := f.login(t, "alice", alicePassword)
				hash := f.passwordHash(t, "alice")
				if err := f.Bootstrap(t.Context(), envOf(env)); err != nil {
					t.Fatalf("Bootstrap with an account in the database: %v", err)
				}
				users, err := f.ListUsers(t.Context())
				if err != nil || len(users) != 1 || users[0] != alice {
					t.Fatalf("the accounts after the bootstrap are %+v: %v", users, err)
				}
				if f.passwordHash(t, "alice") != hash {
					t.Fatal("the bootstrap replaced the password of an account")
				}
				f.principal(t, in)
				_, err = f.Login(t.Context(), "alice", otherPassword, "", "")
				wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
			})
		}
	}
	// Even an account that is disabled counts: the table is not empty.
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleAdmin)
	f.exec(t, `UPDATE users SET disabled = 1`)
	if err := f.Bootstrap(t.Context(), envOf(nil)); err != nil {
		t.Fatal(err)
	}
}

// Twice, and by several processes at once: one admin.
func TestBootstrapConcurrently(t *testing.T) {
	f := newFixture(t)
	env := envOf(map[string]string{"VIBRANCE_ADMIN_PASSWORD": alicePassword})
	var wg sync.WaitGroup
	for range 10 {
		s := f.service(t)
		wg.Go(func() {
			if err := s.Bootstrap(t.Context(), env); err != nil {
				t.Errorf("a concurrent bootstrap failed: %v", err)
			}
		})
	}
	wg.Wait()
	if err := f.Bootstrap(t.Context(), env); err != nil {
		t.Fatal(err)
	}
	users, err := f.ListUsers(t.Context())
	if err != nil || len(users) != 1 || users[0].Username != "admin" {
		t.Fatalf("the accounts after ten bootstraps are %+v: %v", users, err)
	}
	created := 0
	for _, ev := range f.logs.events(t) {
		if ev["msg"] == "first admin created" {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("%d bootstraps say they created the admin", created)
	}
	f.login(t, "admin", alicePassword)
}
