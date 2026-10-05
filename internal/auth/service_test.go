package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A sign-in gives a token of the form of DESIGN.md §7.3, a session of the
// kind and of the lifetime asked for, and the account without its hash.
func TestSignIn(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", alicePassword, RoleUser)
	start := f.clock.Now()

	for _, tc := range []struct {
		name     string
		signIn   func(ctx context.Context, username, password, deviceName, remoteAddr string) (SignIn, error)
		kind     string
		lifetime time.Duration
		device   string
	}{
		{"cookie", f.Login, KindCookie, 30 * 24 * time.Hour, ""},
		{"cookie with a device", f.Login, KindCookie, 30 * 24 * time.Hour, "the browser of the kitchen"},
		{"token", f.CreateToken, KindToken, 90 * 24 * time.Hour, "phone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := tc.signIn(t.Context(), "alice", alicePassword, tc.device, "192.0.2.1:5000")
			if err != nil {
				t.Fatal(err)
			}
			raw, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(in.Token, "vb_"))
			if !strings.HasPrefix(in.Token, "vb_") || err != nil || len(raw) != 32 || len(in.Token) != 46 || !wellFormed(in.Token) {
				t.Fatal("the token is not vb_ and 32 bytes in unpadded base64url")
			}
			if in.User != alice || in.User.Disabled || in.User.Role != RoleUser || in.User.CreatedAt != start {
				t.Fatalf("the user of the sign-in is %+v, want %+v", in.User, alice)
			}
			s := in.Session
			if s.Kind != tc.kind || !s.Current || s.CreatedAt != start || s.LastUsedAt != start || s.ExpiresAt != start.Add(tc.lifetime) {
				t.Fatalf("the session is %+v", s)
			}
			if (tc.device == "") != (s.DeviceName == nil) || (s.DeviceName != nil && *s.DeviceName != tc.device) {
				t.Fatalf("the device name of the session is not %q", tc.device)
			}
			row := f.session(t, s.ID)
			if row.UserID != alice.ID || row.Kind != tc.kind || row.DeviceName.Valid != (tc.device != "") || row.DeviceName.String != tc.device ||
				row.CreatedAt != start.UnixMilli() || row.LastUsedAt != start.UnixMilli() || row.ExpiresAt != start.Add(tc.lifetime).UnixMilli() {
				t.Fatalf("the row of the session is not what was answered (kind %s, user %s)", row.Kind, row.UserID)
			}
			if p := f.principal(t, in); p != (Principal{UserID: alice.ID, Role: RoleUser, SessionID: s.ID}) {
				t.Fatalf("the principal of the session is %+v", p)
			}
		})
	}
	// Every token and every session id is new.
	seen := map[string]bool{}
	for range 50 {
		in := f.login(t, "alice", alicePassword)
		if seen[in.Token] || seen[in.Session.ID] {
			t.Fatal("a token or a session id was given twice")
		}
		seen[in.Token], seen[in.Session.ID] = true, true
	}
}

// §7.1: the sign-in compares the name in lower case.
func TestSignInLowersTheName(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice.b-c_9", alicePassword, RoleUser)
	for _, name := range []string{"alice.b-c_9", "ALICE.B-C_9", "Alice.b-C_9"} {
		if in := f.login(t, name, alicePassword); in.User.ID != alice.ID {
			t.Fatalf("%s signed in as another account", name)
		}
	}
	// The lower case of Unicode is not applied: U+212A, the Kelvin sign,
	// is not a k.
	f.user(t, "kate", bobPassword, RoleUser)
	_, err := f.Login(t.Context(), "Kate", bobPassword, "", "")
	wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
}

