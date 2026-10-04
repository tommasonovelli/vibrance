package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"vibrance/internal/app"
	"vibrance/internal/auth"
)

// The passwords of these tests. They protect nothing.
const (
	firstPassword  = "the first password of anna"
	secondPassword = "the second password of anna"
)

// runUser runs `vibrance user args...` in this process, on the state folder
// dir, with stdin as standard input. It returns the exit code, what was
// written on standard output and the log.
func runUser(t *testing.T, dir string, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, logs bytes.Buffer
	code := user(args, nonRoot, strings.NewReader(stdin), &stdout, dir, newLogger(&logs))
	return code, stdout.String(), logs.String()
}

// accounts opens the accounts of dir as the server would.
func accounts(t *testing.T, dir string) *auth.Service {
	t.Helper()
	s, closeAccounts, err := app.OpenAccounts(t.Context(), dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeAccounts(); err != nil {
			t.Error(err)
		}
	})
	return s
}

// DESIGN.md §7.4: create, list and reset-password, the password from
// standard input only.
func TestUserCommands(t *testing.T) {
	dir := t.TempDir()
	if code, out, logs := runUser(t, dir, firstPassword+"\n", "create", "--username", "anna", "--role", "admin", "--password-stdin"); code != exitOK || out != "" {
		t.Fatalf("create: exit %d, stdout %q, log %s", code, out, logs)
	}
	if code, _, _ := runUser(t, dir, secondPassword, "create", "--username=bob", "--role=user", "--password-stdin"); code != exitOK {
		t.Fatalf("create bob: exit %d", code)
	}
	code, out, _ := runUser(t, dir, "", "list")
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if code != exitOK || len(lines) != 3 || !strings.HasPrefix(lines[0], "USERNAME") ||
		!strings.HasPrefix(lines[1], "anna ") || !strings.Contains(lines[1], " admin ") || !strings.Contains(lines[1], " enabled ") ||
		!strings.HasPrefix(lines[2], "bob ") || !strings.Contains(lines[2], " user ") {
		t.Fatalf("list: exit %d:\n%s", code, out)
	}

	// The line break of `echo` is not part of the password; the password
	// works, and a session of it lives until the reset.
	s := accounts(t, dir)
	in, err := s.Login(t.Context(), "anna", firstPassword, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runUser(t, dir, secondPassword+"\r\n", "reset-password", "--username", "anna", "--password-stdin"); code != exitOK {
		t.Fatalf("reset-password: exit %d", code)
	}
	if _, err := s.Authenticate(t.Context(), in.Session.Kind, in.Token); err == nil {
		t.Fatal("the session of anna lives after the reset of her password")
	}
	if _, err := s.Login(t.Context(), "anna", firstPassword, "", ""); err == nil {
		t.Fatal("the old password still works")
	}
	if _, err := s.Login(t.Context(), "anna", secondPassword, "", ""); err != nil {
		t.Fatalf("the new password does not work: %v", err)
	}
}

// What is refused, with which exit code (§11.4): 2 for the arguments and
// for a name or a password that is not valid, 1 for what the database
// refuses. Nothing is changed, and no password is logged.
func TestUserRefusals(t *testing.T) {
	dir := t.TempDir()
	if code, _, _ := runUser(t, dir, firstPassword, "create", "--username", "anna", "--role", "user", "--password-stdin"); code != exitOK {
		t.Fatal("creating anna failed")
	}
	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
		exit  int
		code  string
	}{
		{"nothing", "", nil, exitUsage, "usage"},
		{"an unknown command", "", []string{"delete", "--username", "anna"}, exitUsage, "usage"},
		{"the password as an argument", "", []string{"create", "--username", "carl", "--role", "user", "--password", secondPassword}, exitUsage, "usage"},
		{"the password after the flags", secondPassword, []string{"create", "--username", "carl", "--role", "user", "--password-stdin", secondPassword}, exitUsage, "usage"},
		{"no --password-stdin", secondPassword, []string{"create", "--username", "carl", "--role", "user"}, exitUsage, "usage"},
		{"no role", secondPassword, []string{"create", "--username", "carl", "--password-stdin"}, exitUsage, "usage"},
		{"another role", secondPassword, []string{"create", "--username", "carl", "--role", "root", "--password-stdin"}, exitUsage, "usage"},
		{"no name", secondPassword, []string{"create", "--role", "user", "--password-stdin"}, exitUsage, "usage"},
		{"reset without a name", secondPassword, []string{"reset-password", "--password-stdin"}, exitUsage, "usage"},
		{"reset with a role", secondPassword, []string{"reset-password", "--username", "anna", "--role", "user", "--password-stdin"}, exitUsage, "usage"},
		{"list with an argument", "", []string{"list", "anna"}, exitUsage, "usage"},
		{"help", "", []string{"create", "--help"}, exitUsage, "usage"},
		{"a name in upper case", secondPassword, []string{"create", "--username", "Carl", "--role", "user", "--password-stdin"}, exitUsage, auth.CodeUsernameInvalid},
		{"a short password", "short\n", []string{"create", "--username", "carl", "--role", "user", "--password-stdin"}, exitUsage, auth.CodePasswordInvalid},
		{"no password at all", "", []string{"create", "--username", "carl", "--role", "user", "--password-stdin"}, exitUsage, auth.CodePasswordInvalid},
		{"two lines", secondPassword + "\n" + secondPassword + "\n", []string{"create", "--username", "carl", "--role", "user", "--password-stdin"}, exitUsage, auth.CodePasswordInvalid},
		{"a password too long", strings.Repeat("a", 2000), []string{"create", "--username", "carl", "--role", "user", "--password-stdin"}, exitUsage, auth.CodePasswordInvalid},
		{"a name that is taken", secondPassword, []string{"create", "--username", "anna", "--role", "admin", "--password-stdin"}, exitFailure, auth.CodeUsernameTaken},
		{"reset of nobody", secondPassword, []string{"reset-password", "--username", "nobody", "--password-stdin"}, exitFailure, auth.CodeUserNotFound},
		{"reset with a short password", "short", []string{"reset-password", "--username", "anna", "--password-stdin"}, exitUsage, auth.CodePasswordInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, logs := runUser(t, dir, tc.stdin, tc.args...)
			if code != tc.exit || out != "" {
				t.Fatalf("exit %d, stdout %q; want exit %d", code, out, tc.exit)
			}
			wantLog(t, []byte(logs), tc.code)
			for _, secret := range []string{firstPassword, secondPassword, "short"} {
				if strings.Contains(logs, secret) {
					t.Fatal("the log holds a password")
				}
			}
		})
	}
	// Nothing was changed: anna has her password, and carl does not exist.
	s := accounts(t, dir)
	if _, err := s.Login(t.Context(), "anna", firstPassword, "", ""); err != nil {
		t.Fatalf("the password of anna changed: %v", err)
	}
	users, err := s.ListUsers(t.Context())
	if err != nil || len(users) != 1 {
		t.Fatalf("%d accounts: %v", len(users), err)
	}
}

