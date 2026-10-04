package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"vibrance/internal/httpx"
)

// CookieName is the name of the session cookie (DESIGN.md §7.3). It is not
// the name MusicLib uses: cookies are not separated by port, and the two
// products may be on one host (T14).
const CookieName = "vibrance_session"

// Access is what an operation asks of a request (§8.3).
type Access int

const (
	// Public: no session is needed, and none is looked for.
	Public Access = iota + 1
	// Authenticated: a live session of any account that is not disabled.
	Authenticated
	// AdminOnly: a live session of an admin.
	AdminOnly
)

type principalKey struct{}

// PrincipalOf returns who the request of ctx is from, as Middleware found.
// It is false for a public operation.
func PrincipalOf(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Middleware authenticates the requests of the operations. access says what
// each one asks, by the pattern the router matched ("GET /api/v1/me"); a
// route that is not in it is refused, never served unchecked.
//
// A request carries its session as `Authorization: Bearer <token>`, a
// session of kind token, or as the cookie vibrance_session, a session of
// kind cookie. When it carries both, the bearer token
// counts and the cookie is not looked at, even if the token is no session
// (§7.3). Without a live session the answer is 401 login_required; with one
// that is not of an admin, on an operation for admins, 403 forbidden. The
// Principal is put in the context of the request, and its user in the
// access log.
//
// Neither the token nor the cookie is ever logged or answered (I5).
func (s *Service) Middleware(access map[string]Access, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			needs := access[r.Pattern]
			switch needs {
			case Public:
				next.ServeHTTP(w, r)
				return
			case Authenticated, AdminOnly:
			default:
				httpx.WriteError(w, r, log, fmt.Errorf("auth: no access rule for the route %q", r.Pattern))
				return
			}
			p, err := s.principal(r)
			if err != nil {
				httpx.WriteError(w, r, log, err)
				return
			}
			httpx.SetUser(r.Context(), p.UserID)
			if needs == AdminOnly && p.Role != RoleAdmin {
				httpx.WriteError(w, r, log, &httpx.Error{Status: http.StatusForbidden, Code: CodeForbidden,
					Message: "This operation is for admins."})
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
		})
	}
}

// principal authenticates r with the credentials it carries.
func (s *Service) principal(r *http.Request) (Principal, error) {
	err := error(loginRequired())
	kind, tokens := credentials(r)
	for _, token := range tokens {
		var p Principal
		if p, err = s.Authenticate(r.Context(), kind, token); err == nil {
			return p, nil
		}
		var refusal *httpx.Error
		if !errors.As(err, &refusal) {
			// Not a refusal: the database failed, or the request ended.
			return Principal{}, err
		}
	}
	return Principal{}, err
}

// credentials returns the kind of session r presents and its tokens: the
// bearer token alone when there is an Authorization header of that scheme,
// otherwise the value of every cookie called CookieName. A browser sends two
// of them when another site of a parent domain set one of that name: the
// one that is a session counts.
func credentials(r *http.Request) (kind string, tokens []string) {
	if token, ok := bearer(r); ok {
		return KindToken, []string{token}
	}
	for _, c := range r.CookiesNamed(CookieName) {
		tokens = append(tokens, c.Value)
	}
	return KindCookie, tokens
}

// bearer returns the token of `Authorization: Bearer <token>`. ok says that
// the request uses that scheme, whatever follows it; more than one
// Authorization header is no token at all.
func bearer(r *http.Request) (token string, ok bool) {
	values := r.Header.Values("Authorization")
	for _, v := range values {
		scheme, rest, _ := strings.Cut(v, " ")
		if strings.EqualFold(scheme, "Bearer") {
			token, ok = rest, true
		}
	}
	if ok && len(values) != 1 {
		return "", true
	}
	return token, ok
}
