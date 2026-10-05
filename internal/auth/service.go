package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// The two kinds of session (DESIGN.md §7.3), in one table: the cookie of a
// browser and the token of a client that is not one.
const (
	KindCookie = "cookie"
	KindToken  = "token"
)

const (
	// CookieLifetime and TokenLifetime are how long a session lasts from its
	// creation, and again from every renewal.
	CookieLifetime = 30 * 24 * time.Hour
	TokenLifetime  = 90 * 24 * time.Hour
	// lastUsedEvery is how often, at most, the use of a session is written:
	// there is one writer, and a write per request would queue the requests
	// of every client behind it (T16).
	lastUsedEvery = 10 * time.Minute
	// refusalDelay is how long a refused attempt holds the slot (§7.2).
	refusalDelay = time.Second
	// cleanupEvery is how often the expired sessions are deleted (§7.3).
	cleanupEvery = time.Hour
)

// User is an account, without its password hash.
type User struct {
	ID        string
	Username  string
	Role      string
	Disabled  bool
	CreatedAt time.Time
}

// Session is a session as its user sees it: the id is a public handle to
// list and to revoke it, never the token.
type Session struct {
	ID string
	// Kind is KindCookie or KindToken.
	Kind string
	// DeviceName is nil when the client gave none.
	DeviceName *string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	// Current says that it is the session of the request.
	Current bool
}

// SignIn is a session that has just begun. Token is the secret the client
// keeps, as a cookie or as a bearer token: it exists only here, once, and
// must never be logged (I5).
type SignIn struct {
	Token   string
	User    User
	Session Session
}

// Principal is who a request is from: the user, its role when the request
// was authenticated, and the session it came with.
type Principal struct {
	UserID    string
	Role      string
	SessionID string
}

// Service is the accounts and their sessions (§7).
//
// Every argon2id computation, of a sign-in, of a change of password or of a
// new account, runs in one slot (§7.2): one at a time, so that the memory
// they take is bounded and trying many passwords at once is useless. A
// refused attempt holds the slot for one second.
type Service struct {
	store *store.Store
	cost  Cost
	now   func() time.Time
	log   *slog.Logger
	slot  chan struct{}
	// dummy is a hash of the cost of this server that no password matches:
	// a sign-in with a name no account has is verified against it, so that
	// it costs what a wrong password costs (T15).
	dummy string
	// pause is time.Sleep, and cleanupEvery the constant of that name; only
	// the tests of this package replace them.
	pause        func(time.Duration)
	cleanupEvery time.Duration
}

// NewService returns the service on st. cost is the cost of the hashes it
// makes, ProductionCost outside the tests; now is the clock. It computes
// one hash, the one sign-ins with an unknown name are verified against.
func NewService(st *store.Store, cost Cost, now func() time.Time, log *slog.Logger) (*Service, error) {
	if !cost.valid() {
		return nil, errors.New("auth: the cost of argon2id is not valid")
	}
	// The password of the dummy hash is random and kept nowhere.
	nobody, _, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	dummy, err := hashPassword(nobody, cost)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return &Service{store: st, cost: cost, now: now, log: log, slot: make(chan struct{}, 1), dummy: dummy,
		pause: time.Sleep, cleanupEvery: cleanupEvery}, nil
}

// enter waits for the slot; leave gives it back.
func (s *Service) enter(ctx context.Context) error {
	select {
	case s.slot <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("auth: waiting for the turn to check a password: %w", ctx.Err())
	}
}

func (s *Service) leave() { <-s.slot }

// hash makes the hash of a password, in the slot.
func (s *Service) hash(ctx context.Context, password string) (string, error) {
	if err := s.enter(ctx); err != nil {
		return "", err
	}
	defer s.leave()
	return hashPassword(password, s.cost)
}

// noRows tells whether err says that a row is not there. Once ctx is over
// the store answers with an error that is also whatever the interrupted
// query returned: that is the end of the request, not a missing row.
func noRows(ctx context.Context, err error) bool {
	return ctx.Err() == nil && errors.Is(err, sql.ErrNoRows)
}

// errRefused is a sign-in that is refused, for any reason.
var errRefused = errors.New("refused")

