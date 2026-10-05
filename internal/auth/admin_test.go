package auth

import (
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// adminOf signs in as an account and returns the Principal of its session.
func (f *fixture) adminOf(t *testing.T, username, password string) Principal {
	t.Helper()
	return f.principal(t, f.login(t, username, password))
}

// account reads an account, and fails the test if it is not there.
func (f *fixture) account(t *testing.T, id string) User {
	t.Helper()
	u, err := f.GetUser(t.Context(), id)
	if err != nil {
		t.Fatalf("reading the account %s: %v", id, err)
	}
	return u
}

func TestGetUserAndMe(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", alicePassword, RoleUser)
	got := f.account(t, alice.ID)
	if got != alice {
		t.Fatalf("GetUser = %+v, want %+v", got, alice)
	}
	_, err := f.GetUser(t.Context(), uuid.NewString())
	wantRefusal(t, err, http.StatusNotFound, CodeUserNotFound)

	p := f.principal(t, f.login(t, "alice", alicePassword))
	if me, err := f.Me(t.Context(), p); err != nil || me != alice {
		t.Fatalf("Me = %+v, %v; want %+v", me, err, alice)
	}
	// Deleted after the request was authenticated: the session is gone too.
	f.exec(t, `DELETE FROM users WHERE id = ?`, alice.ID)
	_, err = f.Me(t.Context(), p)
	wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
}

// §7.5: a role and a state, as sent; disabling revokes the sessions at once,
// and the account cannot sign in until it is enabled again.
func TestUpdateUser(t *testing.T) {
	f := newFixture(t)
	f.user(t, "root", alicePassword, RoleAdmin)
	admin := f.adminOf(t, "root", alicePassword)
	bob := f.user(t, "bob", bobPassword, RoleUser)
	sessions := []SignIn{f.login(t, "bob", bobPassword), f.token(t, "bob", bobPassword)}

	promoted, err := f.UpdateUser(t.Context(), admin, bob.ID, RoleAdmin, false)
	if err != nil || promoted.Role != RoleAdmin || promoted.Disabled || promoted.ID != bob.ID || promoted.Username != "bob" || !promoted.CreatedAt.Equal(bob.CreatedAt) {
		t.Fatalf("promoting bob: %+v, %v", promoted, err)
	}
	// A change of role keeps the sessions: the role is read at each request.
	for _, in := range sessions {
		if p := f.principal(t, in); p.Role != RoleAdmin {
			t.Fatalf("a session of bob has the role %s", p.Role)
		}
	}

	disabled, err := f.UpdateUser(t.Context(), admin, bob.ID, RoleUser, true)
	if err != nil || disabled.Role != RoleUser || !disabled.Disabled {
		t.Fatalf("disabling bob: %+v, %v", disabled, err)
	}
	if got := f.account(t, bob.ID); got != disabled {
		t.Fatalf("the account is %+v, the answer %+v", got, disabled)
	}
	for _, in := range sessions {
		_, err := f.Authenticate(t.Context(), in.Session.Kind, in.Token)
		wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
	}
	for _, r := range f.sessions(t) {
		if r.UserID == bob.ID {
			t.Fatal("a session of the disabled account is still in the database")
		}
	}
	_, err = f.Login(t.Context(), "bob", bobPassword, "", "")
	wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)

	if enabled, err := f.UpdateUser(t.Context(), admin, bob.ID, RoleUser, false); err != nil || enabled.Disabled {
		t.Fatalf("enabling bob: %+v, %v", enabled, err)
	}
	f.login(t, "bob", bobPassword)

	_, err = f.UpdateUser(t.Context(), admin, uuid.NewString(), RoleUser, false)
	wantRefusal(t, err, http.StatusNotFound, CodeUserNotFound)
	if _, err := f.UpdateUser(t.Context(), admin, bob.ID, "root", false); err == nil || refusalCode(err) != "" {
		t.Fatalf("an unknown role: %v, want an internal error", err)
	}
	updates := 0
	for _, ev := range f.logs.events(t) {
		if ev["msg"] == "account updated" {
			updates++
			if ev["by"] != admin.UserID || ev["user_id"] != bob.ID {
				t.Errorf("the log line %v does not say who changed which account", ev)
			}
		}
	}
	if updates != 3 {
		t.Fatalf("%d updates logged, want 3", updates)
	}
}