// I5: the database holds the SHA-256 of a token and nothing the token can
// be read from, in the rows and in every byte of its files.
func TestTokenIsNotStoredInClear(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	cookie := f.login(t, "alice", alicePassword)
	token, err := f.CreateToken(t.Context(), "alice", alicePassword, "phone", "")
	if err != nil {
		t.Fatal(err)
	}
	// Used, renewed and listed: nothing of that writes it either.
	f.clock.advance(20 * 24 * time.Hour)
	for _, in := range []SignIn{cookie, token} {
		p := f.principal(t, in)
		if _, err := f.ListSessions(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}

	for _, in := range []SignIn{cookie, token} {
		sum := sha256.Sum256([]byte(in.Token))
		row := f.session(t, in.Session.ID)
		if row.TokenHash != hex.EncodeToString(sum[:]) {
			t.Fatal("token_hash is not the SHA-256 of the token in lowercase hexadecimal")
		}
		for _, value := range []string{row.ID, row.TokenHash, row.UserID, row.Kind, row.DeviceName.String} {
			if strings.Contains(value, in.Token) || strings.Contains(value, strings.TrimPrefix(in.Token, "vb_")) {
				t.Fatal("a column of the session holds the token")
			}
		}
	}
	disk := f.databaseBytes(t)
	if len(disk) == 0 {
		t.Fatal("the database files were not read")
	}
	for _, in := range []SignIn{cookie, token} {
		secret := strings.TrimPrefix(in.Token, "vb_")
		raw, err := base64.RawURLEncoding.DecodeString(secret)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(disk, []byte(secret)) || bytes.Contains(disk, raw) {
			t.Fatal("the files of the database hold a token")
		}
		if !bytes.Contains(disk, []byte(hashToken(in.Token))) {
			t.Fatal("the files of the database do not hold the hash of a token: the test looked in the wrong place")
		}
	}
	// Nor a password: only its PHC hash.
	if bytes.Contains(disk, []byte(alicePassword)) {
		t.Fatal("the files of the database hold a password")
	}
	if hash := f.passwordHash(t, "alice"); !strings.HasPrefix(hash, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatal("password_hash is not a PHC argon2id string of the cost of the service")
	}
}

// What is not a live session is 401 login_required, and the database is
// not even asked for what has not the form of a token.
func TestAuthenticateRefuses(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	in := f.login(t, "alice", alicePassword)
	hash := f.session(t, in.Session.ID).TokenHash

	flipped := []byte(in.Token)
	if flipped[20] == 'A' {
		flipped[20] = 'B'
	} else {
		flipped[20] = 'A'
	}
	for name, token := range map[string]string{
		"nothing":                 "",
		"the prefix alone":        "vb_",
		"another token":           "vb_" + strings.Repeat("A", 43),
		"one character changed":   string(flipped),
		"without the prefix":      strings.TrimPrefix(in.Token, "vb_"),
		"another prefix":          "VB_" + strings.TrimPrefix(in.Token, "vb_"),
		"cut short":               in.Token[:45],
		"one character more":      in.Token + "A",
		"a space after":           in.Token + " ",
		"a line break after":      in.Token + "\n",
		"padded":                  in.Token + "=",
		"the hash of the token":   hash,
		"the id of the session":   in.Session.ID,
		"standard base64":         "vb_" + strings.Repeat("+", 43),
		"an injection":            "vb_' OR '1'='1" + strings.Repeat("A", 31),
		"a megabyte":              "vb_" + strings.Repeat("A", 1<<20),
		"not UTF-8":               "vb_" + strings.Repeat("\xff", 43),
		"a NUL inside":            in.Token[:10] + "\x00" + in.Token[11:],
		"the session of a cookie": CookieName + "=" + in.Token,
	} {
		_, err := f.Authenticate(t.Context(), KindCookie, token)
		wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
		if err.Error() != loginRequired().Error() {
			t.Errorf("%s: the refusal is not the one of every other", name)
		}
	}
	if p := f.principal(t, in); p.SessionID != in.Session.ID {
		t.Fatal("the token itself is refused")
	}
}

// §7.3, T16: a session expires at expires_at, and a request in the second
// half of its lifetime gives it a whole one again. The clock is the one
// the service was given.
func TestExpiryAndRenewal(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		lifetime time.Duration
	}{{KindCookie, 30 * 24 * time.Hour}, {KindToken, 90 * 24 * time.Hour}} {
		t.Run(tc.kind, func(t *testing.T) {
			f := newFixture(t)
			f.user(t, "alice", alicePassword, RoleUser)
			signIn := f.Login
			if tc.kind == KindToken {
				signIn = f.CreateToken
			}
			in, err := signIn(t.Context(), "alice", alicePassword, "device", "")
			if err != nil {
				t.Fatal(err)
			}
			start := f.clock.Now()
			expires := func() time.Time { return time.UnixMilli(f.session(t, in.Session.ID).ExpiresAt).UTC() }
			half := tc.lifetime / 2

			// The first half: used, never renewed.
			for _, at := range []time.Duration{time.Hour, half - time.Hour, half} {
				f.clock.advance(start.Add(at).Sub(f.clock.Now()))
				f.principal(t, in)
				if got := expires(); got != start.Add(tc.lifetime) {
					t.Fatalf("after %s the session expires at %s: it was renewed before half its lifetime", at, got)
				}
			}
			// More than half: a whole lifetime from the request.
			f.clock.advance(time.Millisecond)
			renewed := f.clock.Now()
			f.principal(t, in)
			if got := expires(); got != renewed.Add(tc.lifetime) {
				t.Fatalf("after half its lifetime the session expires at %s, want %s", got, renewed.Add(tc.lifetime))
			}
			// The instant before the new expiry it lives, and is renewed
			// again: a session that is used never ends.
			f.clock.advance(tc.lifetime - time.Millisecond)
			f.principal(t, in)
			again := f.clock.Now()
			if got := expires(); got != again.Add(tc.lifetime) {
				t.Fatalf("the session expires at %s, want %s", got, again.Add(tc.lifetime))
			}
			// At its expiry it is dead, and stays dead.
			f.clock.advance(tc.lifetime)
			_, err = f.Authenticate(t.Context(), in.Session.Kind, in.Token)
			wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
			if got := expires(); got != again.Add(tc.lifetime) {
				t.Fatal("a request with an expired session renewed it")
			}
			f.clock.advance(-time.Millisecond)
			f.principal(t, in)
			f.clock.advance(100 * 24 * time.Hour)
			_, err = f.Authenticate(t.Context(), in.Session.Kind, in.Token)
			wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
		})
	}
}

// A session that is never used expires a lifetime after its creation.
func TestUnusedSessionExpires(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	in := f.login(t, "alice", alicePassword)
	f.clock.advance(CookieLifetime - time.Millisecond)
	other := f.login(t, "alice", alicePassword)
	f.clock.advance(time.Millisecond)
	_, err := f.Authenticate(t.Context(), in.Session.Kind, in.Token)
	wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
	f.principal(t, other)
}

// §7.3, T16: last_used_at is written at most every ten minutes, so most
// requests write nothing (there is one writer).
func TestLastUsedIsWrittenAtMostEveryTenMinutes(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	in := f.login(t, "alice", alicePassword)
	start := f.clock.Now()
	lastUsed := func() time.Time { return time.UnixMilli(f.session(t, in.Session.ID).LastUsedAt).UTC() }

	// A request every 30 seconds for an hour.
	var writes []time.Duration
	previous := start
	for range 120 {
		f.clock.advance(30 * time.Second)
		f.principal(t, in)
		if got := lastUsed(); got != previous {
			if got != f.clock.Now() {
				t.Fatalf("last_used_at was set to %s at %s", got, f.clock.Now())
			}
			writes = append(writes, got.Sub(previous))
			previous = got
		}
	}
	if len(writes) != 6 {
		t.Fatalf("last_used_at was written %d times in an hour of requests, want 6", len(writes))
	}
	for _, gap := range writes {
		if gap != 10*time.Minute {
			t.Fatalf("last_used_at was written after %s, want every 10 minutes", gap)
		}
	}
	// One millisecond short of ten minutes writes nothing; ten do.
	f.clock.advance(10*time.Minute - time.Millisecond)
	f.principal(t, in)
	if lastUsed() != previous {
		t.Fatal("last_used_at was written before ten minutes had passed")
	}
	f.clock.advance(time.Millisecond)
	f.principal(t, in)
	if lastUsed() != f.clock.Now() {
		t.Fatal("last_used_at was not written after ten minutes")
	}
	// The renewal did not come: an hour is far from half the lifetime.
	if got := f.session(t, in.Session.ID).ExpiresAt; got != start.Add(CookieLifetime).UnixMilli() {
		t.Fatal("the use of the session changed its expiry")
	}
	// A request that writes nothing changes no byte of the row, and one
	// with the clock set back never moves last_used_at back.
	f.clock.advance(-time.Hour)
	before := f.session(t, in.Session.ID)
	f.principal(t, in)
	if f.session(t, in.Session.ID) != before {
		t.Fatal("a request with the clock set back wrote the session")
	}
}

