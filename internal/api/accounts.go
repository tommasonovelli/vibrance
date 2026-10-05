package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"vibrance/internal/auth"
	"vibrance/internal/buildinfo"
)

// The operations of the server, of signing in and out, of the account of
// the request and of the administration of the accounts (DESIGN.md §7,
// §8.3). The rules are those of auth.Service; these only translate.

// GetServerInfo tells a client what it is talking to.
func (Server) GetServerInfo(context.Context, GetServerInfoRequestObject) (GetServerInfoResponseObject, error) {
	return GetServerInfo200JSONResponse{Body: ServerInfo{Name: ServerInfoNameVibrance, Version: buildinfo.Version,
		ApiVersion: ServerInfoApiVersionN1}}, nil
}

// Login starts a session of kind cookie and sets its cookie.
func (s Server) Login(ctx context.Context, req LoginRequestObject) (LoginResponseObject, error) {
	device := ""
	if req.Body.DeviceName != nil {
		device = *req.Body.DeviceName
	}
	in, err := s.accounts.Load().Login(ctx, req.Body.Username, req.Body.Password, device, remoteAddr(ctx))
	if err != nil {
		return nil, err
	}
	cookie := s.sessionCookie(in.Token, int(auth.CookieLifetime/time.Second))
	return Login200JSONResponse{Body: LoginResult{User: userOf(in.User), Session: sessionOf(in.Session)},
		Headers: Login200ResponseHeaders{SetCookie: cookie}}, nil
}

// CreateToken starts a session of kind token; the token is in the body.
func (s Server) CreateToken(ctx context.Context, req CreateTokenRequestObject) (CreateTokenResponseObject, error) {
	in, err := s.accounts.Load().CreateToken(ctx, req.Body.Username, req.Body.Password, req.Body.DeviceName, remoteAddr(ctx))
	if err != nil {
		return nil, err
	}
	return CreateToken201JSONResponse{Body: TokenResult{Token: in.Token, User: userOf(in.User), Session: sessionOf(in.Session)}}, nil
}

// Logout revokes the session of the request and tells the browser to drop
// its cookie. The cookie is dropped whatever the kind of the session: a
// browser that sends a token has no cookie to keep.
func (s Server) Logout(ctx context.Context, _ LogoutRequestObject) (LogoutResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.accounts.Load().Logout(ctx, p); err != nil {
		return nil, err
	}
	cookie := s.sessionCookie("", -1)
	return Logout204Response{Headers: Logout204ResponseHeaders{SetCookie: &cookie}}, nil
}

// GetMe returns the account of the request.
func (s Server) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.accounts.Load().Me(ctx, p)
	if err != nil {
		return nil, err
	}
	return GetMe200JSONResponse{Body: userOf(u)}, nil
}

// ChangePassword changes the password of the account of the request.
func (s Server) ChangePassword(ctx context.Context, req ChangePasswordRequestObject) (ChangePasswordResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.accounts.Load().ChangePassword(ctx, p, req.Body.CurrentPassword, req.Body.NewPassword); err != nil {
		return nil, err
	}
	return ChangePassword204Response{}, nil
}

// ListSessions lists the live sessions of the account of the request.
func (s Server) ListSessions(ctx context.Context, _ ListSessionsRequestObject) (ListSessionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	sessions, err := s.accounts.Load().ListSessions(ctx, p)
	if err != nil {
		return nil, err
	}
	body := SessionList{Sessions: make([]Session, 0, len(sessions))}
	for _, session := range sessions {
		body.Sessions = append(body.Sessions, sessionOf(session))
	}
	return ListSessions200JSONResponse{Body: body}, nil
}

// RevokeSession revokes a session of the account of the request.
func (s Server) RevokeSession(ctx context.Context, req RevokeSessionRequestObject) (RevokeSessionResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.accounts.Load().Revoke(ctx, p, req.Id.String()); err != nil {
		return nil, err
	}
	return RevokeSession204Response{}, nil
}