// Root and a name that cannot be one are refused before anything is read or
// opened, and a standard input
// that fails is a failure that says nothing of what was read.
func TestUserRootAndBrokenInput(t *testing.T) {
	dir := t.TempDir()
	var stdout, logs bytes.Buffer
	code := user([]string{"list"}, 0, strings.NewReader(""), &stdout, dir, newLogger(&logs))
	if code != exitUsage {
		t.Fatalf("root: exit %d", code)
	}
	wantLog(t, logs.Bytes(), app.CodeRunAsRoot)
	wantEmpty(t, dir)

	// A name that cannot be one is refused before the password is read and
	// before the database is created (§11.4).
	logs.Reset()
	unread := iotest.ErrReader(errors.New("the input was read"))
	code = user([]string{"create", "--username", "Not A Name", "--role", "user", "--password-stdin"}, nonRoot, unread, &stdout, dir, newLogger(&logs))
	if code != exitUsage {
		t.Fatalf("an invalid name: exit %d", code)
	}
	wantLog(t, logs.Bytes(), auth.CodeUsernameInvalid)
	wantEmpty(t, dir)

	logs.Reset()
	broken := io.MultiReader(strings.NewReader(firstPassword), iotest.ErrReader(errors.New("the input of "+secondPassword)))
	code = user([]string{"create", "--username", "anna", "--role", "user", "--password-stdin"}, nonRoot, broken, &stdout, dir, newLogger(&logs))
	if code != exitFailure || strings.Contains(logs.String(), firstPassword) || strings.Contains(logs.String(), secondPassword) {
		t.Fatalf("a broken input: exit %d, log %s", code, logs.String())
	}
	wantEmpty(t, dir)
}

// The production cost is what the subcommand hashes with: the hash it
// writes says m=65536,t=3,p=1, and the server reads it.
func TestUserHashesWithTheProductionCost(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	if code, _, _ := runUser(t, dir, firstPassword, "create", "--username", "anna", "--role", "user", "--password-stdin"); code != exitOK {
		t.Fatal("creating anna failed")
	}
	s := accounts(t, dir)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if _, err := s.Login(ctx, "anna", firstPassword, "", ""); err != nil {
		t.Fatalf("the server cannot read the hash of the subcommand: %v", err)
	}
	t.Logf("create and sign in took %s", time.Since(start))
	db, err := sql.Open("sqlite", filepath.Join(dir, "vibrance.db"))
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	scanErr := db.QueryRowContext(t.Context(), `SELECT password_hash FROM users WHERE username = 'anna'`).Scan(&hash)
	if err := errors.Join(scanErr, db.Close()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=1$") {
		t.Fatal("the hash of the subcommand is not of the production cost")
	}
}

// wantEmpty fails the test if dir holds anything.
func wantEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s holds %d entries, want none", dir, len(entries))
	}
}