// Login checks a name and a password and starts a session of kind cookie,
// which lasts CookieLifetime. deviceName is "" when the client gave none.
// remoteAddr is the address the request came from, for the log. A refusal
// is 401 invalid_credentials, whatever the reason.
func (s *Service) Login(ctx context.Context, username, password, deviceName, remoteAddr string) (SignIn, error) {
	return s.signIn(ctx, KindCookie, username, password, deviceName, remoteAddr)
}

// CreateToken is Login for a session of kind token, which lasts
// TokenLifetime.
func (s *Service) CreateToken(ctx context.Context, username, password, deviceName, remoteAddr string) (SignIn, error) {
	return s.signIn(ctx, KindToken, username, password, deviceName, remoteAddr)
}

// signIn is one attempt, in the slot. The order is the guarantee: the
// attempt waits for its turn, then reads the account, verifies the password
// and, when it is refused, waits the delay, all inside the slot. So guesses
// go one at a time and at most one per second, whatever the concurrency. The
// delay is not cut short when the client leaves: its timing tells nothing.
func (s *Service) signIn(ctx context.Context, kind, username, password, deviceName, remoteAddr string) (SignIn, error) {
	if err := s.enter(ctx); err != nil {
		return SignIn{}, err
	}
	defer s.leave()
	name := FoldUsername(username)
	in, err := s.attempt(ctx, kind, name, password, deviceName)
	switch {
	case errors.Is(err, errRefused):
		s.pause(refusalDelay)
		// The name is what the client sent: it is logged only when it has
		// the form of a name. Anything else is no account, and may be a
		// password typed in the wrong field.
		if CheckUsername(name) == nil {
			s.log.Warn("sign-in refused", "username", name, "remote_addr", remoteAddr)
		} else {
			s.log.Warn("sign-in refused", "remote_addr", remoteAddr)
		}
		return SignIn{}, invalidCredentials()
	case err != nil:
		return SignIn{}, fmt.Errorf("auth: signing in: %w", err)
	}
	s.log.Info("signed in", "username", in.User.Username, "user_id", in.User.ID, "kind", kind, "remote_addr", remoteAddr)
	return in, nil
}

// attempt verifies the password of the account called name and starts a
// session. It returns errRefused when there is no such account, the password
// is wrong or the account is disabled; the three run the same computation.
func (s *Service) attempt(ctx context.Context, kind, name, password, deviceName string) (SignIn, error) {
	var user store.User
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		user, err = q.GetUserByUsername(ctx, name)
		return err
	})
	found := true
	switch {
	case noRows(ctx, err):
		found = false
	case err != nil:
		return SignIn{}, err
	}
	encoded := s.dummy
	if found {
		encoded = user.PasswordHash
	}
	ok, err := verifyPassword(encoded, password)
	if err != nil {
		return SignIn{}, fmt.Errorf("the password hash of the account %s: %w", user.ID, err)
	}
	if !found || !ok || user.Disabled != 0 {
		return SignIn{}, errRefused
	}

	lifetime, err := lifetimeOf(kind)
	if err != nil {
		return SignIn{}, err
	}
	token, tokenHash, err := newToken()
	if err != nil {
		return SignIn{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return SignIn{}, fmt.Errorf("making the id of a session: %w", err)
	}
	now := s.now()
	row := store.CreateSessionParams{ID: id.String(), TokenHash: tokenHash, UserID: user.ID, Kind: kind,
		DeviceName: sql.NullString{String: deviceName, Valid: deviceName != ""},
		CreatedAt:  now.UnixMilli(), ExpiresAt: now.Add(lifetime).UnixMilli()}
	err = s.store.WithWriteTx(ctx, func(q *store.Queries) error {
		// The password was verified outside this transaction. A change of
		// password, a reset or a disabling that came in between has revoked
		// the sessions of the account: this one must not outlive it.
		current, err := q.GetUser(ctx, user.ID)
		switch {
		case noRows(ctx, err):
			return errRefused
		case err != nil:
			return err
		case current.Disabled != 0 || current.PasswordHash != user.PasswordHash:
			return errRefused
		}
		return q.CreateSession(ctx, row)
	})
	if err != nil {
		return SignIn{}, err
	}
	var device *string
	if deviceName != "" {
		device = &deviceName
	}
	return SignIn{Token: token, User: userOf(user), Session: Session{ID: row.ID, Kind: kind, DeviceName: device,
		CreatedAt: timeOf(row.CreatedAt), LastUsedAt: timeOf(row.CreatedAt), ExpiresAt: timeOf(row.ExpiresAt), Current: true}}, nil
}