// §7.5: the last enabled admin cannot be demoted, disabled or deleted, by
// anyone, and a refused change changes nothing. A disabled admin does not
// count.
func TestLastAdmin(t *testing.T) {
	f := newFixture(t)
	root := f.user(t, "root", alicePassword, RoleAdmin)
	self := f.adminOf(t, "root", alicePassword)
	off := f.user(t, "off", bobPassword, RoleAdmin)
	if _, err := f.UpdateUser(t.Context(), self, off.ID, RoleAdmin, true); err != nil {
		t.Fatal(err)
	}
	// A principal whose role changed after its request was authenticated:
	// the rule holds whoever asks.
	stale := Principal{UserID: off.ID, Role: RoleAdmin}

	for name, actor := range map[string]Principal{"the admin itself": self, "another account": stale} {
		for change, err := range map[string]error{
			"demote":  second(f.UpdateUser(t.Context(), actor, root.ID, RoleUser, false)),
			"disable": second(f.UpdateUser(t.Context(), actor, root.ID, RoleAdmin, true)),
			"both":    second(f.UpdateUser(t.Context(), actor, root.ID, RoleUser, true)),
			"delete":  f.DeleteUser(t.Context(), actor, root.ID),
		} {
			if refusalCode(err) != CodeLastAdmin {
				t.Errorf("%s, %s: %v, want 409 last_admin", name, change, err)
			}
		}
	}
	wantRefusal(t, f.DeleteUser(t.Context(), self, root.ID), http.StatusConflict, CodeLastAdmin)
	if got := f.account(t, root.ID); got.Role != RoleAdmin || got.Disabled {
		t.Fatalf("a refused change changed the admin: %+v", got)
	}
	f.principal(t, f.login(t, "root", alicePassword))

	// The same values are no change: allowed.
	if _, err := f.UpdateUser(t.Context(), self, root.ID, RoleAdmin, false); err != nil {
		t.Fatalf("setting the values it has: %v", err)
	}
	// The disabled admin is not the last one: it can go.
	if err := f.DeleteUser(t.Context(), self, off.ID); err != nil {
		t.Fatalf("deleting a disabled admin: %v", err)
	}
}

func second[T any](_ T, err error) error { return err }

// §7.5: an admin cannot delete or disable their own account; demoting it is
// allowed while another enabled admin is left.
func TestCannotModifySelf(t *testing.T) {
	f := newFixture(t)
	root := f.user(t, "root", alicePassword, RoleAdmin)
	self := f.adminOf(t, "root", alicePassword)
	other := f.user(t, "other", bobPassword, RoleAdmin)

	_, err := f.UpdateUser(t.Context(), self, root.ID, RoleAdmin, true)
	wantRefusal(t, err, http.StatusConflict, CodeCannotModifySelf)
	wantRefusal(t, f.DeleteUser(t.Context(), self, root.ID), http.StatusConflict, CodeCannotModifySelf)
	if got := f.account(t, root.ID); got.Role != RoleAdmin || got.Disabled {
		t.Fatalf("a refused change changed the account: %+v", got)
	}
	f.principal(t, f.login(t, "root", alicePassword))

	demoted, err := f.UpdateUser(t.Context(), self, root.ID, RoleUser, false)
	if err != nil || demoted.Role != RoleUser {
		t.Fatalf("demoting oneself with another admin left: %+v, %v", demoted, err)
	}
	// Now other is the last enabled admin.
	otherAdmin := f.adminOf(t, "other", bobPassword)
	_, err = f.UpdateUser(t.Context(), otherAdmin, other.ID, RoleUser, false)
	wantRefusal(t, err, http.StatusConflict, CodeLastAdmin)
}