// ListUsers lists every account.
func (s Server) ListUsers(ctx context.Context, _ ListUsersRequestObject) (ListUsersResponseObject, error) {
	users, err := s.accounts.Load().ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	body := UserList{Users: make([]User, 0, len(users))}
	for _, u := range users {
		body.Users = append(body.Users, userOf(u))
	}
	return ListUsers200JSONResponse{Body: body}, nil
}

// CreateUser creates an account.
func (s Server) CreateUser(ctx context.Context, req CreateUserRequestObject) (CreateUserResponseObject, error) {
	u, err := s.accounts.Load().CreateUser(ctx, req.Body.Username, req.Body.Password, string(req.Body.Role))
	if err != nil {
		return nil, err
	}
	return CreateUser201JSONResponse{Body: userOf(u)}, nil
}

// GetUser returns one account.
func (s Server) GetUser(ctx context.Context, req GetUserRequestObject) (GetUserResponseObject, error) {
	u, err := s.accounts.Load().GetUser(ctx, req.Id.String())
	if err != nil {
		return nil, err
	}
	return GetUser200JSONResponse{Body: userOf(u)}, nil
}

// UpdateUser sets the role of an account and whether it is disabled.
func (s Server) UpdateUser(ctx context.Context, req UpdateUserRequestObject) (UpdateUserResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.accounts.Load().UpdateUser(ctx, p, req.Id.String(), string(req.Body.Role), req.Body.Disabled)
	if err != nil {
		return nil, err
	}
	return UpdateUser200JSONResponse{Body: userOf(u)}, nil
}

// DeleteUser deletes an account.
func (s Server) DeleteUser(ctx context.Context, req DeleteUserRequestObject) (DeleteUserResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.accounts.Load().DeleteUser(ctx, p, req.Id.String()); err != nil {
		return nil, err
	}
	return DeleteUser204Response{}, nil
}

// ResetUserPassword sets the password of an account.
func (s Server) ResetUserPassword(ctx context.Context, req ResetUserPasswordRequestObject) (ResetUserPasswordResponseObject, error) {
	if err := s.accounts.Load().ResetUserPassword(ctx, req.Id.String(), req.Body.Password); err != nil {
		return nil, err
	}
	return ResetUserPassword204Response{}, nil
}

// principal is who the request of ctx is from. The authentication puts it
// there before any operation that needs a session runs: without it the
// route was served without its access rule, which is a fault of the server.
func principal(ctx context.Context) (auth.Principal, error) {
	p, ok := auth.PrincipalOf(ctx)
	if !ok {
		return auth.Principal{}, errors.New("api: an operation that needs a session was reached without one")
	}
	return p, nil
}

// sessionCookie is the Set-Cookie of the session cookie (§7.3, T14):
// HttpOnly, SameSite=Strict, Path=/, Secure if and only if the public origin
// is https. A negative maxAge drops the cookie.
func (s Server) sessionCookie(token string, maxAge int) string {
	c := http.Cookie{Name: auth.CookieName, Value: token, Path: "/", MaxAge: maxAge, HttpOnly: true,
		Secure: s.secureCookie, SameSite: http.SameSiteStrictMode}
	return c.String()
}

// timestampLayout is the one form of a date in the API (T25): RFC 3339 in
// UTC, with three digits of milliseconds and Z.
const timestampLayout = "2006-01-02T15:04:05.000Z"

func timestamp(t time.Time) Timestamp { return t.UTC().Format(timestampLayout) }

func userOf(u auth.User) User {
	return User{Id: u.ID, Username: u.Username, Role: Role(u.Role), Disabled: u.Disabled, CreatedAt: timestamp(u.CreatedAt)}
}

func sessionOf(s auth.Session) Session {
	return Session{Id: s.ID, Kind: SessionKind(s.Kind), DeviceName: s.DeviceName, CreatedAt: timestamp(s.CreatedAt),
		LastUsedAt: timestamp(s.LastUsedAt), ExpiresAt: timestamp(s.ExpiresAt), Current: s.Current}
}