func lifetimeOf(kind string) (time.Duration, error) {
	switch kind {
	case KindCookie:
		return CookieLifetime, nil
	case KindToken:
		return TokenLifetime, nil
	default:
		return 0, fmt.Errorf("a session of the unknown kind %q", kind)
	}
}

// timeOf is a timestamp of the database, Unix milliseconds, as a time.
func timeOf(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func userOf(u store.User) User {
	return User{ID: u.ID, Username: u.Username, Role: u.Role, Disabled: u.Disabled != 0, CreatedAt: timeOf(u.CreatedAt)}
}

// Authenticate returns who the session of a token belongs to. kind is how
// the request presented it: KindCookie for the cookie, KindToken for a
// bearer token; a session is used only the way it was made. Anything that
// is not a live session of that kind, of an account that is not disabled,
// is 401 login_required: a text that is not a token, a session that expired
// or was revoked. An account that is disabled is refused at once, whatever its
// sessions.
//
// A session whose lifetime is more than half over is renewed to a full one,
// and its last use is written at most every ten minutes (§7.3): most
// requests write nothing.
func (s *Service) Authenticate(ctx context.Context, kind, token string) (Principal, error) {
	if !wellFormed(token) {
		return Principal{}, loginRequired()
	}
	hash := hashToken(token)
	var row store.GetSessionByTokenHashRow
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		row, err = q.GetSessionByTokenHash(ctx, hash)
		return err
	})
	switch {
	case noRows(ctx, err):
		return Principal{}, loginRequired()
	case err != nil:
		return Principal{}, fmt.Errorf("auth: reading a session: %w", err)
	}
	now := s.now().UnixMilli()
	if subtle.ConstantTimeCompare([]byte(row.TokenHash), []byte(hash)) != 1 || row.Kind != kind || now >= row.ExpiresAt || row.Disabled != 0 {
		return Principal{}, loginRequired()
	}
	if err := s.touch(ctx, row, now); err != nil {
		return Principal{}, fmt.Errorf("auth: recording the use of a session: %w", err)
	}
	return Principal{UserID: row.UserID, Role: row.Role, SessionID: row.ID}, nil
}

// touch writes the use of a session and its renewal, when either is due.
func (s *Service) touch(ctx context.Context, row store.GetSessionByTokenHashRow, now int64) error {
	lifetime, err := lifetimeOf(row.Kind)
	if err != nil {
		return err
	}
	touch := store.TouchSessionParams{ID: row.ID, LastUsedAt: row.LastUsedAt, ExpiresAt: row.ExpiresAt}
	due := false
	if now-row.LastUsedAt >= lastUsedEvery.Milliseconds() {
		touch.LastUsedAt, due = now, true
	}
	if row.ExpiresAt-now < lifetime.Milliseconds()/2 {
		touch.ExpiresAt, due = now+lifetime.Milliseconds(), true
	}
	if !due {
		return nil
	}
	return s.store.WithWriteTx(ctx, func(q *store.Queries) error { return q.TouchSession(ctx, touch) })
}

// Logout revokes the session of the request. One that is gone already is
// not an error.
func (s *Service) Logout(ctx context.Context, p Principal) error {
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) error {
		_, err := q.DeleteSession(ctx, store.DeleteSessionParams{ID: p.SessionID, UserID: p.UserID})
		return err
	})
	if err != nil {
		return fmt.Errorf("auth: signing out: %w", err)
	}
	return nil
}

// ListSessions returns the live sessions of the user of the request, the
// newest first.
func (s *Service) ListSessions(ctx context.Context, p Principal) ([]Session, error) {
	var rows []store.Session
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		rows, err = q.ListSessionsOfUser(ctx, store.ListSessionsOfUserParams{UserID: p.UserID, Now: s.now().UnixMilli()})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("auth: listing the sessions: %w", err)
	}
	sessions := make([]Session, 0, len(rows))
	for _, r := range rows {
		session := Session{ID: r.ID, Kind: r.Kind, CreatedAt: timeOf(r.CreatedAt), LastUsedAt: timeOf(r.LastUsedAt),
			ExpiresAt: timeOf(r.ExpiresAt), Current: r.ID == p.SessionID}
		if r.DeviceName.Valid {
			session.DeviceName = &r.DeviceName.String
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

// Revoke ends one session of the user of the request, the current one
// included. A session that does not exist and one of another user are the
// same 404 session_not_found (I6).
func (s *Service) Revoke(ctx context.Context, p Principal, sessionID string) error {
	var revoked int64
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) (err error) {
		revoked, err = q.DeleteSession(ctx, store.DeleteSessionParams{ID: sessionID, UserID: p.UserID})
		return err
	})
	switch {
	case err != nil:
		return fmt.Errorf("auth: revoking a session: %w", err)
	case revoked == 0:
		return &httpx.Error{Status: http.StatusNotFound, Code: CodeSessionNotFound, Message: "There is no such session."}
	}
	return nil
}