// Requests at the same instant on one session: all authenticated, and the
// row holds the latest values, never an older one.
func TestAuthenticateConcurrently(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", alicePassword, RoleUser)
	in := f.login(t, "alice", alicePassword)
	f.clock.advance(20 * 24 * time.Hour) // both writes are due
	now := f.clock.Now()

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			p, err := f.Authenticate(t.Context(), in.Session.Kind, in.Token)
			if err != nil || p.UserID != alice.ID {
				t.Errorf("a concurrent request was not authenticated: %v", err)
			}
		})
	}
	wg.Wait()
	row := f.session(t, in.Session.ID)
	if row.LastUsedAt != now.UnixMilli() || row.ExpiresAt != now.Add(CookieLifetime).UnixMilli() {
		t.Fatal("the session has not the use and the expiry of the requests")
	}
}

// §7.3: a disabled account is refused at once, with the sessions it has,
// and cannot sign in; enabled again, its sessions and its password work.
func TestDisabledUser(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	f.user(t, "bob", bobPassword, RoleAdmin)
	alice := f.login(t, "alice", alicePassword)
	bob := f.login(t, "bob", bobPassword)

	f.exec(t, `UPDATE users SET disabled = 1 WHERE username = 'alice'`)
	_, err := f.Authenticate(t.Context(), alice.Session.Kind, alice.Token)
	wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
	_, err = f.Login(t.Context(), "alice", alicePassword, "", "")
	refusal := wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
	if refusal.Error() != invalidCredentials().Error() {
		t.Fatal("the refusal of a disabled account is not the one of a wrong password")
	}
	if got := f.paused(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Fatalf("the refusal of a disabled account paused %v, want one second", got)
	}
	f.principal(t, bob)
	if len(f.sessions(t)) != 2 {
		t.Fatal("a refused sign-in left a session")
	}

	f.exec(t, `UPDATE users SET disabled = 0 WHERE username = 'alice'`)
	f.principal(t, alice)
	f.login(t, "alice", alicePassword)
}

// The role of a request is the one the account has now, not the one it had
// at the sign-in.
func TestRoleIsReadAtEveryRequest(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleAdmin)
	in := f.login(t, "alice", alicePassword)
	if p := f.principal(t, in); p.Role != RoleAdmin {
		t.Fatalf("role %q", p.Role)
	}
	f.exec(t, `UPDATE users SET role = 'user' WHERE username = 'alice'`)
	if p := f.principal(t, in); p.Role != RoleUser {
		t.Fatalf("role %q after the account was made a user", p.Role)
	}
}

// §7.2, T15: a name no account has, a wrong password and a disabled account
// are the same answer, byte for byte, after the same work: each verifies a
// hash of the same cost and waits one second in the slot.
func TestRefusedSignInsAreIndistinguishable(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	f.user(t, "carol", otherPassword, RoleUser)
	f.exec(t, `UPDATE users SET disabled = 1 WHERE username = 'carol'`)

	type attempt struct{ name, username, password string }
	attempts := []attempt{
		{"a wrong password", "alice", bobPassword},
		{"no such account", "nobody", alicePassword},
		{"a disabled account, right password", "carol", otherPassword},
		{"a disabled account, wrong password", "carol", alicePassword},
		{"an empty password", "alice", ""},
		{"an empty name", "", alicePassword},
		{"a name that cannot be one", "Alice Smith <a@example.net>", alicePassword},
		{"the password of another account", "alice", otherPassword},
		{"a password with a line break after", "alice", alicePassword + "\n"},
		{"a password of a megabyte", "alice", strings.Repeat("a", 1<<20)},
		{"a name of a megabyte", strings.Repeat("a", 1<<20), alicePassword},
	}
	var first *struct {
		status        int
		code, message string
	}
	for i, a := range attempts {
		for _, signIn := range []func(context.Context, string, string, string, string) (SignIn, error){f.Login, f.CreateToken} {
			in, err := signIn(t.Context(), a.username, a.password, "device", "192.0.2.1:5000")
			refusal := wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
			if in.Token != "" || in.Session.ID != "" || in.User.ID != "" {
				t.Fatalf("%s: a refused sign-in returned something", a.name)
			}
			if len(refusal.Details) != 0 {
				t.Fatalf("%s: the refusal has details", a.name)
			}
			got := struct {
				status        int
				code, message string
			}{refusal.Status, refusal.Code, refusal.Message}
			if first == nil {
				first = &got
			}
			if got != *first {
				t.Fatalf("%s: the refusal differs from the first one", a.name)
			}
			if err.Error() != invalidCredentials().Error() {
				t.Fatalf("%s: the error text differs", a.name)
			}
		}
		if got := len(f.paused()); got != 2*(i+1) {
			t.Fatalf("%s: %d pauses after %d refusals", a.name, got, 2*(i+1))
		}
	}
	for _, d := range f.paused() {
		if d != time.Second {
			t.Fatalf("a refusal paused %s, want one second", d)
		}
	}
	if len(f.sessions(t)) != 0 {
		t.Fatal("a refused sign-in left a session")
	}
	// A sign-in that is not refused does not wait.
	f.login(t, "alice", alicePassword)
	if got := len(f.paused()); got != 2*len(attempts) {
		t.Fatal("a sign-in that was accepted paused")
	}
}