// §12, S14: two admins who delete each other at the same moment: one of the
// two deletions wins, the other is refused, and one admin is left. The same
// for two who demote or disable each other.
func TestAdminsChangeEachOtherConcurrently(t *testing.T) {
	for name, change := range map[string]func(f *fixture, actor Principal, id string) error{
		"delete": func(f *fixture, actor Principal, id string) error { return f.DeleteUser(t.Context(), actor, id) },
		"demote": func(f *fixture, actor Principal, id string) error {
			return second(f.UpdateUser(t.Context(), actor, id, RoleUser, false))
		},
		"disable": func(f *fixture, actor Principal, id string) error {
			return second(f.UpdateUser(t.Context(), actor, id, RoleAdmin, true))
		},
	} {
		t.Run(name, func(t *testing.T) {
			for range 10 {
				f := newFixture(t)
				a := f.user(t, "anna", alicePassword, RoleAdmin)
				b := f.user(t, "bert", bobPassword, RoleAdmin)
				pa, pb := f.adminOf(t, "anna", alicePassword), f.adminOf(t, "bert", bobPassword)
				var wg sync.WaitGroup
				errs := make([]error, 2)
				start := make(chan struct{})
				wg.Go(func() { <-start; errs[0] = change(f, pa, b.ID) })
				wg.Go(func() { <-start; errs[1] = change(f, pb, a.ID) })
				close(start)
				wg.Wait()
				won, refused := 0, 0
				for _, err := range errs {
					switch {
					case err == nil:
						won++
					case refusalCode(err) == CodeLastAdmin:
						refused++
					default:
						t.Fatalf("an unexpected error: %v", err)
					}
				}
				if won != 1 || refused != 1 {
					t.Fatalf("%d changes won and %d were refused, want one each", won, refused)
				}
				var admins int
				f.query(t, `SELECT count(*) FROM users WHERE role = 'admin' AND disabled = 0`, &admins)
				if admins != 1 {
					t.Fatalf("%d enabled admins are left, want 1", admins)
				}
			}
		})
	}
}