// ChangePassword replaces the password of the user of the request and
// revokes every other session of that user (§7.3); the session of the
// request stays. A new password that is not valid is 422 password_invalid. A
// current password that is wrong is 422 current_password_invalid, and holds
// the slot for the delay of a refused attempt, like a sign-in: it is a
// guess at a password.
func (s *Service) ChangePassword(ctx context.Context, p Principal, current, replacement string) error {
	if err := CheckPassword(replacement); err != nil {
		return err
	}
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.leave()
	err := s.replacePassword(ctx, p, current, replacement)
	switch {
	case errors.Is(err, errRefused):
		s.pause(refusalDelay)
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: CodeCurrentPasswordInvalid,
			Message: "The current password is wrong."}
	case err != nil:
		return fmt.Errorf("auth: changing a password: %w", err)
	}
	s.log.Info("password changed", "user_id", p.UserID)
	return nil
}

// replacePassword is the work of ChangePassword, in the slot. It returns
// errRefused when the current password is not the one given.
func (s *Service) replacePassword(ctx context.Context, p Principal, current, replacement string) error {
	var user store.User
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		user, err = q.GetUser(ctx, p.UserID)
		return err
	})
	switch {
	case noRows(ctx, err):
		// The account was deleted after the request was authenticated.
		return loginRequired()
	case err != nil:
		return err
	}
	ok, err := verifyPassword(user.PasswordHash, current)
	if err != nil {
		return fmt.Errorf("the password hash of the account %s: %w", user.ID, err)
	}
	if !ok {
		return errRefused
	}
	hash, err := hashPassword(replacement, s.cost)
	if err != nil {
		return err
	}
	return s.store.WithWriteTx(ctx, func(q *store.Queries) error {
		// Only if the password is still the one that was verified: of two
		// changes at once, the second finds another current password.
		now, err := q.GetUser(ctx, p.UserID)
		switch {
		case noRows(ctx, err):
			return loginRequired()
		case err != nil:
			return err
		case now.PasswordHash != user.PasswordHash:
			return errRefused
		}
		if _, err := q.SetUserPassword(ctx, store.SetUserPasswordParams{ID: p.UserID, PasswordHash: hash,
			PasswordChangedAt: s.now().UnixMilli()}); err != nil {
			return err
		}
		_, err = q.DeleteOtherSessionsOfUser(ctx, store.DeleteOtherSessionsOfUserParams{UserID: p.UserID, ID: p.SessionID})
		return err
	})
}

// CreateUser adds an account that is not disabled. The refusals are
// 422 username_invalid, 422 password_invalid and 409 username_taken.
func (s *Service) CreateUser(ctx context.Context, username, password, role string) (User, error) {
	if err := CheckUsername(username); err != nil {
		return User{}, err
	}
	if err := CheckPassword(password); err != nil {
		return User{}, err
	}
	user, created, err := s.addUser(ctx, username, password, role, false)
	switch {
	case err != nil:
		return User{}, fmt.Errorf("auth: creating an account: %w", err)
	case !created:
		return User{}, &httpx.Error{Status: http.StatusConflict, Code: CodeUsernameTaken,
			Message: "An account with this user name exists already."}
	}
	s.log.Info("account created", "username", user.Username, "user_id", user.ID, "role", user.Role)
	return user, nil
}