// The same path for an account that exists and one that does not: both
// compute argon2id with the cost of the server. Seen from the hash each is
// verified against: the dummy one is a real hash of that cost.
func TestUnknownUserCostsAHash(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	dummy, err := decodePHC(f.dummy)
	if err != nil {
		t.Fatal("the dummy hash is not a hash")
	}
	stored, err := decodePHC(f.passwordHash(t, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	if dummy.cost != testCost || dummy.cost != stored.cost || len(dummy.salt) != len(stored.salt) || len(dummy.hash) != len(stored.hash) {
		t.Fatal("the dummy hash has not the cost and the form of the hash of an account")
	}
	// Nobody signs in with it: not even an account called like nothing.
	for _, password := range []string{"", f.dummy, alicePassword} {
		_, err := f.Login(t.Context(), "", password, "", "")
		wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
	}
	// Two services do not share it.
	if other := f.service(t); other.dummy == f.dummy {
		t.Fatal("two services have the same dummy hash")
	}
}

// T15: a name no account has costs an argon2id computation, as a wrong
// password does. Timed with a cost that dominates everything else (the
// delay of the refusal is not waited): the fastest of a few sign-ins with
// an unknown name must take at least a third of the fastest with a wrong
// password. Skipping the hash makes it a hundred times faster.
func TestUnknownUserTakesAsLongAsAWrongPassword(t *testing.T) {
	f := newFixture(t)
	slow, err := NewService(f.store, Cost{MemoryKiB: 16 * 1024, Time: 2, Threads: 1}, f.clock.Now, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	slow.pause = func(time.Duration) {}
	if _, err := slow.CreateUser(t.Context(), "alice", alicePassword, RoleUser); err != nil {
		t.Fatal(err)
	}
	fastest := func(username string) time.Duration {
		best := time.Hour
		for range 5 {
			start := time.Now()
			_, err := slow.Login(t.Context(), username, bobPassword, "", "")
			best = min(best, time.Since(start))
			wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
		}
		return best
	}
	wrong, unknown := fastest("alice"), fastest("nobody")
	if unknown < wrong/3 {
		t.Fatalf("a sign-in with an unknown name took %s, one with a wrong password %s: the unknown name is not verified against a hash", unknown, wrong)
	}
}

// The delay of a refusal is real: with the clock of the machine, a wrong
// password and an unknown name each take at least one second, and an
// accepted sign-in does not.
func TestRefusalWaitsOneSecond(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	f.pause = time.Sleep

	for _, a := range []struct{ username, password string }{{"alice", bobPassword}, {"nobody", alicePassword}} {
		start := time.Now()
		_, err := f.Login(t.Context(), a.username, a.password, "", "")
		elapsed := time.Since(start)
		wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
		if elapsed < time.Second {
			t.Fatalf("a refusal took %s, want at least one second", elapsed)
		}
	}
	start := time.Now()
	f.login(t, "alice", alicePassword)
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("an accepted sign-in took %s", elapsed)
	}
}

// §7.2, D8: twenty sign-ins at once are checked one at a time. Each refusal
// holds the slot for its delay, so twenty of them take twenty delays.
func TestConcurrentSignInsAreSerialized(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	const delay = 20 * time.Millisecond
	var inside, most, refusals atomic.Int32
	f.pause = func(time.Duration) {
		n := inside.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		refusals.Add(1)
		time.Sleep(delay)
		inside.Add(-1)
	}

	start := time.Now()
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			// Wrong passwords, unknown names and tokens, mixed.
			username, signIn := "alice", f.Login
			if i%2 == 1 {
				username = "nobody"
			}
			if i%3 == 0 {
				signIn = f.CreateToken
			}
			_, err := signIn(t.Context(), username, bobPassword, "device", "")
			if refusalCode(err) != CodeInvalidCredentials || err.Error() != invalidCredentials().Error() {
				t.Errorf("a concurrent sign-in was not refused as the others: %v", err)
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(start)
	if refusals.Load() != 20 {
		t.Fatalf("%d refusals paused, want 20", refusals.Load())
	}
	if most.Load() != 1 {
		t.Fatalf("%d sign-ins were in the slot at once", most.Load())
	}
	if elapsed < 20*delay {
		t.Fatalf("twenty refusals took %s, less than twenty delays of %s", elapsed, delay)
	}
}

// Sign-ins that are accepted are serialized too, and every one gets its own
// session.
func TestConcurrentAcceptedSignIns(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", alicePassword, RoleUser)
	tokens := make([]string, 20)
	var wg sync.WaitGroup
	for i := range tokens {
		wg.Go(func() {
			in, err := f.Login(t.Context(), "alice", alicePassword, "", "")
			if err != nil {
				t.Errorf("a concurrent sign-in failed: %v", err)
				return
			}
			tokens[i] = in.Token
		})
	}
	wg.Wait()
	if len(f.sessions(t)) != 20 {
		t.Fatalf("%d sessions, want 20", len(f.sessions(t)))
	}
	seen := map[string]bool{}
	for _, token := range tokens {
		if seen[token] {
			t.Fatal("two sign-ins got the same token")
		}
		seen[token] = true
		if p, err := f.Authenticate(t.Context(), KindCookie, token); err != nil || p.UserID != alice.ID {
			t.Fatal("a token is of another account")
		}
	}
	if len(f.paused()) != 0 {
		t.Fatal("an accepted sign-in paused")
	}
}

// A sign-in that waits for its turn leaves when its request ends, without
// having checked anything; the one in the slot is not interrupted.
func TestSignInLeavesTheQueueWhenTheRequestEnds(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	inSlot, release := make(chan struct{}), make(chan struct{})
	f.pause = func(time.Duration) {
		close(inSlot)
		<-release
	}
	// Bounded: a refusal that does not pause must fail this test, not hang it.
	waitFor := func(c <-chan struct{}) {
		t.Helper()
		select {
		case <-c:
		case <-time.After(10 * time.Second):
			t.Fatal("the refused sign-in never paused in the slot")
		}
	}
	first := make(chan error, 1)
	go func() {
		_, err := f.Login(context.Background(), "alice", bobPassword, "", "")
		first <- err
	}()
	waitFor(inSlot)

	ctx, cancel := context.WithCancel(t.Context())
	second := make(chan error, 1)
	go func() {
		_, err := f.Login(ctx, "alice", alicePassword, "", "")
		second <- err
	}()
	select {
	case err := <-second:
		t.Fatalf("a sign-in did not wait for the slot: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	if err := <-second; !errors.Is(err, context.Canceled) {
		t.Fatalf("the waiting sign-in returned %v, want the end of its context", err)
	}
	close(release)
	wantRefusal(t, <-first, http.StatusUnauthorized, CodeInvalidCredentials)
	if len(f.sessions(t)) != 0 {
		t.Fatal("a session was created")
	}
	// The slot is free again.
	f.pause = func(time.Duration) {}
	f.login(t, "alice", alicePassword)
}

// A password that is replaced, or an account that is disabled, between the
// check of the password and the creation of the session: no session is
// created, because the change has revoked the sessions of the account
// (§7.3) and this one would outlive it.
func TestSignInRacesWithAChangeOfTheAccount(t *testing.T) {
	for name, change := range map[string]string{
		"password reset": `UPDATE users SET password_hash = 'replaced' WHERE username = 'alice'`,
		"disabled":       `UPDATE users SET disabled = 1 WHERE username = 'alice'`,
		"deleted":        `DELETE FROM users WHERE username = 'alice'`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.user(t, "alice", alicePassword, RoleUser)
			// The service reads its clock once in a sign-in, after the
			// password is verified and before the session is written.
			racing := f.service(t)
			calls := 0
			racing.now = func() time.Time {
				if calls++; calls == 1 {
					f.exec(t, change)
				}
				return f.clock.Now()
			}
			_, err := racing.Login(t.Context(), "alice", alicePassword, "", "")
			wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
			if calls != 1 {
				t.Fatalf("the clock was read %d times: the change did not come where the test wants it", calls)
			}
			if len(f.sessions(t)) != 0 {
				t.Fatal("a session was created for an account that changed during the sign-in")
			}
			if got := f.paused(); !slices.Equal(got, []time.Duration{time.Second}) {
				t.Fatalf("pauses %v, want one second", got)
			}
		})
	}
}

// §7.3: Logout revokes the session of the request, and only that.
func TestLogout(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	first, second := f.login(t, "alice", alicePassword), f.login(t, "alice", alicePassword)
	p := f.principal(t, first)
	if err := f.Logout(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	_, err := f.Authenticate(t.Context(), first.Session.Kind, first.Token)
	wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
	f.principal(t, second)
	// Twice is not an error.
	if err := f.Logout(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if rows := f.sessions(t); len(rows) != 1 || rows[0].ID != second.Session.ID {
		t.Fatal("the other session of the user is not the one left")
	}
}

// ListSessions is the live sessions of the user of the request, the newest
// first, with the current one marked; never those of another user (I6).
func TestListSessions(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	f.user(t, "bob", bobPassword, RoleAdmin)
	old := f.login(t, "alice", alicePassword)
	f.clock.advance(time.Hour)
	phone, err := f.CreateToken(t.Context(), "alice", alicePassword, "phone", "")
	if err != nil {
		t.Fatal(err)
	}
	f.clock.advance(time.Hour)
	current := f.login(t, "alice", alicePassword)
	bob := f.login(t, "bob", bobPassword)

	list, err := f.ListSessions(t.Context(), f.principal(t, current))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range list {
		ids = append(ids, s.ID)
		if s.Current != (s.ID == current.Session.ID) {
			t.Fatalf("the session %s is marked current: %t", s.ID, s.Current)
		}
	}
	if !slices.Equal(ids, []string{current.Session.ID, phone.Session.ID, old.Session.ID}) {
		t.Fatalf("sessions %v, want the three of alice, the newest first", ids)
	}
	if got := list[1]; got.Kind != KindToken || got.DeviceName == nil || *got.DeviceName != "phone" ||
		got.CreatedAt != phone.Session.CreatedAt || got.ExpiresAt != phone.Session.ExpiresAt {
		t.Fatalf("the token session is listed as %+v", got)
	}
	if got := list[2]; got.Kind != KindCookie || got.DeviceName != nil {
		t.Fatalf("the cookie session is listed as %+v", got)
	}

	// A user without sessions that are live gets an empty list, not nil.
	bobs, err := f.ListSessions(t.Context(), f.principal(t, bob))
	if err != nil || len(bobs) != 1 || bobs[0].ID != bob.Session.ID || !bobs[0].Current {
		t.Fatalf("the sessions of bob: %v %v", bobs, err)
	}
	// Expired sessions are not listed, even before the cleanup.
	p := f.principal(t, current)
	f.clock.advance(CookieLifetime - 2*time.Hour)
	list, err = f.ListSessions(t.Context(), p)
	if err != nil || len(list) != 2 || list[0].ID != current.Session.ID || list[1].ID != phone.Session.ID {
		t.Fatalf("after the first session expired: %v %v", list, err)
	}
	f.clock.advance(TokenLifetime)
	list, err = f.ListSessions(t.Context(), p)
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("after every session expired: %v %v", list, err)
	}
}

// Revoke ends one session of the user. A session of another user is the
// same 404 as one that does not exist, and is not touched (I6).
func TestRevoke(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	f.user(t, "bob", bobPassword, RoleAdmin)
	first, second := f.login(t, "alice", alicePassword), f.login(t, "alice", alicePassword)
	bob := f.login(t, "bob", bobPassword)
	p := f.principal(t, first)

	if err := f.Revoke(t.Context(), p, second.Session.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.Authenticate(t.Context(), second.Session.Kind, second.Token)
	wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
	f.principal(t, first)

	missing := wantRefusal(t, f.Revoke(t.Context(), p, second.Session.ID), http.StatusNotFound, CodeSessionNotFound)
	foreign := wantRefusal(t, f.Revoke(t.Context(), p, bob.Session.ID), http.StatusNotFound, CodeSessionNotFound)
	if missing.Message != foreign.Message || len(missing.Details) != 0 || len(foreign.Details) != 0 {
		t.Fatal("the session of another user is not answered as one that does not exist")
	}
	f.principal(t, bob)
	// Not even an admin revokes the session of another user here.
	wantRefusal(t, f.Revoke(t.Context(), f.principal(t, bob), first.Session.ID), http.StatusNotFound, CodeSessionNotFound)
	f.principal(t, first)
	for _, id := range []string{"", "x", "' OR 1=1 --", first.Token} {
		wantRefusal(t, f.Revoke(t.Context(), p, id), http.StatusNotFound, CodeSessionNotFound)
	}
	if len(f.sessions(t)) != 2 {
		t.Fatalf("%d sessions are left, want 2", len(f.sessions(t)))
	}

	// The current one can be revoked too.
	if err := f.Revoke(t.Context(), p, first.Session.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.Authenticate(t.Context(), first.Session.Kind, first.Token)
	wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
}

// §7.3: a change of password revokes every other session of the user, and
// leaves the session of the request and the sessions of everyone else.
func TestChangePasswordRevokesTheOtherSessions(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	f.user(t, "bob", bobPassword, RoleUser)
	current := f.login(t, "alice", alicePassword)
	browser := f.login(t, "alice", alicePassword)
	phone, err := f.CreateToken(t.Context(), "alice", alicePassword, "phone", "")
	if err != nil {
		t.Fatal(err)
	}
	bob := f.login(t, "bob", bobPassword)
	oldHash := f.passwordHash(t, "alice")
	f.clock.advance(time.Hour)

	p := f.principal(t, current)
	if err := f.ChangePassword(t.Context(), p, alicePassword, otherPassword); err != nil {
		t.Fatal(err)
	}
	for _, in := range []SignIn{browser, phone} {
		_, err := f.Authenticate(t.Context(), in.Session.Kind, in.Token)
		wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
	}
	f.principal(t, current)
	f.principal(t, bob)
	if rows := f.sessions(t); len(rows) != 2 {
		t.Fatalf("%d sessions are left, want the current one and the one of bob", len(rows))
	}

	_, err = f.Login(t.Context(), "alice", alicePassword, "", "")
	wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
	f.login(t, "alice", otherPassword)
	f.login(t, "bob", bobPassword)

	newHash := f.passwordHash(t, "alice")
	if newHash == oldHash || !strings.HasPrefix(newHash, "$argon2id$v=19$m=64,t=1,p=1$") || strings.Contains(newHash, otherPassword) {
		t.Fatal("the stored hash is not a new PHC hash")
	}
	var changedAt, createdAt int64
	f.query(t, `SELECT password_changed_at, created_at FROM users WHERE username = 'alice'`, &changedAt, &createdAt)
	if changedAt != f.clock.Now().UnixMilli() || createdAt != f.clock.Now().Add(-time.Hour).UnixMilli() {
		t.Fatal("password_changed_at is not the time of the change")
	}
}

// The refusals of a change of password: the new password is checked first
// and costs nothing; a wrong current password is a guess, and waits like a
// refused sign-in. Neither changes anything.
func TestChangePasswordRefusals(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	current, other := f.login(t, "alice", alicePassword), f.login(t, "alice", alicePassword)
	p := f.principal(t, current)
	hash := f.passwordHash(t, "alice")

	for name, replacement := range map[string]string{
		"too short":            "short",
		"eleven bytes":         "elevenbytes",
		"empty":                "",
		"too long":             strings.Repeat("a", 1025),
		"a line break":         "a password with\na line break",
		"a tab":                "a password with\ta tab",
		"a NUL":                "a password with\x00a NUL",
		"a C1 control":         "a password with \u0085 in it",
		"not UTF-8":            "a password that is \xff\xfe not UTF-8",
		"a delete":             "a password with \x7f in it",
		"a carriage return":    "a password ending in a CR\r",
		"an escape at the end": "a password ending in ESC\x1b",
	} {
		for _, currentPassword := range []string{alicePassword, bobPassword} {
			err := f.ChangePassword(t.Context(), p, currentPassword, replacement)
			refusal := wantRefusal(t, err, http.StatusUnprocessableEntity, CodePasswordInvalid)
			if replacement != "" && len(replacement) > 4 && strings.Contains(refusal.Message, replacement) {
				t.Errorf("%s: the refusal repeats the password", name)
			}
		}
	}
	if len(f.paused()) != 0 {
		t.Fatal("a new password that is not valid paused: it is not a guess")
	}

	for _, wrong := range []string{bobPassword, "", alicePassword + " ", strings.ToUpper(alicePassword), otherPassword} {
		err := f.ChangePassword(t.Context(), p, wrong, otherPassword)
		wantRefusal(t, err, http.StatusUnprocessableEntity, CodeCurrentPasswordInvalid)
	}
	if got := f.paused(); len(got) != 5 || slices.Min(got) != time.Second || slices.Max(got) != time.Second {
		t.Fatalf("wrong current passwords paused %v, want one second each", got)
	}
	if f.passwordHash(t, "alice") != hash {
		t.Fatal("a refused change replaced the password")
	}
	f.principal(t, other)
	f.login(t, "alice", alicePassword)

	// The user of the request is the one whose password is checked: the
	// right password of another account is a wrong one.
	f.user(t, "bob", bobPassword, RoleAdmin)
	wantRefusal(t, f.ChangePassword(t.Context(), p, bobPassword, otherPassword), http.StatusUnprocessableEntity, CodeCurrentPasswordInvalid)

	// An account deleted after the request was authenticated.
	f.exec(t, `DELETE FROM users WHERE username = 'alice'`)
	wantRefusal(t, f.ChangePassword(t.Context(), p, alicePassword, otherPassword), http.StatusUnauthorized, CodeLoginRequired)
}

// The longest and the shortest password are accepted, and are whole: no
// byte of them is dropped before the hash.
func TestPasswordBounds(t *testing.T) {
	f := newFixture(t)
	shortest := "twelve bytes"
	longest := strings.Repeat("é", 512) // 1024 bytes
	f.user(t, "short", shortest, RoleUser)
	f.user(t, "long", longest, RoleUser)
	f.login(t, "short", shortest)
	f.login(t, "long", longest)
	for _, wrong := range []string{longest[:1022], longest + "é", strings.Repeat("é", 511) + "e"} {
		_, err := f.Login(t.Context(), "long", wrong, "", "")
		wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
	}
	_, err := f.Login(t.Context(), "short", shortest[:11], "", "")
	wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
}

// Two changes of password at once with the same current password: one
// wins, and the other finds that the current password is no longer that.
func TestConcurrentChangesOfPassword(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	first, second := f.login(t, "alice", alicePassword), f.login(t, "alice", alicePassword)
	p1, p2 := f.principal(t, first), f.principal(t, second)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Go(func() { errs[0] = f.ChangePassword(t.Context(), p1, alicePassword, otherPassword) })
	wg.Go(func() { errs[1] = f.ChangePassword(t.Context(), p2, alicePassword, bobPassword) })
	wg.Wait()
	won := 0
	for i, err := range errs {
		if err == nil {
			won++
			continue
		}
		wantRefusal(t, errs[i], http.StatusUnprocessableEntity, CodeCurrentPasswordInvalid)
	}
	if won != 1 {
		t.Fatalf("%d changes won, want exactly one", won)
	}
	// The session of the winner is the one that is left, and its password
	// the one that works.
	winner, loser, password := first, second, otherPassword
	if errs[1] == nil {
		winner, loser, password = second, first, bobPassword
	}
	f.principal(t, winner)
	_, err := f.Authenticate(t.Context(), loser.Session.Kind, loser.Token)
	wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
	f.login(t, "alice", password)
}

// §7.3, §7.4: a reset of the password revokes every session of the account,
// from another process too, and at once.
func TestResetPasswordRevokesEverySession(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	f.user(t, "bob", bobPassword, RoleUser)
	sessions := []SignIn{f.login(t, "alice", alicePassword), f.login(t, "alice", alicePassword)}
	bob := f.login(t, "bob", bobPassword)

	// Another service on the same database: `vibrance user reset-password`
	// while the server runs.
	cli := f.service(t)
	if err := cli.ResetPassword(t.Context(), "alice", otherPassword); err != nil {
		t.Fatal(err)
	}
	for _, in := range sessions {
		_, err := f.Authenticate(t.Context(), in.Session.Kind, in.Token)
		wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
	}
	f.principal(t, bob)
	if rows := f.sessions(t); len(rows) != 1 || rows[0].ID != bob.Session.ID {
		t.Fatal("the sessions left are not the one of bob alone")
	}
	_, err := f.Login(t.Context(), "alice", alicePassword, "", "")
	wantRefusal(t, err, http.StatusUnauthorized, CodeInvalidCredentials)
	f.login(t, "alice", otherPassword)

	wantRefusal(t, cli.ResetPassword(t.Context(), "nobody", otherPassword), http.StatusNotFound, CodeUserNotFound)
	// The name is compared in lower case, as a sign-in compares it; a name
	// that cannot be one is refused as such, before anything is hashed.
	again := f.login(t, "alice", otherPassword)
	if err := cli.ResetPassword(t.Context(), "ALICE", otherPassword); err != nil {
		t.Fatalf("a reset of ALICE: %v", err)
	}
	_, err = f.Authenticate(t.Context(), again.Session.Kind, again.Token)
	wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
	for _, name := range []string{"-alice", "Klice", "al", "alice smith", ""} {
		wantRefusal(t, cli.ResetPassword(t.Context(), name, otherPassword), http.StatusUnprocessableEntity, CodeUsernameInvalid)
	}
	wantRefusal(t, cli.ResetPassword(t.Context(), "alice", "short"), http.StatusUnprocessableEntity, CodePasswordInvalid)
	wantRefusal(t, cli.ResetPassword(t.Context(), "alice", "a password with\na line break"), http.StatusUnprocessableEntity, CodePasswordInvalid)
	f.login(t, "alice", otherPassword)
}

func TestCreateUser(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", alicePassword, RoleAdmin)
	if alice.Username != "alice" || alice.Role != RoleAdmin || alice.Disabled || alice.CreatedAt != f.clock.Now() || len(alice.ID) != 36 {
		t.Fatalf("the account is %+v", alice)
	}
	f.clock.advance(time.Minute)
	bob := f.user(t, "bob", bobPassword, RoleUser)
	if bob.ID == alice.ID || bob.Role != RoleUser {
		t.Fatalf("the second account is %+v", bob)
	}

	_, err := f.CreateUser(t.Context(), "alice", otherPassword, RoleUser)
	wantRefusal(t, err, http.StatusConflict, CodeUsernameTaken)
	f.login(t, "alice", alicePassword) // the account is as it was
	for _, name := range []string{"", "ab", "Alice", "ALICE", "al ice", ".alice", "-alice", "_alice", "alice!", "alice@example.net",
		"àlice", strings.Repeat("a", 33), "alice\n", " alice", "alice\x00", "a/b", "a\\b"} {
		_, err := f.CreateUser(t.Context(), name, alicePassword, RoleUser)
		wantRefusal(t, err, http.StatusUnprocessableEntity, CodeUsernameInvalid)
	}
	for _, name := range []string{"abc", "0ab", "a.b", "a-b", "a_b", "a..", strings.Repeat("a", 32), "9-._"} {
		f.user(t, name, alicePassword, RoleUser)
	}
	_, err = f.CreateUser(t.Context(), "carol", "short", RoleUser)
	wantRefusal(t, err, http.StatusUnprocessableEntity, CodePasswordInvalid)
	for _, role := range []string{"", "root", "Admin", "admin "} {
		if _, err := f.CreateUser(t.Context(), "carol", alicePassword, role); err == nil {
			t.Fatalf("an account with the role %q was created", role)
		}
	}

	users, err := f.ListUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, u := range users {
		names = append(names, u.Username)
	}
	if !slices.IsSorted(names) || len(names) != 10 || !slices.Contains(names, "alice") || slices.Contains(names, "carol") {
		t.Fatalf("the accounts are %q", names)
	}
	if i := slices.IndexFunc(users, func(u User) bool { return u.Username == "alice" }); users[i] != alice {
		t.Fatalf("alice is listed as %+v", users[i])
	}
}

// Twenty requests create the same account at once: one succeeds, the others
// find the name taken.
func TestCreateUserConcurrently(t *testing.T) {
	f := newFixture(t)
	var created, taken atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, err := f.CreateUser(t.Context(), "alice", alicePassword, RoleUser)
			switch {
			case err == nil:
				created.Add(1)
			case refusalCode(err) == CodeUsernameTaken:
				taken.Add(1)
			default:
				t.Errorf("a concurrent creation failed: %v", err)
			}
		})
	}
	wg.Wait()
	if created.Load() != 1 || taken.Load() != 19 {
		t.Fatalf("%d created and %d found the name taken, want 1 and 19", created.Load(), taken.Load())
	}
	f.login(t, "alice", alicePassword)
}

// §7.3: the cleanup deletes the sessions that have expired, and no other.
func TestCleanupExpired(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	cookie := f.login(t, "alice", alicePassword)
	token, err := f.CreateToken(t.Context(), "alice", alicePassword, "phone", "")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := f.CleanupExpired(t.Context()); err != nil || n != 0 {
		t.Fatalf("the cleanup of live sessions deleted %d: %v", n, err)
	}
	f.clock.advance(CookieLifetime - time.Millisecond)
	if n, err := f.CleanupExpired(t.Context()); err != nil || n != 0 {
		t.Fatalf("the cleanup deleted %d sessions a millisecond before the first expiry: %v", n, err)
	}
	f.clock.advance(time.Millisecond)
	if n, err := f.CleanupExpired(t.Context()); err != nil || n != 1 {
		t.Fatalf("the cleanup deleted %d sessions at the expiry of the cookie, want 1: %v", n, err)
	}
	if rows := f.sessions(t); len(rows) != 1 || rows[0].ID != token.Session.ID {
		t.Fatal("the session left is not the token")
	}
	_, err = f.Authenticate(t.Context(), cookie.Session.Kind, cookie.Token)
	wantRefusal(t, err, http.StatusUnauthorized, CodeLoginRequired)
	f.principal(t, token)
	f.clock.advance(TokenLifetime)
	if n, err := f.CleanupExpired(t.Context()); err != nil || n != 1 || len(f.sessions(t)) != 0 {
		t.Fatalf("the last cleanup deleted %d sessions: %v", n, err)
	}
}

// Run deletes the expired sessions again and again, and returns when its
// context is cancelled.
func TestRunCleansUntilCancelled(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	f.cleanupEvery = 5 * time.Millisecond
	if cleanupEvery != time.Hour {
		t.Fatalf("the cleanup runs every %s, want every hour", cleanupEvery)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		f.Run(ctx)
		close(done)
	}()
	for round := range 3 {
		f.login(t, "alice", alicePassword)
		f.clock.advance(CookieLifetime)
		deadline := time.Now().Add(10 * time.Second)
		for len(f.sessions(t)) != 0 {
			if time.Now().After(deadline) {
				t.Fatalf("round %d: the expired session was not deleted", round)
			}
			time.Sleep(time.Millisecond)
		}
	}
	live := f.login(t, "alice", alicePassword)
	time.Sleep(20 * time.Millisecond)
	f.principal(t, live)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return when its context was cancelled")
	}
	for _, ev := range f.logs.events(t) {
		if ev["level"] == "ERROR" || ev["level"] == "WARN" {
			t.Fatalf("the cleanup logged %v", ev)
		}
	}
}

func TestNewServiceRefusesACostArgon2CannotRun(t *testing.T) {
	f := newFixture(t)
	for _, cost := range []Cost{{}, {MemoryKiB: 64, Time: 0, Threads: 1}, {MemoryKiB: 64, Time: 1, Threads: 0}, {MemoryKiB: 4, Time: 1, Threads: 1}} {
		if s, err := NewService(f.store, cost, f.clock.Now, nil); err == nil || s != nil {
			t.Fatalf("a service with the cost %+v was made", cost)
		}
	}
}

func TestCheckPassword(t *testing.T) {
	for _, ok := range []string{"twelve bytes", strings.Repeat("a", 1024), "pässwörd ü", "correct horse battery staple",
		"日本語日", "with   and ​ and \U0001f600"} {
		if err := CheckPassword(ok); err != nil {
			t.Errorf("a valid password of %d bytes is refused: %v", len(ok), err)
		}
	}
	for name, bad := range map[string]string{
		"empty": "", "eleven bytes": "elevenbytes", "1025 bytes": strings.Repeat("a", 1025), "a megabyte": strings.Repeat("a", 1<<20),
		"a line break at the end": "a good password\n", "a tab": "a good\tpassword", "a NUL": "a good\x00password",
		"U+0085": "a good\u0085password", "U+009F": "a good\u009fpassword", "DEL": "a good\x7fpassword",
		"not UTF-8": "a good password\xff", "a cut rune": "a good password\xc3",
	} {
		err := CheckPassword(bad)
		refusal := wantRefusal(t, err, http.StatusUnprocessableEntity, CodePasswordInvalid)
		if len(bad) >= 5 && strings.Contains(refusal.Message, bad) {
			t.Errorf("%s: the refusal repeats the password", name)
		}
	}
}

func TestFoldUsername(t *testing.T) {
	for in, want := range map[string]string{"": "", "alice": "alice", "ALICE": "alice", "A.b-C_9": "a.b-c_9", "K": "K", "À": "À", "Z[": "z["} {
		if got := FoldUsername(in); got != want {
			t.Errorf("FoldUsername(%q) = %q, want %q", in, got, want)
		}
	}
}