// §7.5: deleting an account deletes its sessions, its favorites and its
// playlists with their items, and nothing of anyone else; the tracks stay.
func TestDeleteUserCascades(t *testing.T) {
	f := newFixture(t)
	f.user(t, "root", alicePassword, RoleAdmin)
	admin := f.adminOf(t, "root", alicePassword)
	bob := f.user(t, "bob", bobPassword, RoleUser)
	carl := f.user(t, "carl", otherPassword, RoleUser)
	gone := []SignIn{f.login(t, "bob", bobPassword), f.token(t, "bob", bobPassword)}
	kept := f.login(t, "carl", otherPassword)

	f.exec(t, `INSERT INTO artists (id, name, sort_key) VALUES ('a', 'A', x'00')`)
	f.exec(t, `INSERT INTO albums (id, artist_id, artist_key, title, title_key, year_key, compilation, rel_path,
		album_revision, render_version, receipt_hash, track_count, duration_ms, available, first_seen_at, updated_at)
		VALUES ('b', 'a', x'00', 'B', x'00', 10000, 0, 'A/B', 1, '1', 'h', 1, 1, 1, 1, 1)`)
	f.exec(t, `INSERT INTO tracks (id, album_id, fingerprint, fp_version, disc, "no", title, artist, rel_path, file_size,
		file_mtime_ns, file_sha256, codec, sample_rate, channels, available, updated_at)
		VALUES ('t', 'b', 'f', 'v', 1, 1, 'T', 'A', 'A/B/t.flac', 1, 1, 's', 'flac', 44100, 2, 1, 1)`)
	for _, owner := range []string{bob.ID, carl.ID} {
		f.exec(t, `INSERT INTO favorites (user_id, track_id, created_at) VALUES (?, 't', 1)`, owner)
		f.exec(t, `INSERT INTO playlists (id, user_id, name, revision, created_at, updated_at) VALUES (?, ?, 'P', 1, 1, 1)`, "p-"+owner, owner)
		f.exec(t, `INSERT INTO playlist_items (id, playlist_id, track_id, position, added_at) VALUES (?, ?, 't', 0, 1)`, "i-"+owner, "p-"+owner)
	}

	if err := f.DeleteUser(t.Context(), admin, bob.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.GetUser(t.Context(), bob.ID)
	wantRefusal(t, err, http.StatusNotFound, CodeUserNotFound)
	for _, in := range gone {
		_, err := f.Authenticate(t.Context(), in.Session.Kind, in.Token)
		wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
	}
	f.principal(t, kept)
	for table, owner := range map[string]string{
		"sessions": "user_id", "favorites": "user_id", "playlists": "user_id",
		"playlist_items": "(SELECT user_id FROM playlists WHERE id = playlist_id)",
	} {
		var of, others, all int
		f.query(t, `SELECT count(*) FROM `+table+` WHERE `+owner+` = '`+bob.ID+`'`, &of)
		f.query(t, `SELECT count(*) FROM `+table+` WHERE `+owner+` = '`+carl.ID+`'`, &others)
		f.query(t, `SELECT count(*) FROM `+table, &all)
		// The sessions of the admin are the rest.
		if of != 0 || others != 1 || (table != "sessions" && all != 1) {
			t.Errorf("%s: %d rows of the deleted account, %d of carl, %d in all; want 0, 1 and only those of carl", table, of, others, all)
		}
	}
	var tracks int
	f.query(t, `SELECT count(*) FROM tracks`, &tracks)
	if tracks != 1 {
		t.Fatal("the deletion of an account deleted a track")
	}
	// A playlist item whose playlist went with its owner is gone too.
	var orphans int
	f.query(t, `SELECT count(*) FROM playlist_items WHERE playlist_id NOT IN (SELECT id FROM playlists)`, &orphans)
	if orphans != 0 {
		t.Fatal("the items of a deleted playlist are left")
	}

	wantRefusal(t, f.DeleteUser(t.Context(), admin, bob.ID), http.StatusNotFound, CodeUserNotFound)
}

// §7.3: the reset of an admin, by id, revokes every session of the account.
func TestResetUserPassword(t *testing.T) {
	f := newFixture(t)
	bob := f.user(t, "bob", bobPassword, RoleUser)
	sessions := []SignIn{f.login(t, "bob", bobPassword), f.token(t, "bob", bobPassword)}
	if err := f.ResetUserPassword(t.Context(), bob.ID, otherPassword); err != nil {
		t.Fatal(err)
	}
	for _, in := range sessions {
		_, err := f.Authenticate(t.Context(), in.Session.Kind, in.Token)
		wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
	}
	_, err := f.Login(t.Context(), "bob", bobPassword, "", "")
	wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
	f.login(t, "bob", otherPassword)

	wantRefusal(t, f.ResetUserPassword(t.Context(), uuid.NewString(), otherPassword), http.StatusNotFound, CodeUserNotFound)
	wantRefusal(t, f.ResetUserPassword(t.Context(), bob.ID, "short"), http.StatusUnprocessableEntity, CodePasswordInvalid)
	f.login(t, "bob", otherPassword)
}

// A change of an account that fails for a reason that is not a refusal is
// an error with what was being done, never a refusal.
func TestAdminFailure(t *testing.T) {
	refusal := lastAdmin()
	if got := adminFailure("x", refusal); got != refusal {
		t.Fatalf("a refusal became %v", got)
	}
	cause := errors.New("disk on fire")
	if got := adminFailure("deleting an account", cause); !errors.Is(got, cause) || refusalCode(got) != "" {
		t.Fatalf("an error became %v", got)
	}
}