// addUser hashes the password and writes the account, unless an account has
// that name; with onlyFirst, unless any account exists at all. The check
// and the insertion are one transaction, so of two that try at once one
// finds the account of the other. username and password are valid already.
func (s *Service) addUser(ctx context.Context, username, password, role string, onlyFirst bool) (User, bool, error) {
	if role != RoleAdmin && role != RoleUser {
		return User{}, false, fmt.Errorf("the unknown role %q", role)
	}
	// Outside the transaction: a write transaction is short (I11).
	hash, err := s.hash(ctx, password)
	if err != nil {
		return User{}, false, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return User{}, false, fmt.Errorf("making the id of an account: %w", err)
	}
	row := store.CreateUserParams{ID: id.String(), Username: username, PasswordHash: hash, Role: role,
		CreatedAt: s.now().UnixMilli()}
	created := false
	err = s.store.WithWriteTx(ctx, func(q *store.Queries) error {
		if onlyFirst {
			n, err := q.CountUsers(ctx)
			if err != nil || n > 0 {
				return err
			}
		}
		_, err := q.GetUserByUsername(ctx, username)
		if !noRows(ctx, err) {
			// nil: the name is taken.
			return err
		}
		created = true
		return q.CreateUser(ctx, row)
	})
	if err != nil || !created {
		return User{}, false, err
	}
	return User{ID: row.ID, Username: username, Role: role, CreatedAt: timeOf(row.CreatedAt)}, true, nil
}

// ResetPassword gives the account called username a new password and
// revokes every session of that account (§7.3, §7.4). The name is compared
// in lower case, as a sign-in does. The refusals are 422 username_invalid,
// 422 password_invalid and 404 user_not_found.
func (s *Service) ResetPassword(ctx context.Context, username, password string) error {
	name := FoldUsername(username)
	if err := CheckUsername(name); err != nil {
		return err
	}
	return s.resetPassword(ctx, password, func(q *store.Queries) (store.User, error) {
		return q.GetUserByUsername(ctx, name)
	})
}

// ResetUserPassword is ResetPassword for the account id, the reset an admin
// makes through the API.
func (s *Service) ResetUserPassword(ctx context.Context, id, password string) error {
	return s.resetPassword(ctx, password, func(q *store.Queries) (store.User, error) {
		return q.GetUser(ctx, id)
	})
}

// resetPassword gives the account that find reads a new password and
// revokes all its sessions, in one transaction.
func (s *Service) resetPassword(ctx context.Context, password string, find func(q *store.Queries) (store.User, error)) error {
	if err := CheckPassword(password); err != nil {
		return err
	}
	// Outside the transaction: a write transaction is short (I11).
	hash, err := s.hash(ctx, password)
	if err != nil {
		return err
	}
	var user store.User
	found := true
	err = s.store.WithWriteTx(ctx, func(q *store.Queries) error {
		var err error
		user, err = find(q)
		switch {
		case noRows(ctx, err):
			found = false
			return nil
		case err != nil:
			return err
		}
		if _, err := q.SetUserPassword(ctx, store.SetUserPasswordParams{ID: user.ID, PasswordHash: hash,
			PasswordChangedAt: s.now().UnixMilli()}); err != nil {
			return err
		}
		_, err = q.DeleteSessionsOfUser(ctx, user.ID)
		return err
	})
	switch {
	case err != nil:
		return fmt.Errorf("auth: resetting a password: %w", err)
	case !found:
		return userNotFound()
	}
	s.log.Info("password reset", "username", user.Username, "user_id", user.ID)
	return nil
}

// ListUsers returns every account, by name.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	var rows []store.User
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		rows, err = q.ListUsers(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("auth: listing the accounts: %w", err)
	}
	users := make([]User, 0, len(rows))
	for _, r := range rows {
		users = append(users, userOf(r))
	}
	return users, nil
}

// CleanupExpired deletes the sessions that have expired and returns how
// many they were. They were dead already: Authenticate refuses them.
func (s *Service) CleanupExpired(ctx context.Context) (int64, error) {
	var deleted int64
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) (err error) {
		deleted, err = q.DeleteExpiredSessions(ctx, s.now().UnixMilli())
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("auth: deleting the expired sessions: %w", err)
	}
	if deleted > 0 {
		s.log.Info("expired sessions deleted", "sessions", deleted)
	}
	return deleted, nil
}

// Run deletes the expired sessions every hour, until ctx is cancelled
// (§7.3). The server does it once more at its start, before it serves.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cleanupEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.CleanupExpired(ctx); err != nil && ctx.Err() == nil {
				s.log.Error("cleanup of the sessions failed", "code", "internal", "err", err.Error())
			}
		}
	}
}
